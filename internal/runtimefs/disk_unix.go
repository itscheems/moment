//go:build !windows

package runtimefs

import (
	"fmt"
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
