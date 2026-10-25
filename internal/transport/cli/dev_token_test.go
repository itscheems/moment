package cli

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevSavedTokenFileRebuildAndDryRun(t *testing.T) {
	application := momentTestApplication()
	root := t.TempDir()
	fixed := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	file := filepath.Join(t.TempDir(), "account.token")
	if err := os.WriteFile(file, []byte(fixed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"dev", "scenario", "timeline-basic", "--data-dir", root, "--account-token-file", file, "--json"}
	dry := execute(application, "", append(args, "--dry-run")...)
	if dry.exit != ExitSuccess || strings.Contains(dry.stdout, fixed) {
		t.Fatalf("dry run failed or exposed credential: exit=%d", dry.exit)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("dry run mutated data: %v", err)
	}
	for i := 0; i < 2; i++ {
		out := execute(application, "", append(args, "--yes", "--no-input")...)
		if out.exit != ExitSuccess {
			t.Fatalf("rebuild failed: exit=%d stderr=%s", out.exit, out.stderr)
		}
		if commandData(t, out)["api_token"] != fixed {
			t.Fatal("rebuild changed the saved credential")
		}
	}
}

func TestDevInvalidTokenFileDoesNotMutateTarget(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(t.TempDir(), "account.token")
	if err := os.WriteFile(file, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := execute(momentTestApplication(), "", "dev", "scenario", "timeline-basic", "--data-dir", root, "--account-token-file", file, "--yes", "--no-input", "--json")
	if out.exit != ExitInvalidInput {
		t.Fatalf("invalid token exit=%d", out.exit)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("invalid credential changed target: %v", err)
	}
}
