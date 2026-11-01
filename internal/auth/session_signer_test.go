package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

func testSubject() Subject {
	return Subject{UserID: "0123456789abcdef0123456789abcdef", SessionID: "fedcba9876543210fedcba9876543210"}
}

func initializedRuntime(t *testing.T) *runtimefs.Runtime {
	t.Helper()
	state, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func signerFixture(t *testing.T) (*Manager, *runtimefs.Runtime, *time.Time) {
	t.Helper()
	state := initializedRuntime(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	options := Options{Now: func() time.Time { return now }, SessionTTL: 30 * time.Minute}
	if _, err := InitializeOffline(context.Background(), state, options); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(state.Root, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	return manager, state, &now
}

func TestSigningMaterialContainsNoAccountCredential(t *testing.T) {
	_, state, _ := signerFixture(t)
	path := filepath.Join(state.Root, secretsFilename)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("unexpected signing metadata fields: %d", len(fields))
	}
	for _, name := range []string{"version", "session_signing_key", "session_epoch", "initialized_at_ms"} {
		if _, found := fields[name]; !found {
			t.Fatalf("missing %s", name)
		}
	}
	for _, word := range []string{"token_hash", "password_hash", "user_id", "owner"} {
		if bytes.Contains(contents, []byte(word)) {
			t.Fatalf("signing material contains account authority: %s", word)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private signing key permissions: %v", info.Mode())
	}
	if _, err := InitializeOffline(context.Background(), state, Options{}); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("signing material was replaced: %v", err)
	}
}

func TestSignedSessionPreservesSubjectAndCSRFAndSurvivesReload(t *testing.T) {
	manager, state, now := signerFixture(t)
	value, issued, err := manager.IssueSessionForVerifiedSubject(testSubject())
	if err != nil {
		t.Fatal(err)
	}
	verified, grant, err := manager.AuthorizeSession(value)
	if err != nil || verified != issued || verified.UserID != testSubject().UserID || verified.SessionID != testSubject().SessionID || verified.CSRFToken == "" {
		t.Fatalf("session verification failed: %v", err)
	}
	if !verified.ExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Fatal("session TTL changed")
	}
	if epoch, err := manager.SessionEpochAuthorized(grant); err != nil || epoch != verified.Epoch {
		t.Fatalf("signing revision: %d, %v", epoch, err)
	}
	reloaded, err := NewManager(state.Root, Options{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if after, _, err := reloaded.AuthorizeSession(value); err != nil || after != issued {
		t.Fatalf("reload changed a signed subject: %v", err)
	}
}

func TestResumedSessionDoesNotRenewOriginalExpiry(t *testing.T) {
	manager, _, now := signerFixture(t)
	_, original, err := manager.IssueSessionForVerifiedSubject(testSubject())
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(20 * time.Minute)
	if _, _, err := manager.ResumeSession(testSubject(), original.ExpiresAt, original.Epoch+1); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("signing revision mismatch was accepted", err)
	}
	value, resumed, err := manager.ResumeSession(testSubject(), original.ExpiresAt, original.Epoch)
	if err != nil || !resumed.ExpiresAt.Equal(original.ExpiresAt) || resumed.CSRFToken == original.CSRFToken {
		t.Fatalf("resume renewed authority: %+v %v", resumed, err)
	}
	if verified, err := manager.ValidateSession(value); err != nil || verified != resumed {
		t.Fatal("resumed cookie invalid", err)
	}
	*now = original.ExpiresAt
	if _, _, err := manager.ResumeSession(testSubject(), original.ExpiresAt, original.Epoch); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session resumed", err)
	}
}

func TestSignerRejectsForgedExpiredAndIncompleteSessions(t *testing.T) {
	manager, _, now := signerFixture(t)
	value, _, err := manager.IssueSessionForVerifiedSubject(testSubject())
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(value, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.ReplaceAll(payload, []byte(testSubject().UserID), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	for _, invalid := range []string{"", "unsigned", ".", parts[0] + ".bad-signature", base64.RawURLEncoding.EncodeToString(payload) + "." + parts[1], value + ".extra"} {
		if _, _, err := manager.AuthorizeSession(invalid); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalid session accepted: %v", err)
		}
	}
	*now = now.Add(time.Hour)
	if _, _, err := manager.AuthorizeSession(value); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session accepted: %v", err)
	}
	for _, subject := range []Subject{{}, {UserID: strings.Repeat("0", 32), SessionID: testSubject().SessionID}, {UserID: testSubject().UserID, SessionID: "missing"}} {
		if _, _, err := manager.IssueSessionForVerifiedSubject(subject); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("incomplete subject signed: %v", err)
		}
	}
}

func TestProviderStateUsesSigningMaterialWithoutGrantingAnAccount(t *testing.T) {
	manager, _, _ := signerFixture(t)
	for _, payload := range [][]byte{[]byte(`{"nonce":"state","return":"/"}`), bytes.Repeat([]byte("x"), maxSessionBytes)} {
		sealed, err := manager.SealState(payload)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := manager.OpenState(sealed)
		if err != nil || !bytes.Equal(opened, payload) {
			t.Fatalf("signed provider state did not round-trip: %v", err)
		}
	}
	if _, err := manager.OpenState("forged.state"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("forged provider state accepted: %v", err)
	}
	if _, err := manager.RandomToken(0); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("invalid entropy request accepted")
	}
	if value, err := manager.RandomToken(32); err != nil || len(value) != 43 {
		t.Fatal("provider randomness unavailable")
	}
}

func TestSigningInitializationRequiresOfflineInstanceOwnership(t *testing.T) {
	state := initializedRuntime(t)
	lease, err := state.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Shared)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if _, err := InitializeOffline(context.Background(), state, Options{}); !errors.Is(err, ErrInstanceInUse) {
		t.Fatalf("active instance signing material changed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InitializeOffline(ctx, state, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled initialization: %v", err)
	}
}

func TestSignerRejectsLegacyCredentialsAndInsecureKeyFiles(t *testing.T) {
	manager, state, _ := signerFixture(t)
	path := filepath.Join(state.Root, secretsFilename)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(contents, &fields); err != nil {
		t.Fatal(err)
	}
	fields["token_hash"] = "legacy-accountless-credential"
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); !errors.Is(err, ErrInvalidSecrets) {
		t.Fatalf("legacy credential field accepted: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := manager.Load(); !errors.Is(err, ErrSecretPermissions) {
			t.Fatalf("insecure key permissions accepted: %v", err)
		}
	}
}
