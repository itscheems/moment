// Package auth signs browser sessions and temporary provider state.
// Password and API token verification belongs to the account service.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

const (
	secretsFilename    = "secrets.json"
	secretsVersion     = 3
	signingKeyBytes    = 32
	csrfTokenBytes     = 32
	maxSecretsBytes    = 16 << 10
	maxSessionBytes    = 4 << 10
	maxSignedBytes     = (maxSessionBytes*8+5)/6 + 1 + (sha256.Size*8+5)/6
	defaultSessionTTL  = 12 * time.Hour
	offlineLockTimeout = 150 * time.Millisecond
)

var (
	// ErrNotInitialized reports that session signing material does not exist yet.
	ErrNotInitialized = errors.New("session signing material is not initialized")
	// ErrAlreadyInitialized prevents accidentally replacing an existing signing key.
	ErrAlreadyInitialized = errors.New("session signing material is already initialized")
	// ErrUnauthorized reports an invalid session or signed provider state.
	ErrUnauthorized = errors.New("session verification failed")
	// ErrInvalidSecrets reports malformed or unsupported persisted auth state.
	ErrInvalidSecrets = errors.New("session signing material is invalid")
	// ErrSecretPermissions reports a secret file that is not private.
	ErrSecretPermissions = errors.New("session signing material permissions are not private")
	// ErrInstanceInUse reports that an offline mutation cannot own serve.lock.
	ErrInstanceInUse = errors.New("moment server is using this instance")
)

// Options provides deterministic test seams. Zero values select cryptographic
// randomness, the system clock, and a twelve-hour browser session.
type Options struct {
	Random     io.Reader
	Now        func() time.Time
	SessionTTL time.Duration
}

// Status contains only signing initialization metadata.
type Status struct {
	Initialized   bool       `json:"initialized"`
	SessionEpoch  uint64     `json:"session_epoch"`
	InitializedAt *time.Time `json:"initialized_at,omitempty"`
}

// Session is the verified, non-secret identity carried by a signed cookie.
type Session struct {
	// UserID and SessionID name who this session belongs to and which
	// sign-in it is. Both are 32 lowercase hex digits.
	UserID    string
	SessionID string
	ExpiresAt time.Time
	Epoch     uint64
	CSRFToken string
}

// Grant proves a signature against a signing snapshot. It grants no account
// authority; the account and session must also resolve in the database.
type Grant struct {
	authority *snapshot
}

// SessionEpochAuthorized returns the epoch carried by a still-current grant.
// It lets long-lived operations identify the signing revision, while account
// authorization is always resolved from the database.
func (manager *Manager) SessionEpochAuthorized(grant Grant) (uint64, error) {
	current := manager.current.Load()
	if current == nil || grant.authority == nil || grant.authority != current {
		return 0, ErrUnauthorized
	}
	return current.sessionEpoch, nil
}

type diskSecrets struct {
	Version           int    `json:"version"`
	SessionSigningKey string `json:"session_signing_key"`
	SessionEpoch      uint64 `json:"session_epoch"`
	InitializedAtMS   int64  `json:"initialized_at_ms"`
}

type snapshot struct {
	signingKey      [signingKeyBytes]byte
	sessionEpoch    uint64
	initializedAtMS int64
}

// sessionPayload identifies the account and database session being presented.
type sessionPayload struct {
	UserID      string `json:"user_id"`
	SessionID   string `json:"session_id"`
	ExpiresAtMS int64  `json:"expires_at_ms"`
	Epoch       uint64 `json:"session_epoch"`
	CSRFToken   string `json:"csrf_token"`
}

// Subject names the account a session belongs to.
//
// It is supplied by the caller rather than resolved here on purpose. This
// package knows about signatures and deliberately owns no account credential
// storage; the transport layer, which already reaches the database on every
// request, is where an account can be looked up.
type Subject struct {
	UserID    string
	SessionID string
}

