//go:build darwin

package runtimefs

import "golang.org/x/sys/unix"

func renameDirectoryNoReplace(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
