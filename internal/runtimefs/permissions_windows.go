//go:build windows

package runtimefs

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	windowsFileAddSubdirectory = 0x00000004
	windowsFileDeleteChild     = 0x00000040
)

func inspectPathPermissions(path string) (PathPermissions, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return PathPermissions{}, err
	}
	readAccess := uint32(windows.FILE_READ_DATA)
	writeAccess := uint32(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA)
	traverseAccess := uint32(0)
	if info.IsDir() {
		readAccess = windows.FILE_LIST_DIRECTORY
		writeAccess = windows.FILE_WRITE_DATA | windowsFileAddSubdirectory | windowsFileDeleteChild
		traverseAccess = windows.FILE_TRAVERSE
	}
	return PathPermissions{
		Read:     canOpenForAccess(path, info.IsDir(), readAccess),
		Write:    canOpenForAccess(path, info.IsDir(), writeAccess),
		Traverse: !info.IsDir() || canOpenForAccess(path, true, traverseAccess),
	}, nil
}

func canOpenForAccess(path string, directory bool, access uint32) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attributes := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if directory {
		attributes |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, attributes, 0)
	if err != nil {
		return false
	}
	return windows.CloseHandle(handle) == nil
}
