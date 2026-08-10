//go:build windows

package runtimefs

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// AvailableBytes reports bytes available to the current process on the
// filesystem containing path.
func AvailableBytes(path string) (uint64, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("resolve filesystem path: %w", err)
	}
	pointer, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return 0, fmt.Errorf("encode filesystem path: %w", err)
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &available, nil, nil); err != nil {
		return 0, fmt.Errorf("inspect available filesystem bytes: %w", err)
	}
	return available, nil
}

// AvailableBytesRoot uses the pinned root's name. Development callers retain
// a separate no-share-delete handle for the complete operation on Windows.
func AvailableBytesRoot(root *os.Root) (uint64, error) {
	return AvailableBytes(root.Name())
}
