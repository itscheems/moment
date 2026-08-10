//go:build linux

package devtools

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// isMountBoundary uses the kernel mount ID rather than st_dev. Linux bind
// mounts commonly share a device number with their source while receiving a
// distinct mount ID.
func isMountBoundary(path string) bool {
	var child unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &child); err != nil {
		return true
	}
	var parent unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, filepath.Dir(path), unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &parent); err != nil {
		return true
	}
	return child.Mnt_id != parent.Mnt_id
}
