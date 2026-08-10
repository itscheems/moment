package runtimefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorityReadersRejectOversizedNonregularAndDuplicateMetadata(t *testing.T) {
	t.Run("oversized instance", func(t *testing.T) {
		runtimeState, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(runtimeState.Root, instanceFile)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", int(maxInstanceMetadataBytes)+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadInstance(runtimeState.Root); err == nil {
			t.Fatal("oversized instance metadata was accepted")
		}
	})

	t.Run("oversized current", func(t *testing.T) {
		runtimeState, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(runtimeState.Root, currentFile)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", int(maxCurrentPointerBytes)+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := runtimeState.ResolveCurrent(); !errors.Is(err, ErrInvalidCurrent) {
			t.Fatalf("oversized CURRENT error = %v", err)
		}
	})

	t.Run("duplicate instance field", func(t *testing.T) {
		runtimeState, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(runtimeState.Root, instanceFile)
		data := `{"instance_id":"` + runtimeState.Instance.ID + `","instance_id":"` + runtimeState.Instance.ID + `","environment":"production","layout_version":1,"initialized_at":"2026-08-11T00:00:00Z"}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadInstance(runtimeState.Root); !errors.Is(err, ErrInvalidInstance) {
			t.Fatalf("duplicate instance field error = %v", err)
		}
	})

	for _, name := range []string{instanceFile, currentFile} {
		name := name
		t.Run(name+" symlink", func(t *testing.T) {
			runtimeState, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			secretPath := filepath.Join(runtimeState.Root, "secrets-canary")
			if err := os.WriteFile(secretPath, []byte("CANARY_SECRET_CONTENT"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(runtimeState.Root, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secretPath, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if name == instanceFile {
				if _, err := ReadInstance(runtimeState.Root); err == nil || strings.Contains(err.Error(), "CANARY") {
					t.Fatalf("unsafe instance error = %v", err)
				}
			} else if _, err := runtimeState.ResolveCurrent(); err == nil || strings.Contains(err.Error(), "CANARY") {
				t.Fatalf("unsafe CURRENT error = %v", err)
			}
		})
	}
}
