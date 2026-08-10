//go:build darwin || linux

package runtimefs

import "os"

func inspectPathPermissions(path string) (PathPermissions, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return PathPermissions{}, err
	}
	permissions := info.Mode().Perm()
	result := PathPermissions{Read: permissions&0o400 != 0, Write: permissions&0o200 != 0}
	if info.IsDir() {
		result.Traverse = permissions&0o100 != 0
	} else {
		result.Traverse = true
	}
	return result, nil
}
