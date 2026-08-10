//go:build darwin

package devtools

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// isMountBoundary rejects every exact mount point reported by the kernel.
// Darwin does not expose Linux statx mount IDs, so an inability to enumerate
// mounts is unsafe and fails closed.
func isMountBoundary(path string) bool {
	count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return true
	}
	filesystems := make([]unix.Statfs_t, count)
	count, err = unix.Getfsstat(filesystems, unix.MNT_NOWAIT)
	if err != nil {
		return true
	}
	path = filepath.Clean(path)
	for index := 0; index < count; index++ {
		mountpoint := filepath.Clean(unix.ByteSliceToString(filesystems[index].Mntonname[:]))
		if platformPathEqual(path, mountpoint) {
			return true
		}
	}
	return false
}
