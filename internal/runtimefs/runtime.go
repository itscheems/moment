// Package runtimefs owns Moment's durable data-directory and generation layout.
package runtimefs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// LayoutVersion identifies the on-disk instance metadata understood by this
	// runtime. Database schema versions are owned by the SQLite layer instead.
	LayoutVersion = 1

	instanceFile = "instance.json"
	currentFile  = "CURRENT"
)

// Errors returned while resolving persistent instance metadata.
var (
	ErrIncompleteLayout    = errors.New("data directory is only partially initialized")
	ErrInvalidInstance     = errors.New("invalid instance metadata")
	ErrEnvironmentMismatch = errors.New("instance environment does not match configuration")
)

// Environment marks whether destructive development-only operations may ever
// target an instance. The marker is metadata, not an authorization mechanism.
type Environment string

const (
	// EnvironmentProduction marks a normal data directory.
	EnvironmentProduction Environment = "production"
	// EnvironmentDevelopment explicitly marks a disposable development directory.
	EnvironmentDevelopment Environment = "development"
)

// Config selects the data directory and the marker used for a new instance.
// An empty DataDir resolves to the platform application-data default. An empty
// Environment defaults to production.
type Config struct {
	DataDir     string
	Environment Environment
}

// Instance is the non-secret identity stored in instance.json.
type Instance struct {
	ID            string      `json:"instance_id"`
	Environment   Environment `json:"environment"`
	LayoutVersion int         `json:"layout_version"`
	InitializedAt time.Time   `json:"initialized_at"`
}

// Runtime is a resolved, structurally ready Moment data directory.
type Runtime struct {
	Root     string
	Instance Instance
	Current  Generation
	Locks    *Locker
}

// IsDevelopment reports whether the persistent instance marker explicitly
// permits development-only tooling.
func (r *Runtime) IsDevelopment() bool {
	return r != nil && r.Instance.Environment == EnvironmentDevelopment
}

// ResolveDataDir returns an absolute data directory. Explicit paths always win.
func ResolveDataDir(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(filepath.Clean(explicit))
	}

	var base string
	var err error
	switch runtime.GOOS {
	case "darwin":
		base, err = os.UserHomeDir()
		if err == nil {
			base = filepath.Join(base, "Library", "Application Support")
		}
	case "windows":
		base, err = os.UserConfigDir()
	default:
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base, err = os.UserHomeDir()
			if err == nil {
				base = filepath.Join(base, ".local", "share")
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("resolve platform data directory: %w", err)
	}
	if base == "" {
		return "", errors.New("platform data directory is empty")
	}
	return filepath.Abs(filepath.Join(base, "moment"))
}

// Initialize creates a new crash-safe layout. A compatible, complete existing
// layout is returned unchanged so callers may safely retry after losing their
// own response. Partial initialization is completed only when instance.json is
// valid and CURRENT has not yet been created.
func Initialize(ctx context.Context, config Config) (*Runtime, error) {
	root, environment, err := resolveConfig(config)
	if err != nil {
		return nil, err
	}
	if err := ensureBaseDirectories(root); err != nil {
		return nil, err
	}
	locks, err := NewLocker(root)
	if err != nil {
		return nil, err
	}
	lease, err := locks.Acquire(ctx, ImportLock, Exclusive)
	if err != nil {
		return nil, fmt.Errorf("acquire initialization lock: %w", err)
	}
	defer func() {
		_ = lease.Release() // initialization errors remain authoritative
	}()
	if err := CheckSameFilesystem(root, filepath.Join(root, "generations"), filepath.Join(root, "staging", "import")); err != nil {
		return nil, err
	}

	instancePath := filepath.Join(root, instanceFile)
	currentPath := filepath.Join(root, currentFile)
	instanceExists, err := pathExists(instancePath)
	if err != nil {
		return nil, err
	}
	currentExists, err := pathExists(currentPath)
	if err != nil {
		return nil, err
	}

	if currentExists && !instanceExists {
		return nil, fmt.Errorf("%w: CURRENT exists without instance.json", ErrIncompleteLayout)
	}

	var instance Instance
	if instanceExists {
		instance, err = readInstance(instancePath)
		if err != nil {
			return nil, err
		}
		if instance.Environment != environment {
			return nil, fmt.Errorf("%w: stored=%s configured=%s", ErrEnvironmentMismatch, instance.Environment, environment)
		}
	} else {
		instance, err = newInstance(environment)
		if err != nil {
			return nil, err
		}
		if err := writeJSONAtomic(root, instanceFile, instance); err != nil {
			return nil, fmt.Errorf("write instance metadata: %w", err)
		}
	}

	if currentExists {
		return openResolved(root, instance, locks)
	}

	manager := &Runtime{Root: root, Instance: instance, Locks: locks}
	generation, err := manager.CreateGeneration(ctx, "")
	if err != nil {
		return nil, err
	}
	if err := manager.switchCurrent(ctx, generation.ID, switchHooks{}); err != nil {
		return nil, fmt.Errorf("select initial generation: %w", err)
	}
	manager.Current = generation
	return manager, nil
}

