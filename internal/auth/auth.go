// Package auth owns Moment's single-owner bearer credential and revocable
// stateless browser sessions.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

const (
	secretsFilename    = "secrets.json"
	secretsVersion     = 1
	tokenBytes         = 32
	signingKeyBytes    = 32
	csrfTokenBytes     = 32
	maxSecretsBytes    = 16 << 10
	maxSessionBytes    = 4 << 10
	defaultSessionTTL  = 12 * time.Hour
	offlineLockTimeout = 150 * time.Millisecond
)

var (
	// ErrNotInitialized reports that owner credentials do not exist yet.
	ErrNotInitialized = errors.New("owner credentials are not initialized")
	// ErrAlreadyInitialized prevents accidentally replacing an existing owner.
	ErrAlreadyInitialized = errors.New("owner credentials are already initialized")
	// ErrUnauthorized reports an invalid bearer token or browser session.
	ErrUnauthorized = errors.New("owner authentication failed")
	// ErrInvalidSecrets reports malformed or unsupported persisted auth state.
	ErrInvalidSecrets = errors.New("owner secrets are invalid")
	// ErrSecretPermissions reports a secret file that is not private.
	ErrSecretPermissions = errors.New("owner secrets permissions are not private")
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

// Status is deliberately safe for owner status responses and diagnostics.
type Status struct {
	Initialized   bool       `json:"initialized"`
	SessionEpoch  uint64     `json:"session_epoch"`
	InitializedAt *time.Time `json:"initialized_at,omitempty"`
	RotatedAt     *time.Time `json:"rotated_at,omitempty"`
}

// Credential is returned only by initialization and rotation. Token is never
// retained by Manager or persisted by the store.
type Credential struct {
	Token         string     `json:"token"`
	Initialized   bool       `json:"initialized"`
	SessionEpoch  uint64     `json:"session_epoch"`
	InitializedAt time.Time  `json:"initialized_at"`
	RotatedAt     *time.Time `json:"rotated_at,omitempty"`
}

// Session is the verified, non-secret identity carried by a signed cookie.
type Session struct {
	ExpiresAt time.Time
	Epoch     uint64
	CSRFToken string
}

// Grant is an opaque proof that a bearer token or browser session was valid
// against one immutable authority snapshot. Callers cannot manufacture a
// usable grant; mutations must revalidate it while holding the authority lock.
type Grant struct {
	authority *snapshot
}

// SessionEpochAuthorized returns the epoch carried by a still-current grant.
// It lets long-lived owner operations bind persisted work to the exact
// authority admitted by middleware without exposing secret material.
func (manager *Manager) SessionEpochAuthorized(grant Grant) (uint64, error) {
	current := manager.current.Load()
	if current == nil || grant.authority == nil || grant.authority != current {
		return 0, ErrUnauthorized
	}
	return current.sessionEpoch, nil
}

type diskSecrets struct {
	Version           int    `json:"version"`
	TokenHash         string `json:"token_hash"`
	SessionSigningKey string `json:"session_signing_key"`
	SessionEpoch      uint64 `json:"session_epoch"`
	InitializedAtMS   int64  `json:"initialized_at_ms"`
	RotatedAtMS       *int64 `json:"rotated_at_ms,omitempty"`
}

type snapshot struct {
	tokenHash       [sha256.Size]byte
	signingKey      [signingKeyBytes]byte
	sessionEpoch    uint64
	initializedAtMS int64
	rotatedAtMS     *int64
}

type sessionPayload struct {
	ExpiresAtMS int64  `json:"expires_at_ms"`
	Epoch       uint64 `json:"session_epoch"`
	CSRFToken   string `json:"csrf_token"`
}

// Manager serves one in-memory auth snapshot. Load is intentionally separate
// from construction so the production server can acquire serve.lock before it
// reads secrets.json, eliminating the offline-rotation startup race.
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
func InitializeOffline(ctx context.Context, runtimeState *runtimefs.Runtime, options Options) (Credential, error) {
	if runtimeState == nil || runtimeState.Locks == nil {
		return Credential{}, errors.New("resolved runtime is required")
	}
	var initialized Credential
	err := WithOfflineInitializationLocks(ctx, runtimeState, func(held *runtimefs.LeaseSet) error {
		var initializeErr error
		initialized, initializeErr = InitializeOfflineWithinLocks(ctx, runtimeState, held, options)
		return initializeErr
	})
	return initialized, err
}

// WithOfflineInitializationLocks continuously owns Import -> Serve while an
// instance-wide owner bootstrap operation resolves CURRENT and performs its
// database and credential mutations.
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
		return fmt.Errorf("acquire offline owner initialization locks: %w", acquireErr)
	}
	defer func() { err = errors.Join(err, held.Release()) }()
	return operation(held)
}

