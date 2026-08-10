package runtimefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeAndRestartResolveOneGeneration(t *testing.T) {
	root := t.TempDir()
	runtime, err := Initialize(context.Background(), Config{
		DataDir:     root,
		Environment: EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.IsDevelopment() {
		t.Fatal("development instance marker was not preserved")
	}
	if runtime.Instance.ID == "" || runtime.Current.ID == "" {
		t.Fatalf("missing runtime identity: %+v", runtime)
	}
	assertGenerationPaths(t, runtime.Current)

	restarted, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Instance.ID != runtime.Instance.ID {
		t.Fatalf("instance changed across restart: %s != %s", restarted.Instance.ID, runtime.Instance.ID)
	}
	if restarted.Current.ID != runtime.Current.ID {
		t.Fatalf("generation changed across restart: %s != %s", restarted.Current.ID, runtime.Current.ID)
	}
	if restarted.Current.DatabasePath != filepath.Join(restarted.Current.Root, "moment.db") ||
		restarted.Current.OriginalsDir != filepath.Join(restarted.Current.Root, "media", "original") {
		t.Fatalf("database and media did not resolve from one generation: %+v", restarted.Current)
	}

	for _, path := range []string{
		filepath.Join(root, "cache", "previews"),
		filepath.Join(root, "backups"),
		filepath.Join(root, "staging", "import"),
		filepath.Join(root, "tmp"),
		filepath.Join(root, "locks"),
	} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("required runtime directory %s is not ready: %v", path, err)
		}
	}
}

func TestInitializeRejectsEnvironmentMismatch(t *testing.T) {
	root := t.TempDir()
	if _, err := Initialize(context.Background(), Config{DataDir: root}); err != nil {
		t.Fatal(err)
	}
	_, err := Initialize(context.Background(), Config{DataDir: root, Environment: EnvironmentDevelopment})
	if !errors.Is(err, ErrEnvironmentMismatch) {
		t.Fatalf("expected environment mismatch, got %v", err)
	}
}

