//go:build darwin || linux

package runtimefs

import (
	"os"
)

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

func publishFileNoReplace(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = directory.Close()
	}()
	return directory.Sync()
}