// Open resolves an initialized runtime with a non-cancelable startup context.
func Open(dataDir string) (*Runtime, error) {
	return OpenContext(context.Background(), dataDir)
}

// OpenContext resolves an initialized runtime. It briefly serializes stale
// pointer cleanup with import coordination and never guesses another generation
// when CURRENT is absent or invalid.
func OpenContext(ctx context.Context, dataDir string) (*Runtime, error) {
	root, err := ResolveDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	if err := ensureBaseDirectories(root); err != nil {
		return nil, err
	}
	instance, err := readInstance(filepath.Join(root, instanceFile))
	if err != nil {
		return nil, err
	}
	locks, err := NewLocker(root)
	if err != nil {
		return nil, err
	}
	lease, err := locks.Acquire(ctx, ImportLock, Exclusive)
	if err != nil {
		return nil, fmt.Errorf("coordinate runtime startup: %w", err)
	}
	defer func() {
		_ = lease.Release()
	}()
	return openResolved(root, instance, locks)
}

func openResolved(root string, instance Instance, locks *Locker) (*Runtime, error) {
	manager := &Runtime{Root: root, Instance: instance, Locks: locks}
	generation, err := manager.ResolveCurrent()
	if err != nil {
		return nil, err
	}
	manager.Current = generation
	if err := manager.cleanupAfterValidCurrent(); err != nil {
		return nil, err
	}
	return manager, nil
}

func resolveConfig(config Config) (string, Environment, error) {
	root, err := ResolveDataDir(config.DataDir)
	if err != nil {
		return "", "", err
	}
	environment := config.Environment
	if environment == "" {
		environment = EnvironmentProduction
	}
	if environment != EnvironmentProduction && environment != EnvironmentDevelopment {
		return "", "", fmt.Errorf("unsupported environment %q", environment)
	}
	return root, environment, nil
}

func ensureBaseDirectories(root string) error {
	paths := []string{
		root,
		filepath.Join(root, "generations"),
		filepath.Join(root, "cache", "previews"),
		filepath.Join(root, "backups"),
		filepath.Join(root, "staging", "import"),
		filepath.Join(root, "tmp"),
		filepath.Join(root, "locks"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create runtime directory %s: %w", path, err)
		}
	}
	return nil
}

func newInstance(environment Environment) (Instance, error) {
	id, err := randomHex(16)
	if err != nil {
		return Instance{}, fmt.Errorf("generate instance id: %w", err)
	}
	return Instance{
		ID:            id,
		Environment:   environment,
		LayoutVersion: LayoutVersion,
		InitializedAt: time.Now().UTC(),
	}, nil
}

func readInstance(path string) (Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Instance{}, fmt.Errorf("%w: instance.json is missing", ErrIncompleteLayout)
		}
		return Instance{}, fmt.Errorf("read instance metadata: %w", err)
	}
	var instance Instance
	if err := json.Unmarshal(data, &instance); err != nil {
		return Instance{}, fmt.Errorf("%w: decode instance.json: %w", ErrInvalidInstance, err)
	}
	if instance.LayoutVersion != LayoutVersion || len(instance.ID) != 32 || instance.InitializedAt.IsZero() {
		return Instance{}, fmt.Errorf("%w: unsupported or incomplete instance.json", ErrInvalidInstance)
	}
	if _, err := hex.DecodeString(instance.ID); err != nil {
		return Instance{}, fmt.Errorf("%w: malformed instance id", ErrInvalidInstance)
	}
	if instance.Environment != EnvironmentProduction && instance.Environment != EnvironmentDevelopment {
		return Instance{}, fmt.Errorf("%w: unsupported environment %q", ErrInvalidInstance, instance.Environment)
	}
	return instance, nil
}

func writeJSONAtomic(root, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(root, "."+name+"-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, filepath.Join(root, name)); err != nil {
		return err
	}
	removeTemporary = false
	return syncDirectory(root)
}

func randomHex(byteCount int) (string, error) {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect %s: %w", path, err)
}

func validateGenerationID(id string) error {
	if id == "" || len(id) > 128 || id == "." || id == ".." || strings.TrimSpace(id) != id {
		return fmt.Errorf("invalid generation id %q", id)
	}
	for _, character := range id {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return fmt.Errorf("invalid generation id %q", id)
	}
	return nil
}