// Valid requires a complete account and session subject before anything is signed.
func (subject Subject) Valid() bool {
	return domain.ValidateUserID(subject.UserID) == nil && domain.ValidateUserID(subject.SessionID) == nil
}

// Manager serves one in-memory auth snapshot. Load is intentionally separate
// from construction so the production server can acquire serve.lock before it
// reads the instance signing material.
type Manager struct {
	root       string
	random     io.Reader
	now        func() time.Time
	sessionTTL time.Duration
	mutationMu sync.Mutex
	current    atomic.Pointer[snapshot]
}

// NewManager binds auth state to one resolved data root without reading it.
func NewManager(root string, options Options) (*Manager, error) {
	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve auth data root: %w", err)
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.SessionTTL == 0 {
		options.SessionTTL = defaultSessionTTL
	}
	if options.SessionTTL <= 0 || options.SessionTTL > 7*24*time.Hour {
		return nil, errors.New("session TTL must be greater than zero and no more than seven days")
	}
	return &Manager{
		root:       absoluteRoot,
		random:     options.Random,
		now:        options.Now,
		sessionTTL: options.SessionTTL,
	}, nil
}

// Load reads and validates secrets.json into an immutable atomic snapshot.
func (manager *Manager) Load() error {
	if manager == nil {
		return errors.New("auth manager is required")
	}
	loaded, err := readSnapshot(manager.root)
	if err != nil {
		return err
	}
	manager.current.Store(loaded)
	return nil
}

// ReadStatus reads only safe metadata directly from one data root.
func ReadStatus(root string) (Status, error) {
	loaded, err := readSnapshot(root)
	if errors.Is(err, ErrNotInitialized) {
		return Status{Initialized: false}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return loaded.status(), nil
}

// Status returns safe metadata from the live atomic snapshot.
func (manager *Manager) Status() (Status, error) {
	loaded := manager.current.Load()
	if loaded == nil {
		return Status{}, ErrNotInitialized
	}
	return loaded.status(), nil
}

// InitializeOffline creates the first credential while exclusively owning the
// server lock. A persistent but unlocked serve.lock file does not block it.
func InitializeOffline(ctx context.Context, runtimeState *runtimefs.Runtime, options Options) (Status, error) {
	if runtimeState == nil || runtimeState.Locks == nil {
		return Status{}, errors.New("resolved runtime is required")
	}
	var initialized Status
	err := WithOfflineInitializationLocks(ctx, runtimeState, func(held *runtimefs.LeaseSet) error {
		var initializeErr error
		initialized, initializeErr = InitializeOfflineWithinLocks(ctx, runtimeState, held, options)
		return initializeErr
	})
	return initialized, err
}

// WithOfflineInitializationLocks continuously owns Import -> Serve while an
// instance bootstrap operation performs its database and signing mutations.
func WithOfflineInitializationLocks(ctx context.Context, runtimeState *runtimefs.Runtime, operation func(*runtimefs.LeaseSet) error) (err error) {
	if runtimeState == nil || runtimeState.Locks == nil || operation == nil {
		return errors.New("resolved runtime and initialization operation are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lockContext, cancel := context.WithTimeout(ctx, offlineLockTimeout)
	defer cancel()
	held, acquireErr := runtimeState.Locks.AcquireSet(lockContext,
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.ServeLock, Mode: runtimefs.Exclusive},
	)
	if acquireErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(acquireErr, context.DeadlineExceeded) {
			return ErrInstanceInUse
		}
		return fmt.Errorf("acquire offline signing initialization locks: %w", acquireErr)
	}
	defer func() { err = errors.Join(err, held.Release()) }()
	return operation(held)
}

// InitializeOfflineWithinLocks publishes the first session signing key while the
// caller continuously owns this runtime's Import -> Serve initialization set.
func InitializeOfflineWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, options Options) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if runtimeState == nil || held == nil || !held.BelongsTo(runtimeState.Root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) {
		return Status{}, runtimefs.ErrInsufficientLocks
	}

	if _, err := readSnapshot(runtimeState.Root); err == nil {
		return Status{}, ErrAlreadyInitialized
	} else if !errors.Is(err, ErrNotInitialized) {
		return Status{}, err
	}
	manager, err := NewManager(runtimeState.Root, options)
	if err != nil {
		return Status{}, err
	}
	loaded, err := manager.newSnapshot()
	if err != nil {
		return Status{}, err
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if err := writeSnapshot(runtimeState.Root, loaded); err != nil {
		return Status{}, err
	}
	return loaded.status(), nil
}

