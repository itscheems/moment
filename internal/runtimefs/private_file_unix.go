//go:build darwin || linux

package runtimefs

import (
	"fmt"
	"os"
)

func protectPrivateFile(path string) error {
	return os.Chmod(path, 0o600)
}

// VerifyPrivateFilePermissions confirms that a private file has no group or
// other access on POSIX platforms.
func VerifyPrivateFilePermissions(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("private file mode is %04o, want 0600", info.Mode().Perm())
	}
	return nil
}
