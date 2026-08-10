package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

type stagedRandomReader struct {
	mu      sync.Mutex
	call    int
	entered [2]chan struct{}
	release [2]chan struct{}
}

func newStagedRandomReader() *stagedRandomReader {
	return &stagedRandomReader{
		entered: [2]chan struct{}{make(chan struct{}), make(chan struct{})},
		release: [2]chan struct{}{make(chan struct{}), make(chan struct{})},
	}
}

func (reader *stagedRandomReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	call := reader.call
	reader.call++
	reader.mu.Unlock()
	if call >= len(reader.entered) {
		return 0, errors.New("unexpected random read")
	}
	close(reader.entered[call])
	<-reader.release[call]
	for index := range buffer {
		buffer[index] = byte(call + 1)
	}
	return len(buffer), nil
}

func initializedRuntime(t *testing.T) *runtimefs.Runtime {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return runtimeState
}

func initializeOwner(t *testing.T, runtimeState *runtimefs.Runtime, now time.Time) Credential {
	t.Helper()
	credential, err := InitializeOffline(context.Background(), runtimeState, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func TestOfflineInitializationPersistsOnlyProtectedSecretMaterial(t *testing.T) {
	runtimeState := initializedRuntime(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	credential := initializeOwner(t, runtimeState, now)

	decoded, err := base64.RawURLEncoding.DecodeString(credential.Token)
	if err != nil || len(decoded) != tokenBytes {
		t.Fatalf("token has %d decoded bytes, err = %v", len(decoded), err)
	}
	secretPath := filepath.Join(runtimeState.Root, secretsFilename)
	contents, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), credential.Token) {
		t.Fatal("raw owner token was persisted")
	}
	for _, field := range []string{"token_hash", "session_signing_key", "session_epoch", "initialized_at_ms"} {
		if !strings.Contains(string(contents), `"`+field+`"`) {
			t.Fatalf("secrets.json missing %q", field)
		}
	}
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets mode = %04o", info.Mode().Perm())
	}

	status, err := ReadStatus(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Initialized || status.SessionEpoch != 1 || status.InitializedAt == nil || !status.InitializedAt.Equal(now) || status.RotatedAt != nil {
		t.Fatalf("status = %#v", status)
	}
	if _, err := InitializeOffline(context.Background(), runtimeState, Options{}); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second initialization error = %v", err)
	}
	contentsAfter, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contentsAfter) != string(contents) {
		t.Fatal("second initialization changed secrets.json")
	}
}

func TestBearerSessionsExpireAndRotationRevokesAllOldCredentials(t *testing.T) {
	runtimeState := initializedRuntime(t)
	clock := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	credential := initializeOwner(t, runtimeState, clock)
	manager, err := NewManager(runtimeState.Root, Options{
		Now:        func() time.Time { return clock },
		SessionTTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if !manager.AuthenticateBearer(credential.Token) || manager.AuthenticateBearer(credential.Token+"x") {
		t.Fatal("bearer authentication mismatch")
	}
	cookie, session, err := manager.IssueSession(credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if session.Epoch != 1 || session.CSRFToken == "" || !session.ExpiresAt.Equal(clock.Add(15*time.Minute)) {
		t.Fatalf("session = %#v", session)
	}
	verified, err := manager.ValidateSession(cookie)
	if err != nil || verified.CSRFToken != session.CSRFToken {
		t.Fatalf("verified = %#v, err = %v", verified, err)
	}
	if _, err := manager.ValidateSession(cookie + "x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered session error = %v", err)
	}

	grant, err := manager.AuthorizeBearer(credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := manager.RotateAuthorized(grant)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == credential.Token || rotated.SessionEpoch != 2 {
		t.Fatalf("rotated = %#v", rotated)
	}
	if manager.AuthenticateBearer(credential.Token) || !manager.AuthenticateBearer(rotated.Token) {
		t.Fatal("rotation did not replace bearer authority")
	}
	if _, err := manager.ValidateSession(cookie); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old session error = %v", err)
	}
	if _, _, err := manager.IssueSession(credential.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old bearer session exchange error = %v", err)
	}

	newCookie, _, err := manager.IssueSession(rotated.Token)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(16 * time.Minute)
	if _, err := manager.ValidateSession(newCookie); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session error = %v", err)
	}
}

func TestOfflineRotationUsesLockOwnershipNotLockFileExistence(t *testing.T) {
	runtimeState := initializedRuntime(t)
	original := initializeOwner(t, runtimeState, time.Now())
	secretPath := filepath.Join(runtimeState.Root, secretsFilename)
	before, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RotateOffline(context.Background(), runtimeState, Options{}); !errors.Is(err, ErrInstanceInUse) {
		t.Fatalf("rotation while server lock held error = %v", err)
	}
	afterFailure, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFailure) != string(before) {
		t.Fatal("failed offline rotation drifted secrets.json")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeState.Root, "locks", "serve.lock")); err != nil {
		t.Fatalf("persistent lock file missing: %v", err)
	}
	rotated, err := RotateOffline(context.Background(), runtimeState, Options{})
	if err != nil {
		t.Fatalf("stale lock filename blocked rotation: %v", err)
	}
	if rotated.Token == original.Token || rotated.SessionEpoch != 2 {
		t.Fatalf("rotated = %#v", rotated)
	}
}