// InitializeDevelopmentWithinLocks creates the first cryptographically random
// session signing key for a marked development instance while the complete
// destructive lock set is continuously held. Existing credentials are never
// rotated; callers must inspect status first and return the token exactly once.
func InitializeDevelopmentWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, authority *runtimefs.DevelopmentAuthority, held *runtimefs.LeaseSet) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if authority == nil || authority.Validate(runtimeState, held) != nil {
		return Status{}, runtimefs.ErrInsufficientLocks
	}
	root := authority.RootAuthority()
	if status, err := ReadStatusWithinRoot(root); err != nil {
		return Status{}, err
	} else if status.Initialized {
		return Status{}, ErrAlreadyInitialized
	}
	manager, err := NewManager(runtimeState.Root, Options{})
	if err != nil {
		return Status{}, err
	}
	loaded, err := manager.newSnapshot()
	if err != nil {
		return Status{}, err
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if err := writeSnapshotWithinRoot(root, loaded); err != nil {
		rollbackErr := rollbackDevelopmentSnapshot(root)
		return Status{}, errors.Join(err, rollbackErr)
	}
	status, statusErr := ReadStatusWithinRoot(root)
	if statusErr != nil || !status.Initialized || status.SessionEpoch != loaded.sessionEpoch {
		rollbackErr := rollbackDevelopmentSnapshot(root)
		return Status{}, errors.Join(errors.New("development session signing key was not durably readable"), statusErr, rollbackErr)
	}
	return loaded.status(), nil
}

// ReadStatusWithinRoot reads development signing state through a pinned root.
func ReadStatusWithinRoot(authority *os.Root) (Status, error) {
	if authority == nil {
		return Status{}, errors.New("pinned signing authority root is required")
	}
	file, err := authority.Open(secretsFilename)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Initialized: false}, nil
	}
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSecretsBytes {
		return Status{}, errors.Join(ErrInvalidSecrets, err)
	}
	var stored diskSecrets
	decoder := json.NewDecoder(io.LimitReader(file, maxSecretsBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Status{}, ErrInvalidSecrets
	}
	loaded, err := decodeSnapshot(stored)
	if err != nil {
		return Status{}, err
	}
	return loaded.status(), nil
}

