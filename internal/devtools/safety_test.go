package devtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

func TestOpenTargetAcceptsExactMarkedDevelopmentRoot(t *testing.T) {
	runtimeState := developmentRuntime(t)
	target, err := OpenTarget(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	if target.Root != runtimeState.Root || target.Instance.ID != runtimeState.Instance.ID {
		t.Fatalf("target identity drifted: %+v", target)
	}
	if err := target.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTargetRejectsUnmarkedProductionAndSymlinkedRoots(t *testing.T) {
	if _, err := OpenTarget(canonicalTempDir(t)); !domain.HasCode(err, domain.CodeDataIntegrityFailed) {
		t.Fatalf("unmarked root error = %v, want %s", err, domain.CodeDataIntegrityFailed)
	}
	production, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: canonicalTempDir(t), Environment: runtimefs.EnvironmentProduction})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenTarget(production.Root); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("production root error = %v, want %s", err, domain.CodeInvalidArgument)
	}
	development := developmentRuntime(t)
	link := filepath.Join(canonicalTempDir(t), "linked-development")
	if err := os.Symlink(development.Root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := OpenTarget(link); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("symlink root error = %v, want %s", err, domain.CodeInvalidArgument)
	}
}

func TestOpenTargetRejectsControlledSymlink(t *testing.T) {
	runtimeState := developmentRuntime(t)
	outside := canonicalTempDir(t)
	cache := filepath.Join(runtimeState.Root, "cache")
	if err := os.Rename(cache, cache+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, cache); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := OpenTarget(runtimeState.Root); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("controlled symlink error = %v, want %s", err, domain.CodeInvalidArgument)
	}
}

func TestTargetVerifyRejectsAuthorityAndGenerationReplacement(t *testing.T) {
	tests := []struct {
		name string
		swap func(*testing.T, *runtimefs.Runtime)
	}{
		{
			name: "marker",
			swap: func(t *testing.T, runtimeState *runtimefs.Runtime) {
				path := filepath.Join(runtimeState.Root, "instance.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				replaceFileForTest(t, path, data, 0o600)
			},
		},
		{
			name: "current",
			swap: func(t *testing.T, runtimeState *runtimefs.Runtime) {
				path := filepath.Join(runtimeState.Root, "CURRENT")
				replaceFileForTest(t, path, []byte(runtimeState.Current.ID+"\n"), 0o600)
			},
		},
		{
			name: "generation",
			swap: func(t *testing.T, runtimeState *runtimefs.Runtime) {
				old := runtimeState.Current.Root + "-old"
				if err := os.Rename(runtimeState.Current.Root, old); err != nil {
					t.Fatal(err)
				}
				if err := copyGenerationSkeleton(runtimeState.Current, runtimeState.Current.Root); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtimeState := developmentRuntime(t)
			target, err := OpenTarget(runtimeState.Root)
			if err != nil {
				t.Fatal(err)
			}
			test.swap(t, runtimeState)
			if err := target.Verify(); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("Verify error = %v, want %s", err, domain.CodeInvalidArgument)
			}
		})
	}
}

func TestAcquireOperationLocksUsesServeOwnershipAndCallerContext(t *testing.T) {
	runtimeState := developmentRuntime(t)
	serve, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireOperationLocks(context.Background(), runtimeState); !domain.HasCode(err, domain.CodeInstanceInUse) {
		t.Fatalf("serve ownership error = %v, want %s", err, domain.CodeInstanceInUse)
	}
	if err := serve.Release(); err != nil {
		t.Fatal(err)
	}

	importLease, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ImportLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := acquireOperationLocks(ctx, runtimeState); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked import error = %v, want deadline", err)
	}
	if err := importLease.Release(); err != nil {
		t.Fatal(err)
	}
}

func developmentRuntime(t *testing.T) *runtimefs.Runtime {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{
		DataDir: canonicalTempDir(t), Environment: runtimefs.EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtimeState
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func replaceFileForTest(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	temporary := path + ".replacement"
	if err := os.WriteFile(temporary, data, mode); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func copyGenerationSkeleton(source runtimefs.Generation, destination string) error {
	if err := os.MkdirAll(filepath.Join(destination, "media", "original"), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(source.Root+"-old", "moment.db"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "moment.db"), data, 0o600)
}
