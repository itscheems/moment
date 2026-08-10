package runtimefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	publishPreparedReplace       = replaceFile
	publishPreparedNoReplace     = publishFileNoReplace
	publishPreparedSyncDirectory = syncDirectory
)

// PublishPreparedFile atomically publishes a flushed and closed temporary file
// beside its destination, then completes the platform metadata durability
// barrier. Callers must prepare the source in the destination directory.
func PublishPreparedFile(source, destination string) error {
	destinationDirectory, err := validatePreparedFile(source, destination)
	if err != nil {
		return err
	}
	if err := publishPreparedReplace(source, destination); err != nil {
		return fmt.Errorf("atomically publish prepared file: %w", err)
	}
	if err := publishPreparedSyncDirectory(destinationDirectory); err != nil {
		return fmt.Errorf("sync publication directory: %w", err)
	}
	return nil
}

// PublishPreparedFileNoReplace atomically publishes a flushed and closed
// temporary file only when destination is absent. A competing creator wins
// without being overwritten or removed by this caller.
func PublishPreparedFileNoReplace(source, destination string) error {
	destinationDirectory, err := validatePreparedFile(source, destination)
	if err != nil {
		return err
	}
	if err := publishPreparedNoReplace(source, destination); err != nil {
		return fmt.Errorf("atomically publish new prepared file: %w", err)
	}
	if err := publishPreparedSyncDirectory(destinationDirectory); err != nil {
		return fmt.Errorf("sync publication directory: %w", err)
	}
	return nil
}

// SyncDirectory completes the platform metadata durability barrier for a
// directory whose entries were created without a prepared-file publication.
func SyncDirectory(path string) error {
	if path == "" {
		return errors.New("directory path is required")
	}
	return syncDirectory(path)
}

func validatePreparedFile(source, destination string) (string, error) {
	sourceDirectory, err := filepath.Abs(filepath.Dir(source))
	if err != nil {
		return "", fmt.Errorf("resolve prepared file directory: %w", err)
	}
	destinationDirectory, err := filepath.Abs(filepath.Dir(destination))
	if err != nil {
		return "", fmt.Errorf("resolve publication directory: %w", err)
	}
	if sourceDirectory != destinationDirectory {
		return "", errors.New("prepared file must share the destination directory")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("inspect prepared file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("prepared file must be a regular non-symlink file")
	}
	return destinationDirectory, nil
}
