package runtimefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	publishPreparedReplace       = replaceFile
	publishPreparedSyncDirectory = syncDirectory
)

// PublishPreparedFile atomically publishes a flushed and closed temporary file
// beside its destination, then completes the platform metadata durability
// barrier. Callers must prepare the source in the destination directory.
func PublishPreparedFile(source, destination string) error {
	sourceDirectory, err := filepath.Abs(filepath.Dir(source))
	if err != nil {
		return fmt.Errorf("resolve prepared file directory: %w", err)
	}
	destinationDirectory, err := filepath.Abs(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("resolve publication directory: %w", err)
	}
	if sourceDirectory != destinationDirectory {
		return errors.New("prepared file must share the destination directory")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect prepared file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("prepared file must be a regular non-symlink file")
	}
	if err := publishPreparedReplace(source, destination); err != nil {
		return fmt.Errorf("atomically publish prepared file: %w", err)
	}
	if err := publishPreparedSyncDirectory(destinationDirectory); err != nil {
		return fmt.Errorf("sync publication directory: %w", err)
	}
	return nil
}
