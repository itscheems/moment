//go:build darwin || linux

package runtimefs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBoundedAuthorityReadRejectsSwapAndFIFOWithoutBlocking(t *testing.T) {
	t.Run("regular identity swap", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "authority")
		replacement := filepath.Join(root, "replacement")
		if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacement, []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readBoundedAuthorityFileAfterLstat(path, 32, func() {
			if renameErr := os.Rename(replacement, path); renameErr != nil {
				t.Fatal(renameErr)
			}
		}); err == nil {
			t.Fatal("authority identity swap was accepted")
		}
	})

	t.Run("fifo before lstat", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "authority")
		if err := unix.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readBoundedAuthorityFile(path, 32); err == nil {
			t.Fatal("authority FIFO was accepted")
		}
	})

	t.Run("fifo swap", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "authority")
		fifo := filepath.Join(root, "fifo")
		if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readBoundedAuthorityFileAfterLstat(path, 32, func() {
			if renameErr := os.Rename(fifo, path); renameErr != nil {
				t.Fatal(renameErr)
			}
		}); err == nil {
			t.Fatal("authority FIFO swap was accepted")
		}
	})
}
