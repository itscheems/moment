//go:build !windows

package runtimefs

import (
	"fmt"
	"os"
	"syscall"
)

// AvailableBytes reports bytes available to the current process on the
// filesystem containing path.
func AvailableBytes(path string) (uint64, error) {
	var state syscall.Statfs_t
	if err := syscall.Statfs(path, &state); err != nil {
		return 0, fmt.Errorf("inspect available filesystem bytes: %w", err)
	}
	return state.Bavail * uint64(state.Bsize), nil
}

// AvailableBytesRoot measures the filesystem behind an already-pinned root
// descriptor without resolving its pathname again.
func AvailableBytesRoot(root *os.Root) (uint64, error) {
	directory, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	defer func() { _ = directory.Close() }()
	var state syscall.Statfs_t
	if err := syscall.Fstatfs(int(directory.Fd()), &state); err != nil {
		return 0, fmt.Errorf("inspect available rooted filesystem bytes: %w", err)
	}
	return state.Bavail * uint64(state.Bsize), nil
}
