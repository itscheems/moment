//go:build windows

package runtimefs

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	movefileReplaceExisting = 0x00000001
	movefileWriteThrough    = 0x00000008
)

var (
	kernel32FS      = syscall.NewLazyDLL("kernel32.dll")
	procMoveFileExW = kernel32FS.NewProc("MoveFileExW")
)

func replaceFile(source, destination string) error {
	sourcePointer, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(destinationPointer)),
		movefileReplaceExisting|movefileWriteThrough,
	)
	if result == 0 {
		return callErr
	}
	return nil
}

func syncDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("metadata barrier target is not a directory: %s", path)
	}
	// Windows has no POSIX-style directory fsync. Every durable publication in
	// this package goes through replaceFile, whose MoveFileExW call includes
	// MOVEFILE_WRITE_THROUGH; file contents are flushed before that publication.
	return nil
}
