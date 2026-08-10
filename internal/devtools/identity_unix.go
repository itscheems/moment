//go:build !windows

package devtools

import (
	"os"
	"syscall"
)

type volumeIdentity uint64

func inspectVolumeIdentity(path string) (volumeIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, syscall.EINVAL
	}
	return volumeIdentity(stat.Dev), nil
}

func isReparsePoint(string, os.FileInfo) bool { return false }
