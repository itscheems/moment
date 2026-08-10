//go:build windows

package runtimefs

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
	errorLockViolation      = syscall.Errno(33)
)

var (
	kernel32Lock     = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32Lock.NewProc("LockFileEx")
	procUnlockFileEx = kernel32Lock.NewProc("UnlockFileEx")
)

type platformLockState struct {
	overlapped syscall.Overlapped
}

func tryLockFile(file *os.File, mode LockMode, state *platformLockState) (bool, error) {
	flags := uintptr(lockfileFailImmediately)
	if mode == Exclusive {
		flags |= lockfileExclusiveLock
	}
	result, _, callErr := procLockFileEx.Call(
		file.Fd(),
		flags,
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&state.overlapped)),
	)
	if result != 0 {
		return true, nil
	}
	if errors.Is(callErr, errorLockViolation) {
		return false, nil
	}
	return false, callErr
}

func unlockFile(file *os.File, state *platformLockState) error {
	result, _, callErr := procUnlockFileEx.Call(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&state.overlapped)),
	)
	if result == 0 {
		return callErr
	}
	return nil
}
