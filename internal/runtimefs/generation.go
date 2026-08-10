package runtimefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Errors returned when resolving or publishing runtime generations.
var (
	ErrInvalidCurrent     = errors.New("CURRENT does not select a valid generation")
	ErrGenerationExists   = errors.New("generation already exists")
	ErrGenerationNotReady = errors.New("generation is not structurally ready")
	ErrCrossFilesystem    = errors.New("runtime paths are not on one filesystem")
	ErrInsufficientLocks  = errors.New("generation mutation requires exclusive runtime locks")
)

var finalizeStagedRename = renameDirectoryNoReplace

// Generation binds the authoritative database and original media paths to one
// identifier. Callers must not resolve these paths independently.
type Generation struct {
	ID           string
	Root         string
	DatabasePath string
	MediaRoot    string
	OriginalsDir string
}

// CreateGeneration durably creates an empty, structurally valid generation.
// Supplying an empty identifier generates one suitable for directory use. The
// import lock prevents startup cleanup from mistaking an active partial for a
// crash remnant.
func (r *Runtime) CreateGeneration(ctx context.Context, id string, held *LeaseSet) (Generation, error) {
	if held == nil || held.root != r.Root || !held.Holds(ImportLock, Exclusive) {
		return Generation{}, ErrInsufficientLocks
	}
	return r.createGeneration(ctx, id)
}

