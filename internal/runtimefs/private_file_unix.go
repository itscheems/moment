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
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private file is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("private file mode is %04o, want 0600", info.Mode().Perm())
	}
	return nil
}

// VerifyWritablePrivateFilePermissions applies the runtime's authoritative
// writable-file policy. It is distinct from a write probe: diagnostics can
// verify the policy without changing the file.
func VerifyWritablePrivateFilePermissions(path string) error {
	return VerifyPrivateFilePermissions(path)
}

// VerifyPrivateDirectoryPermissions confirms that an authoritative runtime
// directory is owner-readable, owner-writable, and owner-traversable only.
func VerifyPrivateDirectoryPermissions(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory is not a directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory mode is %04o, want 0700", info.Mode().Perm())
	}
	return nil
}
