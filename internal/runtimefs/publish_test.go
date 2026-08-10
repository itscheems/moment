package runtimefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishPreparedFileOrdersReplaceBeforeDurabilityBarrier(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, ".partial-source")
	destination := filepath.Join(directory, "ready")
	if err := os.WriteFile(source, []byte("complete"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalReplace := publishPreparedReplace
	originalSync := publishPreparedSyncDirectory
	t.Cleanup(func() {
		publishPreparedReplace = originalReplace
		publishPreparedSyncDirectory = originalSync
	})
	var events []string
	publishPreparedReplace = func(from, to string) error {
		events = append(events, "replace")
		return os.Rename(from, to)
	}
	publishPreparedSyncDirectory = func(path string) error {
		events = append(events, "sync")
		if path != directory {
			t.Fatalf("sync directory = %q, want %q", path, directory)
		}
		return nil
	}
	if err := PublishPreparedFile(source, destination); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != "replace" || events[1] != "sync" {
		t.Fatalf("publication events = %v, want [replace sync]", events)
	}
}

func TestPublishPreparedFilePropagatesDurabilityFailure(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, ".partial-source")
	destination := filepath.Join(directory, "ready")
	if err := os.WriteFile(source, []byte("complete"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalReplace := publishPreparedReplace
	originalSync := publishPreparedSyncDirectory
	t.Cleanup(func() {
		publishPreparedReplace = originalReplace
		publishPreparedSyncDirectory = originalSync
	})
	injected := errors.New("injected metadata barrier failure")
	publishPreparedReplace = os.Rename
	publishPreparedSyncDirectory = func(string) error { return injected }
	if err := PublishPreparedFile(source, destination); !errors.Is(err, injected) {
		t.Fatalf("publication error = %v, want injected durability failure", err)
	}
}

func TestPublishPreparedFileRejectsCrossDirectorySource(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "source")
	destinationDirectory := filepath.Join(root, "destination")
	if err := os.MkdirAll(sourceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDirectory, "partial")
	if err := os.WriteFile(source, []byte("complete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishPreparedFile(source, filepath.Join(destinationDirectory, "ready")); err == nil {
		t.Fatal("cross-directory prepared file was accepted")
	}
}
