package runtimefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var developmentBootstrapDirectories = []string{
	"generations", "cache", filepath.Join("cache", "previews"), "backups", "staging",
	filepath.Join("staging", "import"), filepath.Join("staging", "import-status"), "tmp", "locks",
}

const developmentBootstrapLock = ".bootstrap.lock"

// InitializeDevelopmentRoot starts or resumes one development bootstrap
// through a caller-pinned root handle. The development instance marker is the
// recovery authority: once it is durably published, retry accepts only the
// exact controlled layout and completes missing members without deleting any.
func InitializeDevelopmentRoot(ctx context.Context, dataDir string, authority *os.Root) (*Runtime, bool, error) {
	root, err := validateDevelopmentAuthority(dataDir, authority)
	if err != nil {
		return nil, false, err
	}
	releaseBootstrap, err := acquireDevelopmentBootstrapLock(ctx, authority)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = releaseBootstrap() }()
	entries, err := fs.ReadDir(authority.FS(), ".")
	if err != nil {
		return nil, false, err
	}
	var instance Instance
	markerCreated := false
	meaningfulEntries := entriesWithoutBootstrapLock(entries)
	if len(meaningfulEntries) == 0 {
		instance, err = newInstance(EnvironmentDevelopment)
		if err != nil {
			return nil, false, err
		}
		instanceData, marshalErr := json.MarshalIndent(instance, "", "  ")
		if marshalErr != nil {
			return nil, false, marshalErr
		}
		instanceData = append(instanceData, '\n')
		if err := writeRootedNoReplace(authority, "instance.json", instanceData, 0o600); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, false, err
			}
			data, readErr := authority.ReadFile("instance.json")
			if readErr != nil || decodeStrictInstance(data, &instance) != nil || instance.Environment != EnvironmentDevelopment {
				return nil, false, errors.Join(ErrInvalidInstance, readErr)
			}
		} else {
			markerCreated = true
		}
	} else {
		if err := validateDevelopmentBootstrapMembers(authority, entries); err != nil {
			return nil, false, err
		}
		data, readErr := authority.ReadFile("instance.json")
		if readErr != nil {
			return nil, false, errors.Join(errors.New("development bootstrap recovery marker is missing"), readErr)
		}
		if err := decodeStrictInstance(data, &instance); err != nil || instance.Environment != EnvironmentDevelopment || instance.LayoutVersion != LayoutVersion {
			return nil, false, errors.Join(ErrInvalidInstance, err)
		}
	}
	for _, path := range developmentBootstrapDirectories {
		if err := ensureRootedDirectory(authority, path); err != nil {
			return nil, false, fmt.Errorf("prepare development bootstrap directory %s: %w", filepath.Base(path), err)
		}
	}
	locker, err := NewRootedLocker(root, authority)
	if err != nil {
		return nil, false, err
	}
	held, err := locker.AcquireSet(ctx,
		LockRequest{Name: ImportLock, Mode: Exclusive}, LockRequest{Name: ServeLock, Mode: Exclusive},
		LockRequest{Name: MaintenanceLock, Mode: Exclusive}, LockRequest{Name: MediaLock, Mode: Exclusive},
	)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = held.Release() }()
	runtimeState := &Runtime{Root: root, Instance: instance, Locks: locker}
	if currentData, readErr := authority.ReadFile("CURRENT"); readErr == nil {
		id := strings.TrimSpace(string(currentData))
		generation := generationAt(root, id)
		raw := string(currentData)
		if id == "" || (raw != id && raw != id+"\n") || validateRootedGeneration(authority, id) != nil {
			return nil, false, ErrInvalidCurrent
		}
		runtimeState.Current = generation
		return runtimeState, markerCreated, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, false, readErr
	}
	generation, err := runtimeState.CreateDevelopmentGeneration(ctx, authority, held)
	if err != nil {
		return nil, false, err
	}
	if err := writeRootedAtomic(authority, "CURRENT", []byte(generation.ID+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	runtimeState.Current = generation
	return runtimeState, markerCreated, nil
}

func acquireDevelopmentBootstrapLock(ctx context.Context, authority *os.Root) (func() error, error) {
	file, err := authority.OpenFile(developmentBootstrapLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	locked, state, err := waitForFileLock(ctx, file, Exclusive)
	if err != nil {
		return nil, err
	}
	return func() error { return errors.Join(unlockFile(locked, &state), locked.Close()) }, nil
}

func entriesWithoutBootstrapLock(entries []fs.DirEntry) []fs.DirEntry {
	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() != developmentBootstrapLock {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func validateRootedGeneration(authority *os.Root, id string) error {
	if err := validateGenerationID(id); err != nil {
		return err
	}
	for _, member := range []struct {
		path      string
		directory bool
	}{
		{filepath.Join("generations", id), true}, {filepath.Join("generations", id, "moment.db"), false},
		{filepath.Join("generations", id, "media"), true}, {filepath.Join("generations", id, "media", "original"), true},
	} {
		info, err := authority.Lstat(member.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != member.directory || (!member.directory && !info.Mode().IsRegular()) {
			return errors.Join(ErrGenerationNotReady, err)
		}
	}
	return nil
}

func validateDevelopmentAuthority(dataDir string, authority *os.Root) (string, error) {
	root, err := ResolveDataDir(dataDir)
	if err != nil || authority == nil {
		return "", errors.Join(errors.New("pinned development bootstrap root is required"), err)
	}
	authorityInfo, err := authority.Stat(".")
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(authorityInfo, rootInfo) {
		return "", errors.Join(errors.New("development bootstrap handle does not match the exact root"), err)
	}
	return root, nil
}

func validateDevelopmentBootstrapMembers(root *os.Root, entries []fs.DirEntry) error {
	allowed := map[string]bool{
		"instance.json": true, "CURRENT": true, "generations": true, "cache": true,
		"backups": true, "staging": true, "tmp": true, "locks": true, developmentBootstrapLock: true,
	}
	for _, entry := range entries {
		name := entry.Name()
		if !allowed[name] && !strings.HasPrefix(name, ".CURRENT.") && !strings.HasPrefix(name, ".instance.json.") {
			return fmt.Errorf("development bootstrap contains foreign member %q", name)
		}
		info, err := root.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (name == developmentBootstrapLock && !info.Mode().IsRegular()) {
			return errors.Join(errors.New("development bootstrap member is unsafe"), err)
		}
	}
	return nil
}

func ensureRootedDirectory(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		mkdirErr := root.Mkdir(path, 0o700)
		if errors.Is(mkdirErr, os.ErrExist) {
			return ensureRootedDirectory(root, path)
		}
		return mkdirErr
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(errors.New("controlled bootstrap path is not an ordinary directory"), err)
	}
	return nil
}

func writeRootedNoReplace(root *os.Root, name string, data []byte, mode os.FileMode) error {
	random, err := randomHex(8)
	if err != nil {
		return err
	}
	temporary := "." + name + "." + random + ".next"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
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
	if err := root.Link(temporary, name); err != nil {
		return err
	}
	if err := root.Remove(temporary); err != nil {
		return err
	}
	removeTemporary = false
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func writeRootedAtomic(root *os.Root, name string, data []byte, mode os.FileMode) error {
	random, err := randomHex(8)
	if err != nil {
		return err
	}
	temporary := "." + name + "." + random + ".next"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
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
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	removeTemporary = false
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
