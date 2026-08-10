//go:build windows

package runtimefs

import "os"

func openAuthorityFile(path string) (*os.File, error) {
	return os.Open(path)
}
