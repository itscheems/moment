//go:build windows

package runtimefs

func renameDirectoryNoReplace(source, destination string) error {
	return moveFile(source, destination, movefileWriteThrough)
}