// InitializeOfflineWithinLocks publishes the first owner credential while the
// caller continuously owns this runtime's Import -> Serve initialization set.
func InitializeOfflineWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, options Options) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if runtimeState == nil || held == nil || !held.BelongsTo(runtimeState.Root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) {
		return Credential{}, runtimefs.ErrInsufficientLocks
	}

	if _, err := readSnapshot(runtimeState.Root); err == nil {
		return Credential{}, ErrAlreadyInitialized
	} else if !errors.Is(err, ErrNotInitialized) {
		return Credential{}, err
	}
	manager, err := NewManager(runtimeState.Root, options)
	if err != nil {
		return Credential{}, err
	}
	token, loaded, err := manager.newSnapshot(nil)
	if err != nil {
		return Credential{}, err
	}
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if err := writeSnapshot(runtimeState.Root, loaded); err != nil {
		return Credential{}, err
	}
	return credential(token, loaded), nil
}

// InitializeDevelopmentWithinLocks creates the first cryptographically random
// owner credential for a marked development instance while the complete
// destructive lock set is continuously held. Existing credentials are never
// rotated; callers must inspect status first and return the token exactly once.
func InitializeDevelopmentWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, authority *runtimefs.DevelopmentAuthority, held *runtimefs.LeaseSet) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if authority == nil || authority.Validate(runtimeState, held) != nil {
		return Credential{}, runtimefs.ErrInsufficientLocks
	}
	root := authority.RootAuthority()
	if status, err := ReadStatusWithinRoot(root); err != nil {
		return Credential{}, err
	} else if status.Initialized {
		return Credential{}, ErrAlreadyInitialized
	}
	manager, err := NewManager(runtimeState.Root, Options{})
	if err != nil {
		return Credential{}, err
	}
	token, loaded, err := manager.newSnapshot(nil)
	if err != nil {
		return Credential{}, err
	}
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if err := writeSnapshotWithinRoot(root, loaded); err != nil {
		rollbackErr := rollbackDevelopmentSnapshot(root)
		return Credential{}, errors.Join(err, rollbackErr)
	}
	status, statusErr := ReadStatusWithinRoot(root)
	if statusErr != nil || !status.Initialized || status.SessionEpoch != loaded.sessionEpoch {
		rollbackErr := rollbackDevelopmentSnapshot(root)
		return Credential{}, errors.Join(errors.New("development owner credential was not durably readable"), statusErr, rollbackErr)
	}
	return credential(token, loaded), nil
}