func writeSnapshotWithinRoot(authority *os.Root, value *snapshot) error {
	stored := diskSecrets{
		Version:           secretsVersion,
		SessionSigningKey: base64.RawURLEncoding.EncodeToString(value.signingKey[:]), SessionEpoch: value.sessionEpoch,
		InitializedAtMS: value.initializedAtMS,
	}
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	var suffix [8]byte
	if _, err := io.ReadFull(rand.Reader, suffix[:]); err != nil {
		return err
	}
	temporary := fmt.Sprintf(".%s.%x.next", secretsFilename, suffix)
	file, err := authority.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		_ = file.Close()
		if !published {
			_ = authority.Remove(temporary)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := authority.Rename(temporary, secretsFilename); err != nil {
		return err
	}
	published = true
	directory, err := authority.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func rollbackDevelopmentSnapshot(authority *os.Root) error {
	removeErr := authority.Remove(secretsFilename)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	directory, err := authority.Open(".")
	if err != nil {
		return errors.Join(removeErr, err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(removeErr, syncErr, closeErr)
}

// IssueSessionForVerifiedSubject signs a session for an account this package
// did not authenticate itself.
//
// The caller verifies a password, account token or provider identity and
// persists the resulting session before returning the signed value.
func (manager *Manager) IssueSessionForVerifiedSubject(subject Subject) (string, Session, error) {
	manager.mutationMu.Lock()
	defer manager.mutationMu.Unlock()
	current := manager.current.Load()
	if current == nil {
		return "", Session{}, ErrNotInitialized
	}
	return manager.issueSession(current, subject)
}

func (manager *Manager) issueSession(current *snapshot, subject Subject) (string, Session, error) {
	if !subject.Valid() {
		return "", Session{}, ErrUnauthorized
	}
	csrf, err := randomToken(manager.random, csrfTokenBytes)
	if err != nil {
		return "", Session{}, fmt.Errorf("generate CSRF token: %w", err)
	}
	expiresAt := manager.now().UTC().Add(manager.sessionTTL)
	payload := sessionPayload{
		UserID:      subject.UserID,
		SessionID:   subject.SessionID,
		ExpiresAtMS: expiresAt.UnixMilli(),
		Epoch:       current.sessionEpoch,
		CSRFToken:   csrf,
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", Session{}, err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature := sign(current.signingKey[:], payloadPart)
	value := payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature)
	return value, Session{
		UserID:    subject.UserID,
		SessionID: subject.SessionID,
		ExpiresAt: expiresAt,
		Epoch:     current.sessionEpoch,
		CSRFToken: csrf,
	}, nil
}

// ValidateSession verifies signature, expiry, and the currently active epoch.
func (manager *Manager) ValidateSession(value string) (Session, error) {
	session, _, err := manager.AuthorizeSession(value)
	return session, err
}

// AuthorizeSession verifies a browser session and binds a grant to the same
// immutable authority snapshot used for signature and epoch validation.
func (manager *Manager) AuthorizeSession(value string) (Session, Grant, error) {
	current := manager.current.Load()
	if current == nil || value == "" || len(value) > maxSignedBytes {
		return Session{}, Grant{}, ErrUnauthorized
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Session{}, Grant{}, ErrUnauthorized
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(providedSignature, sign(current.signingKey[:], parts[0])) {
		return Session{}, Grant{}, ErrUnauthorized
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payloadBytes) > maxSessionBytes {
		return Session{}, Grant{}, ErrUnauthorized
	}
	var payload sessionPayload
	decoder := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Epoch == 0 || payload.CSRFToken == "" {
		return Session{}, Grant{}, ErrUnauthorized
	}
	// A cookie without a subject is one this build cannot act on. Sessions
	// issued before accounts existed decode to exactly that, so upgrading
	// signs everyone out once rather than admitting an unnamed caller.
	if !(Subject{UserID: payload.UserID, SessionID: payload.SessionID}).Valid() {
		return Session{}, Grant{}, ErrUnauthorized
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Session{}, Grant{}, ErrUnauthorized
	}
	expiresAt := time.UnixMilli(payload.ExpiresAtMS).UTC()
	if payload.Epoch != current.sessionEpoch || !expiresAt.After(manager.now().UTC()) {
		return Session{}, Grant{}, ErrUnauthorized
	}
	return Session{
		UserID:    payload.UserID,
		SessionID: payload.SessionID,
		ExpiresAt: expiresAt,
		Epoch:     payload.Epoch,
		CSRFToken: payload.CSRFToken,
	}, Grant{authority: current}, nil
}

func (manager *Manager) newSnapshot() (*snapshot, error) {
	value := &snapshot{sessionEpoch: 1, initializedAtMS: manager.now().UTC().UnixMilli()}
	if value.initializedAtMS <= 0 {
		return nil, errors.New("initialization time must be after the Unix epoch")
	}
	if _, err := io.ReadFull(manager.random, value.signingKey[:]); err != nil {
		return nil, err
	}
	return value, nil
}

func readSnapshot(root string) (*snapshot, error) {
	path := filepath.Join(root, secretsFilename)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotInitialized
		}
		return nil, fmt.Errorf("inspect session signing material: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: secrets.json is not a regular file", ErrInvalidSecrets)
	}
	if err := runtimefs.VerifyPrivateFilePermissions(path); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSecretPermissions, err)
	}
	if info.Size() <= 0 || info.Size() > maxSecretsBytes {
		return nil, fmt.Errorf("%w: unexpected file size", ErrInvalidSecrets)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session signing material: %w", err)
	}
	defer func() { _ = file.Close() }()
	var stored diskSecrets
	decoder := json.NewDecoder(io.LimitReader(file, maxSecretsBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return nil, fmt.Errorf("%w: decode secrets.json", ErrInvalidSecrets)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidSecrets)
	}
	return decodeSnapshot(stored)
}

func decodeSnapshot(stored diskSecrets) (*snapshot, error) {
	if stored.Version != secretsVersion || stored.SessionEpoch == 0 || stored.InitializedAtMS <= 0 {
		return nil, fmt.Errorf("%w: unsupported or incomplete signing state", ErrInvalidSecrets)
	}
	key, err := base64.RawURLEncoding.DecodeString(stored.SessionSigningKey)
	if err != nil || len(key) != signingKeyBytes {
		return nil, fmt.Errorf("%w: malformed signing key", ErrInvalidSecrets)
	}
	loaded := &snapshot{sessionEpoch: stored.SessionEpoch, initializedAtMS: stored.InitializedAtMS}
	copy(loaded.signingKey[:], key)
	return loaded, nil
}

func writeSnapshot(root string, value *snapshot) error {
	stored := diskSecrets{
		Version:           secretsVersion,
		SessionSigningKey: base64.RawURLEncoding.EncodeToString(value.signingKey[:]),
		SessionEpoch:      value.sessionEpoch,
		InitializedAtMS:   value.initializedAtMS,
	}
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := runtimefs.WritePrivateFileAtomic(root, secretsFilename, encoded); err != nil {
		return fmt.Errorf("persist session signing material: %w", err)
	}
	return nil
}

func (value *snapshot) status() Status {
	initializedAt := time.UnixMilli(value.initializedAtMS).UTC()
	return Status{Initialized: true, SessionEpoch: value.sessionEpoch, InitializedAt: &initializedAt}
}

func randomToken(source io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sign(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, payload)
	return mac.Sum(nil)
}

// SealState signs a short-lived value with the instance signing key and
// returns it in the same two-part encoding a session cookie uses.
//
// It exists so a federated sign-in can carry its own state -- the CSRF nonce,
// the PKCE verifier, where to return to -- in a cookie instead of in a table.
// There is no server-side scratch space in this product, and a value that lives
// for ten minutes does not justify inventing one; signing with the key that
// already authenticates sessions makes the cookie exactly as forgeable as a
// session, which is to say not.
func (manager *Manager) SealState(payload []byte) (string, error) {
	current := manager.current.Load()
	if current == nil {
		return "", ErrNotInitialized
	}
	if len(payload) == 0 || len(payload) > maxSessionBytes {
		return "", ErrUnauthorized
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := sign(current.signingKey[:], encoded)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// OpenState verifies a value produced by SealState and returns its payload.
func (manager *Manager) OpenState(value string) ([]byte, error) {
	current := manager.current.Load()
	if current == nil || value == "" || len(value) > maxSignedBytes {
		return nil, ErrUnauthorized
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrUnauthorized
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(providedSignature, sign(current.signingKey[:], parts[0])) {
		return nil, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) > maxSessionBytes {
		return nil, ErrUnauthorized
	}
	return payload, nil
}

// RandomToken serializes access to the configured entropy source.
func (manager *Manager) RandomToken(size int) (string, error) {
	if size <= 0 || size > maxSessionBytes {
		return "", ErrUnauthorized
	}
	manager.mutationMu.Lock()
	defer manager.mutationMu.Unlock()
	return randomToken(manager.random, size)
}
