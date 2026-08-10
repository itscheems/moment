package devtools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/moment/moment/internal/runtimefs"
)

type generationGuard struct {
	generation runtimefs.Generation
	pinned     map[string]os.FileInfo
}

func pinGeneration(target *Target, generation runtimefs.Generation) (*generationGuard, error) {
	if err := target.Verify(); err != nil {
		return nil, err
	}
	expectedParent := filepath.Join(target.Root, "generations")
	if !platformPathEqual(filepath.Dir(generation.Root), expectedParent) || filepath.Base(generation.Root) != generation.ID || generation.ID == target.Current.ID {
		return nil, fs.ErrInvalid
	}
	paths := []string{generation.Root, generation.DatabasePath, generation.MediaRoot, generation.OriginalsDir}
	pinned := make(map[string]os.FileInfo, len(paths))
	for _, path := range paths {
		if err := rejectControlledPath(target.Root, path); err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(path, info) {
			return nil, errors.Join(fs.ErrInvalid, err)
		}
		volume, err := inspectVolumeIdentity(path)
		if err != nil || volume != target.rootVolume || isMountBoundary(path) {
			return nil, errors.Join(fs.ErrInvalid, err)
		}
		pinned[path] = info
	}
	return &generationGuard{generation: generation, pinned: pinned}, nil
}

func (guard *generationGuard) Verify(target *Target) error {
	if guard == nil {
		return fs.ErrInvalid
	}
	if err := target.Verify(); err != nil {
		return err
	}
	for path, expected := range guard.pinned {
		if err := rejectControlledPath(target.Root, path); err != nil {
			return err
		}
		actual, err := os.Lstat(path)
		if err != nil || actual.Mode()&os.ModeSymlink != 0 || isReparsePoint(path, actual) || !os.SameFile(expected, actual) {
			return errors.Join(fs.ErrInvalid, err)
		}
		volume, err := inspectVolumeIdentity(path)
		if err != nil || volume != target.rootVolume || isMountBoundary(path) {
			return errors.Join(fs.ErrInvalid, err)
		}
	}
	return nil
}

func (guard *generationGuard) authority(target *Target) runtimefs.DevelopmentGenerationAuthority {
	if guard == nil {
		return runtimefs.DevelopmentGenerationAuthority{}
	}
	generation := guard.generation
	return runtimefs.DevelopmentGenerationAuthority{
		ID: generation.ID, InstanceRoot: target.rootInfo, Root: guard.pinned[generation.Root], Database: guard.pinned[generation.DatabasePath],
		Media: guard.pinned[generation.MediaRoot], Originals: guard.pinned[generation.OriginalsDir],
	}
}

func targetGenerationAuthority(target *Target) runtimefs.DevelopmentGenerationAuthority {
	if target == nil {
		return runtimefs.DevelopmentGenerationAuthority{}
	}
	generation := target.Current
	return runtimefs.DevelopmentGenerationAuthority{
		ID: generation.ID, InstanceRoot: target.rootInfo, Root: target.pinned[generation.Root], Database: target.pinned[generation.DatabasePath],
		Media: target.pinned[generation.MediaRoot], Originals: target.pinned[generation.OriginalsDir],
	}
}