func (r *Runtime) createGeneration(ctx context.Context, id string) (Generation, error) {
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	if id == "" {
		random, err := randomHex(8)
		if err != nil {
			return Generation{}, fmt.Errorf("generate generation id: %w", err)
		}
		id = "gen-" + time.Now().UTC().Format("20060102T150405000000000Z") + "-" + random
	}
	if err := validateGenerationID(id); err != nil {
		return Generation{}, err
	}

	parent := filepath.Join(r.Root, "generations")
	final := filepath.Join(parent, id)
	if exists, err := pathExists(final); err != nil {
		return Generation{}, err
	} else if exists {
		return Generation{}, fmt.Errorf("%w: %s", ErrGenerationExists, id)
	}

	partial, err := os.MkdirTemp(parent, ".partial-")
	if err != nil {
		return Generation{}, fmt.Errorf("create generation staging directory: %w", err)
	}
	removePartial := true
	defer func() {
		if removePartial {
			_ = os.RemoveAll(partial)
		}
	}()

	mediaRoot := filepath.Join(partial, "media")
	originals := filepath.Join(mediaRoot, "original")
	if err := os.MkdirAll(originals, 0o700); err != nil {
		return Generation{}, fmt.Errorf("create original media directory: %w", err)
	}
	databasePath := filepath.Join(partial, "moment.db")
	database, err := os.OpenFile(databasePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return Generation{}, fmt.Errorf("create generation database: %w", err)
	}
	if err := database.Sync(); err != nil {
		_ = database.Close()
		return Generation{}, fmt.Errorf("sync generation database: %w", err)
	}
	if err := database.Close(); err != nil {
		return Generation{}, fmt.Errorf("close generation database: %w", err)
	}
	for _, directory := range []string{originals, mediaRoot, partial} {
		if err := syncDirectory(directory); err != nil {
			return Generation{}, fmt.Errorf("sync generation directory: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	if err := replaceFile(partial, final); err != nil {
		return Generation{}, fmt.Errorf("publish generation: %w", err)
	}
	removePartial = false
	if err := syncDirectory(parent); err != nil {
		return Generation{}, fmt.Errorf("sync generations directory: %w", err)
	}
	return generationAt(r.Root, id), nil
}

// ResolveCurrent reads the sole authoritative pointer and validates that the
// database and media paths belong to the same complete generation.
func (r *Runtime) ResolveCurrent() (Generation, error) {
	data, err := readBoundedAuthorityFile(filepath.Join(r.Root, currentFile), maxCurrentPointerBytes)
	if err != nil {
		return Generation{}, fmt.Errorf("%w: read CURRENT: %w", ErrInvalidCurrent, err)
	}
	raw := string(data)
	id := strings.TrimSpace(raw)
	if id == "" || (raw != id && raw != id+"\n") {
		return Generation{}, fmt.Errorf("%w: malformed pointer", ErrInvalidCurrent)
	}
	if err := validateGenerationID(id); err != nil {
		return Generation{}, fmt.Errorf("%w: %w", ErrInvalidCurrent, err)
	}
	generation := generationAt(r.Root, id)
	if err := validateGeneration(generation); err != nil {
		return Generation{}, fmt.Errorf("%w: %w", ErrInvalidCurrent, err)
	}
	return generation, nil
}

// SwitchCurrent atomically selects a fully created generation. The supplied
// set must belong to this runtime and hold import, maintenance, and media
// exclusively; direct import additionally acquires ServeLock before calling.
// A successful return includes the platform metadata durability barrier.
func (r *Runtime) SwitchCurrent(ctx context.Context, id string, held *LeaseSet) error {
	if held == nil || held.root != r.Root || !held.Holds(ImportLock, Exclusive) ||
		!held.Holds(MaintenanceLock, Exclusive) || !held.Holds(MediaLock, Exclusive) {
		return ErrInsufficientLocks
	}
	return r.switchCurrent(ctx, id, switchHooks{})
}

// FinalizeStagedGeneration durably moves one completely validated import
// generation into generations/<id> without changing CURRENT. Import commit
// keeps all three mutation locks until readiness or rollback is proven.
func (r *Runtime) FinalizeStagedGeneration(ctx context.Context, staged Generation, held *LeaseSet) (Generation, error) {
	if r == nil || held == nil || held.root != r.Root || !held.Holds(ImportLock, Exclusive) ||
		!held.Holds(MaintenanceLock, Exclusive) || !held.Holds(MediaLock, Exclusive) {
		return Generation{}, ErrInsufficientLocks
	}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	if err := validateGenerationID(staged.ID); err != nil {
		return Generation{}, err
	}
	expectedRoot := filepath.Join(r.Root, "staging", "import", staged.ID, "generation")
	actualRoot, err := filepath.Abs(filepath.Clean(staged.Root))
	if err != nil {
		return Generation{}, err
	}
	expectedRoot, err = filepath.Abs(expectedRoot)
	if err != nil || actualRoot != expectedRoot {
		return Generation{}, errors.New("staged generation is outside its bound import directory")
	}
	if err := validateGeneration(staged); err != nil {
		return Generation{}, err
	}
	if err := validateGenerationTree(staged.Root); err != nil {
		return Generation{}, err
	}
	parent := filepath.Join(r.Root, "generations")
	destination := filepath.Join(parent, staged.ID)
	if _, err := os.Lstat(destination); err == nil {
		return Generation{}, fmt.Errorf("%w: %s", ErrGenerationExists, staged.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Generation{}, err
	}
	for _, directory := range []string{staged.OriginalsDir, staged.MediaRoot, staged.Root} {
		if err := syncDirectory(directory); err != nil {
			return Generation{}, fmt.Errorf("sync staged generation: %w", err)
		}
	}
	if err := finalizeStagedRename(staged.Root, destination); err != nil {
		if exists, inspectErr := pathExists(destination); inspectErr == nil && exists {
			return Generation{}, fmt.Errorf("%w: %s", ErrGenerationExists, staged.ID)
		}
		return Generation{}, fmt.Errorf("finalize staged generation: %w", err)
	}
	if err := syncDirectory(filepath.Dir(staged.Root)); err != nil {
		return Generation{}, fmt.Errorf("sync import staging after finalize: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return Generation{}, fmt.Errorf("sync generations after finalize: %w", err)
	}
	result := generationAt(r.Root, staged.ID)
	if err := validateGeneration(result); err != nil {
		return Generation{}, err
	}
	return result, nil
}

func validateGenerationTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("%w: unsafe staged member %s", ErrGenerationNotReady, filepath.Base(path))
		}
		return nil
	})
}

type switchHooks struct {
	beforeReplace func() error
	afterReplace  func() error
}

