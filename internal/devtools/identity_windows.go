//go:build windows

package devtools

import (
	"os"

	"golang.org/x/sys/windows"
)

type volumeIdentity uint32

func inspectVolumeIdentity(path string) (volumeIdentity, error) {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(path16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, err
	}
	return volumeIdentity(info.VolumeSerialNumber), nil
}

func isReparsePoint(path string, _ os.FileInfo) bool {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(path16)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func isMountBoundary(path string) bool {
	info, err := os.Lstat(path)
	return err != nil || isReparsePoint(path, info)
}