// ReadStatusWithinRoot reads development owner state through a pinned root.
func ReadStatusWithinRoot(authority *os.Root) (Status, error) {
	if authority == nil {
		return Status{}, errors.New("pinned owner authority root is required")
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
		Version: secretsVersion, TokenHash: base64.RawURLEncoding.EncodeToString(value.tokenHash[:]),
		SessionSigningKey: base64.RawURLEncoding.EncodeToString(value.signingKey[:]), SessionEpoch: value.sessionEpoch,
		InitializedAtMS: value.initializedAtMS, RotatedAtMS: cloneInt64(value.rotatedAtMS),
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

// RotateOffline changes the bearer hash and epoch while excluding a running
// server. The signing key remains stable; the epoch revokes every old cookie.
func RotateOffline(ctx context.Context, runtimeState *runtimefs.Runtime, options Options) (Credential, error) {
	if runtimeState == nil || runtimeState.Locks == nil {
		return Credential{}, errors.New("resolved runtime is required")
	}
	lease, err := acquireOfflineLease(ctx, runtimeState.Locks)
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = lease.Release() }()

	manager, err := NewManager(runtimeState.Root, options)
	if err != nil {
		return Credential{}, err
	}
	current, err := readSnapshot(runtimeState.Root)
	if err != nil {
		return Credential{}, err
	}
	token, next, err := manager.newSnapshot(current)
	if err != nil {
		return Credential{}, err
	}
	if err := writeSnapshot(runtimeState.Root, next); err != nil {
		return Credential{}, err
	}
	return credential(token, next), nil
}

func (manager *Manager) rotateCurrent(current *snapshot) (Credential, error) {
	token, next, err := manager.newSnapshot(current)
	if err != nil {
		return Credential{}, err
	}
	if err := writeSnapshot(manager.root, next); err != nil {
		return Credential{}, err
	}
	manager.current.Store(next)
	return credential(token, next), nil
}

// AuthenticateBearer verifies the exact bearer value in constant time.
func (manager *Manager) AuthenticateBearer(token string) bool {
	_, err := manager.AuthorizeBearer(token)
	return err == nil
}

// AuthorizeBearer verifies the exact bearer value and binds the resulting
// grant to the same immutable authority snapshot used for verification.
func (manager *Manager) AuthorizeBearer(token string) (Grant, error) {
	current := manager.current.Load()
	if !authenticateBearer(current, token) {
		return Grant{}, ErrUnauthorized
	}
	return Grant{authority: current}, nil
}

// IssueSession exchanges a valid bearer for a signed, expiring, epoch-bound
// browser session plus a CSRF token embedded in its authenticated payload.
func (manager *Manager) IssueSession(token string) (string, Session, error) {
	manager.mutationMu.Lock()
	defer manager.mutationMu.Unlock()
	current := manager.current.Load()
	if !authenticateBearer(current, token) {
		return "", Session{}, ErrUnauthorized
	}
	csrf, err := randomToken(manager.random, csrfTokenBytes)
	if err != nil {
		return "", Session{}, fmt.Errorf("generate CSRF token: %w", err)
	}
	expiresAt := manager.now().UTC().Add(manager.sessionTTL)
	payload := sessionPayload{ExpiresAtMS: expiresAt.UnixMilli(), Epoch: current.sessionEpoch, CSRFToken: csrf}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", Session{}, err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature := sign(current.signingKey[:], payloadPart)
	value := payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature)
	return value, Session{ExpiresAt: expiresAt, Epoch: current.sessionEpoch, CSRFToken: csrf}, nil
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
	if current == nil || value == "" || len(value) > maxSessionBytes {
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
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Session{}, Grant{}, ErrUnauthorized
	}
	expiresAt := time.UnixMilli(payload.ExpiresAtMS).UTC()
	if payload.Epoch != current.sessionEpoch || !expiresAt.After(manager.now().UTC()) {
		return Session{}, Grant{}, ErrUnauthorized
	}
	return Session{ExpiresAt: expiresAt, Epoch: payload.Epoch, CSRFToken: payload.CSRFToken}, Grant{authority: current}, nil
}

// RotateAuthorized changes the live authority only if the caller's grant still
// names the current immutable snapshot. Verification and mutation therefore
// form one authority decision even when requests were admitted concurrently.
func (manager *Manager) RotateAuthorized(grant Grant) (Credential, error) {
	manager.mutationMu.Lock()
	defer manager.mutationMu.Unlock()
	current := manager.current.Load()
	if current == nil {
		return Credential{}, ErrNotInitialized
	}
	if grant.authority == nil || grant.authority != current {
		return Credential{}, ErrUnauthorized
	}
	return manager.rotateCurrent(current)
}

