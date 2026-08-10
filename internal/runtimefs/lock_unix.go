//go:build darwin || linux

package runtimefs

import (
	"errors"
	"os"
	"syscall"
)

type platformLockState struct{}

func tryLockFile(file *os.File, mode LockMode, _ *platformLockState) (bool, error) {
	operation := syscall.LOCK_NB
	if mode == Shared {
		operation |= syscall.LOCK_SH
	} else {
		operation |= syscall.LOCK_EX
	}
	err := syscall.Flock(int(file.Fd()), operation)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(file *os.File, _ *platformLockState) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