func (r *Runtime) switchCurrent(ctx context.Context, id string, hooks switchHooks) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateGenerationID(id); err != nil {
		return err
	}
	generation := generationAt(r.Root, id)
	if err := validateGeneration(generation); err != nil {
		return err
	}
	nextPath := filepath.Join(r.Root, currentFile+".next")
	if info, err := os.Lstat(nextPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse to replace symlinked CURRENT.next")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect CURRENT.next: %w", err)
	}
	next, err := os.OpenFile(nextPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open CURRENT.next: %w", err)
	}
	if err := next.Chmod(0o600); err != nil {
		_ = next.Close()
		return fmt.Errorf("protect CURRENT.next: %w", err)
	}
	if _, err := next.WriteString(id); err != nil {
		_ = next.Close()
		return fmt.Errorf("write CURRENT.next: %w", err)
	}
	if err := next.Sync(); err != nil {
		_ = next.Close()
		return fmt.Errorf("sync CURRENT.next: %w", err)
	}
	if err := next.Close(); err != nil {
		return fmt.Errorf("close CURRENT.next: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hooks.beforeReplace != nil {
		if err := hooks.beforeReplace(); err != nil {
			return err
		}
	}
	if err := replaceFile(nextPath, filepath.Join(r.Root, currentFile)); err != nil {
		return fmt.Errorf("replace CURRENT: %w", err)
	}
	if hooks.afterReplace != nil {
		if err := hooks.afterReplace(); err != nil {
			return err
		}
	}
	if err := syncDirectory(r.Root); err != nil {
		return fmt.Errorf("sync CURRENT metadata: %w", err)
	}
	r.Current = generation
	return nil
}

func generationAt(root, id string) Generation {
	generationRoot := filepath.Join(root, "generations", id)
	mediaRoot := filepath.Join(generationRoot, "media")
	return Generation{
		ID:           id,
		Root:         generationRoot,
		DatabasePath: filepath.Join(generationRoot, "moment.db"),
		MediaRoot:    mediaRoot,
		OriginalsDir: filepath.Join(mediaRoot, "original"),
	}
}

func validateGeneration(generation Generation) error {
	root, err := os.Lstat(generation.Root)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: generation directory", ErrGenerationNotReady)
	}
	database, err := os.Lstat(generation.DatabasePath)
	if err != nil || !database.Mode().IsRegular() || database.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: moment.db", ErrGenerationNotReady)
	}
	for label, path := range map[string]string{"media": generation.MediaRoot, "original media": generation.OriginalsDir} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s directory", ErrGenerationNotReady, label)
		}
	}
	return nil
}

func (r *Runtime) cleanupAfterValidCurrent() error {
	nextPath := filepath.Join(r.Root, currentFile+".next")
	if err := os.Remove(nextPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale CURRENT.next: %w", err)
	}
	if err := cleanupGenerationPartials(r.Root); err != nil {
		return err
	}
	return syncDirectory(r.Root)
}

func cleanupGenerationPartials(root string) error {
	parent := filepath.Join(root, "generations")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return fmt.Errorf("scan generation staging directories: %w", err)
	}
	removed := false
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".partial-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(parent, entry.Name())); err != nil {
			return fmt.Errorf("remove abandoned generation staging directory %s: %w", entry.Name(), err)
		}
		removed = true
	}
	if removed {
		return syncDirectory(parent)
	}
	return nil
}

// CheckSameFilesystem proves that atomic renames can cross every supplied
// directory. Each directory must already exist. The probe is always removed.
func CheckSameFilesystem(directories ...string) error {
	if len(directories) < 2 {
		return nil
	}
	probe, err := os.CreateTemp(directories[0], ".moment-fs-check-")
	if err != nil {
		return fmt.Errorf("create filesystem probe: %w", err)
	}
	if err := probe.Close(); err != nil {
		_ = os.Remove(probe.Name())
		return err
	}
	original := probe.Name()
	current := original
	defer func() {
		_ = os.Remove(current)
		_ = os.Remove(original)
	}()
	name := filepath.Base(original)
	for _, directory := range directories[1:] {
		next := filepath.Join(directory, name)
		if err := os.Rename(current, next); err != nil {
			return fmt.Errorf("%w: %s to %s: %w", ErrCrossFilesystem, current, directory, err)
		}
		current = next
	}
	if err := os.Rename(current, original); err != nil {
		return fmt.Errorf("%w: restore probe: %w", ErrCrossFilesystem, err)
	}
	current = original
	return nil
}