func (manager *Manager) newSnapshot(previous *snapshot) (string, *snapshot, error) {
	token, err := randomToken(manager.random, tokenBytes)
	if err != nil {
		return "", nil, fmt.Errorf("generate owner token: %w", err)
	}
	nowMS := manager.now().UTC().UnixMilli()
	next := &snapshot{initializedAtMS: nowMS, sessionEpoch: 1}
	if previous == nil {
		if _, err := io.ReadFull(manager.random, next.signingKey[:]); err != nil {
			return "", nil, fmt.Errorf("generate session signing key: %w", err)
		}
	} else {
		if previous.sessionEpoch == math.MaxUint64 {
			return "", nil, fmt.Errorf("%w: session epoch exhausted", ErrInvalidSecrets)
		}
		next.signingKey = previous.signingKey
		next.initializedAtMS = previous.initializedAtMS
		next.sessionEpoch = previous.sessionEpoch + 1
		next.rotatedAtMS = &nowMS
	}
	next.tokenHash = sha256.Sum256([]byte(token))
	return token, next, nil
}

func acquireOfflineLease(ctx context.Context, locker *runtimefs.Locker) (*runtimefs.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lockContext, cancel := context.WithTimeout(ctx, offlineLockTimeout)
	defer cancel()
	lease, err := locker.Acquire(lockContext, runtimefs.ServeLock, runtimefs.Exclusive)
	if err == nil {
		return lease, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrInstanceInUse
	}
	return nil, fmt.Errorf("acquire offline owner lock: %w", err)
}

func readSnapshot(root string) (*snapshot, error) {
	path := filepath.Join(root, secretsFilename)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotInitialized
		}
		return nil, fmt.Errorf("inspect owner secrets: %w", err)
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
		return nil, fmt.Errorf("open owner secrets: %w", err)
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
		return nil, fmt.Errorf("%w: unsupported or incomplete state", ErrInvalidSecrets)
	}
	hash, err := base64.RawURLEncoding.DecodeString(stored.TokenHash)
	if err != nil || len(hash) != sha256.Size {
		return nil, fmt.Errorf("%w: malformed token hash", ErrInvalidSecrets)
	}
	key, err := base64.RawURLEncoding.DecodeString(stored.SessionSigningKey)
	if err != nil || len(key) != signingKeyBytes {
		return nil, fmt.Errorf("%w: malformed signing key", ErrInvalidSecrets)
	}
	if stored.RotatedAtMS != nil && *stored.RotatedAtMS < stored.InitializedAtMS {
		return nil, fmt.Errorf("%w: rotation precedes initialization", ErrInvalidSecrets)
	}
	loaded := &snapshot{
		sessionEpoch:    stored.SessionEpoch,
		initializedAtMS: stored.InitializedAtMS,
		rotatedAtMS:     cloneInt64(stored.RotatedAtMS),
	}
	copy(loaded.tokenHash[:], hash)
	copy(loaded.signingKey[:], key)
	return loaded, nil
}

func writeSnapshot(root string, value *snapshot) error {
	stored := diskSecrets{
		Version:           secretsVersion,
		TokenHash:         base64.RawURLEncoding.EncodeToString(value.tokenHash[:]),
		SessionSigningKey: base64.RawURLEncoding.EncodeToString(value.signingKey[:]),
		SessionEpoch:      value.sessionEpoch,
		InitializedAtMS:   value.initializedAtMS,
		RotatedAtMS:       cloneInt64(value.rotatedAtMS),
	}
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := runtimefs.WritePrivateFileAtomic(root, secretsFilename, encoded); err != nil {
		return fmt.Errorf("persist owner secrets: %w", err)
	}
	return nil
}

func (value *snapshot) status() Status {
	initializedAt := time.UnixMilli(value.initializedAtMS).UTC()
	status := Status{Initialized: true, SessionEpoch: value.sessionEpoch, InitializedAt: &initializedAt}
	if value.rotatedAtMS != nil {
		rotatedAt := time.UnixMilli(*value.rotatedAtMS).UTC()
		status.RotatedAt = &rotatedAt
	}
	return status
}

func credential(token string, value *snapshot) Credential {
	status := value.status()
	return Credential{
		Token:         token,
		Initialized:   status.Initialized,
		SessionEpoch:  status.SessionEpoch,
		InitializedAt: *status.InitializedAt,
		RotatedAt:     status.RotatedAt,
	}
}

func randomToken(source io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func authenticateBearer(current *snapshot, token string) bool {
	if current == nil || token == "" {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(digest[:], current.tokenHash[:]) == 1
}

func sign(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, payload)
	return mac.Sum(nil)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