func TestConcurrentOnlineRotationsRequireCurrentAuthorityGrant(t *testing.T) {
	runtimeState := initializedRuntime(t)
	original := initializeOwner(t, runtimeState, time.Now())
	manager, err := NewManager(runtimeState.Root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}

	const attempts = 12
	grants := make([]Grant, attempts)
	for index := range grants {
		grants[index], err = manager.AuthorizeBearer(original.Token)
		if err != nil {
			t.Fatal(err)
		}
	}
	credentials := make(chan Credential, attempts)
	errorsFound := make(chan error, attempts)
	var group sync.WaitGroup
	for _, grant := range grants {
		grant := grant
		group.Add(1)
		go func() {
			defer group.Done()
			credential, err := manager.RotateAuthorized(grant)
			if err != nil {
				errorsFound <- err
				return
			}
			credentials <- credential
		}()
	}
	group.Wait()
	close(credentials)
	close(errorsFound)
	var successful []Credential
	for credential := range credentials {
		successful = append(successful, credential)
	}
	if len(successful) != 1 {
		t.Fatalf("successful rotations = %d, want 1", len(successful))
	}
	unauthorized := 0
	for err := range errorsFound {
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatal(err)
		}
		unauthorized++
	}
	if unauthorized != attempts-1 {
		t.Fatalf("unauthorized rotations = %d, want %d", unauthorized, attempts-1)
	}
	rotated := successful[0]
	if rotated.SessionEpoch != 2 {
		t.Fatalf("rotated epoch = %d", rotated.SessionEpoch)
	}
	if !manager.AuthenticateBearer(rotated.Token) || manager.AuthenticateBearer(original.Token) {
		t.Fatal("rotation did not publish exactly one new bearer authority")
	}
	reloaded, err := NewManager(runtimeState.Root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	status, err := reloaded.Status()
	if err != nil || status.SessionEpoch != 2 || !reloaded.AuthenticateBearer(rotated.Token) {
		t.Fatalf("reloaded status = %#v, err = %v", status, err)
	}
}

func TestStaleSessionGrantCannotRotateCurrentAuthority(t *testing.T) {
	runtimeState := initializedRuntime(t)
	original := initializeOwner(t, runtimeState, time.Now())
	manager, err := NewManager(runtimeState.Root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	cookie, _, err := manager.IssueSession(original.Token)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := manager.AuthorizeSession(cookie)
	if err != nil {
		t.Fatal(err)
	}
	_, stale, err := manager.AuthorizeSession(cookie)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := manager.RotateAuthorized(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RotateAuthorized(stale); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale session grant rotation error = %v", err)
	}
	status, err := manager.Status()
	if err != nil || status.SessionEpoch != 2 || !manager.AuthenticateBearer(rotated.Token) {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestSessionIssuanceAndRotationShareOneAuthorityDecision(t *testing.T) {
	runtimeState := initializedRuntime(t)
	original := initializeOwner(t, runtimeState, time.Now())
	random := newStagedRandomReader()
	manager, err := NewManager(runtimeState.Root, Options{Random: random})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	grant, err := manager.AuthorizeBearer(original.Token)
	if err != nil {
		t.Fatal(err)
	}

	type sessionResult struct {
		cookie  string
		session Session
		err     error
	}
	issued := make(chan sessionResult, 1)
	go func() {
		cookie, session, issueErr := manager.IssueSession(original.Token)
		issued <- sessionResult{cookie: cookie, session: session, err: issueErr}
	}()
	<-random.entered[0]

	type rotationOutcome struct {
		credential Credential
		err        error
	}
	rotated := make(chan rotationOutcome, 1)
	go func() {
		credential, rotateErr := manager.RotateAuthorized(grant)
		rotated <- rotationOutcome{credential: credential, err: rotateErr}
	}()
	select {
	case <-random.entered[1]:
		t.Fatal("rotation entered random generation while session issuance held the authority lock")
	case <-time.After(25 * time.Millisecond):
	}

	close(random.release[0])
	select {
	case <-random.entered[1]:
	case <-time.After(time.Second):
		t.Fatal("rotation did not proceed after session issuance completed")
	}
	issueResult := <-issued
	if issueResult.err != nil || issueResult.session.Epoch != 1 {
		t.Fatalf("issued session = %#v, err = %v", issueResult.session, issueResult.err)
	}
	if _, err := manager.ValidateSession(issueResult.cookie); err != nil {
		t.Fatalf("session was not valid at issuance: %v", err)
	}

	close(random.release[1])
	rotateResult := <-rotated
	if rotateResult.err != nil || rotateResult.credential.SessionEpoch != 2 {
		t.Fatalf("rotation = %#v, err = %v", rotateResult.credential, rotateResult.err)
	}
	if _, err := manager.ValidateSession(issueResult.cookie); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("issued session survived rotation: %v", err)
	}
}

func TestSecretValidationRejectsInsecureModeAndSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACLs rather than POSIX mode bits")
	}
	runtimeState := initializedRuntime(t)
	initializeOwner(t, runtimeState, time.Now())
	secretPath := filepath.Join(runtimeState.Root, secretsFilename)
	if err := os.Chmod(secretPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStatus(runtimeState.Root); !errors.Is(err, ErrSecretPermissions) {
		t.Fatalf("insecure permission error = %v", err)
	}

	contents, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	realPath := filepath.Join(runtimeState.Root, "elsewhere.json")
	if err := os.WriteFile(realPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, secretPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStatus(runtimeState.Root); !errors.Is(err, ErrInvalidSecrets) {
		t.Fatalf("symlink error = %v", err)
	}
}