func TestValidCurrentWinsOverStaleNext(t *testing.T) {
	runtime, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	other := createGenerationForTest(t, runtime, "newer-generation")
	nextPath := filepath.Join(runtime.Root, currentFile+".next")
	if err := os.WriteFile(nextPath, []byte(other.ID), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Current.ID != runtime.Current.ID {
		t.Fatalf("stale next pointer was promoted: got %s want %s", restarted.Current.ID, runtime.Current.ID)
	}
	if _, err := os.Stat(nextPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale next pointer was not removed: %v", err)
	}
}

func TestOpenRemovesOnlyAbandonedGenerationStagingDirectories(t *testing.T) {
	runtime, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	validExtra := createGenerationForTest(t, runtime, "valid-extra")
	partial := filepath.Join(runtime.Root, "generations", ".partial-crashed-create")
	if err := os.MkdirAll(filepath.Join(partial, "media", "original"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "moment.db"), []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned generation staging directory remains: %v", err)
	}
	if restarted.Current.ID != runtime.Current.ID {
		t.Fatalf("cleanup changed current generation: got %s want %s", restarted.Current.ID, runtime.Current.ID)
	}
	assertGenerationPaths(t, restarted.Current)
	assertGenerationPaths(t, validExtra)
}

func TestInitializeResumeRemovesAbandonedGenerationStagingDirectories(t *testing.T) {
	root := t.TempDir()
	runtime, err := Initialize(context.Background(), Config{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, currentFile)); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(root, "generations", ".partial-interrupted-initialize")
	if err := os.MkdirAll(partial, 0o700); err != nil {
		t.Fatal(err)
	}

	resumed, err := Initialize(context.Background(), Config{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Instance.ID != runtime.Instance.ID {
		t.Fatalf("initialization resume replaced instance identity: %s != %s", resumed.Instance.ID, runtime.Instance.ID)
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted initialization staging directory remains: %v", err)
	}
	assertGenerationPaths(t, resumed.Current)
}

func TestInvalidCurrentFailsWithoutGuessingRecovery(t *testing.T) {
	runtime, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	validAlternative := createGenerationForTest(t, runtime, "valid-alternative")
	if err := os.WriteFile(filepath.Join(runtime.Root, currentFile), []byte("missing-generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	nextPath := filepath.Join(runtime.Root, currentFile+".next")
	if err := os.WriteFile(nextPath, []byte(validAlternative.ID), 0o600); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(runtime.Root, "generations", ".partial-recovery-evidence")
	if err := os.MkdirAll(partial, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err = Open(runtime.Root)
	if !errors.Is(err, ErrInvalidCurrent) {
		t.Fatalf("expected invalid CURRENT readiness failure, got %v", err)
	}
	data, readErr := os.ReadFile(nextPath)
	if readErr != nil {
		t.Fatalf("invalid CURRENT must leave recovery evidence: %v", readErr)
	}
	if string(data) != validAlternative.ID {
		t.Fatalf("CURRENT.next changed while CURRENT was invalid: %q", data)
	}
	if info, statErr := os.Stat(partial); statErr != nil || !info.IsDir() {
		t.Fatalf("invalid CURRENT must preserve partial recovery evidence: %v", statErr)
	}
}

func TestSwitchFailureInjectionNeverSplitsDatabaseAndMedia(t *testing.T) {
	runtime, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	original := runtime.Current
	next := createGenerationForTest(t, runtime, "replacement")
	injected := errors.New("injected switch failure")

	err = runtime.switchCurrent(context.Background(), next.ID, switchHooks{
		beforeReplace: func() error { return injected },
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected pre-replace failure, got %v", err)
	}
	current, err := runtime.ResolveCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != original.ID {
		t.Fatalf("pre-replace failure changed CURRENT: got %s want %s", current.ID, original.ID)
	}
	assertGenerationPaths(t, current)

	// A valid CURRENT makes the stale next file disposable on restart.
	restarted, err := Open(runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	err = restarted.switchCurrent(context.Background(), next.ID, switchHooks{
		afterReplace: func() error { return injected },
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected post-replace failure, got %v", err)
	}
	current, err = restarted.ResolveCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != next.ID {
		t.Fatalf("post-replace CURRENT does not select the complete new generation: %s", current.ID)
	}
	assertGenerationPaths(t, current)
}

func TestSwitchCurrentRequiresCoordinatedExclusiveLocks(t *testing.T) {
	runtime, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CreateGeneration(context.Background(), "unguarded", nil); !errors.Is(err, ErrInsufficientLocks) {
		t.Fatalf("unguarded generation creation was accepted: %v", err)
	}
	next := createGenerationForTest(t, runtime, "coordinated-replacement")
	if err := runtime.SwitchCurrent(context.Background(), next.ID, nil); !errors.Is(err, ErrInsufficientLocks) {
		t.Fatalf("unguarded generation switch was accepted: %v", err)
	}

	held, err := runtime.Locks.AcquireSet(context.Background(),
		LockRequest{Name: ImportLock, Mode: Exclusive},
		LockRequest{Name: MaintenanceLock, Mode: Exclusive},
		LockRequest{Name: MediaLock, Mode: Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SwitchCurrent(context.Background(), next.ID, held); err != nil {
		t.Fatal(err)
	}
	if runtime.Current.ID != next.ID {
		t.Fatalf("guarded switch did not update runtime: %s", runtime.Current.ID)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SwitchCurrent(context.Background(), next.ID, held); !errors.Is(err, ErrInsufficientLocks) {
		t.Fatalf("released lock set still authorized a switch: %v", err)
	}
}

func TestCurrentRejectsExtraPointerContent(t *testing.T) {
	runtime, err := Initialize(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtime.Root, currentFile)
	if err := os.WriteFile(path, []byte(runtime.Current.ID+"\nother"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ResolveCurrent(); !errors.Is(err, ErrInvalidCurrent) {
		t.Fatalf("expected malformed pointer failure, got %v", err)
	}
}

func assertGenerationPaths(t *testing.T, generation Generation) {
	t.Helper()
	if err := validateGeneration(generation); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{generation.DatabasePath, generation.MediaRoot, generation.OriginalsDir} {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(generation.Root, absolute)
		if err != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("generation path escaped root: %s (%v)", path, err)
		}
	}
}

func createGenerationForTest(t *testing.T, runtime *Runtime, id string) Generation {
	t.Helper()
	held, err := runtime.Locks.AcquireSet(context.Background(), LockRequest{Name: ImportLock, Mode: Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	generation, err := runtime.CreateGeneration(context.Background(), id, held)
	if releaseErr := held.Release(); releaseErr != nil {
		t.Fatalf("release generation creation lock: %v", releaseErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return generation
}
