package runtimefs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const helperEnvironment = "MOMENT_RUNTIME_LOCK_HELPER"

func TestInProcessSharedExclusiveTimeoutAndCancellation(t *testing.T) {
	locker, err := NewLocker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := locker.Acquire(context.Background(), MaintenanceLock, Shared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Release(); err != nil {
			t.Errorf("release first shared lock: %v", err)
		}
	})
	second, err := locker.Acquire(context.Background(), MaintenanceLock, Shared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Release(); err != nil {
			t.Errorf("release second shared lock: %v", err)
		}
	})

	timeout, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := locker.Acquire(timeout, MaintenanceLock, Exclusive); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive lock should time out behind readers, got %v", err)
	}

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := locker.Acquire(canceled, MaintenanceLock, Exclusive); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock should honor cancellation, got %v", err)
	}
}

func TestAcquireSetEnforcesGlobalOrder(t *testing.T) {
	locker, err := NewLocker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.AcquireSet(context.Background(),
		LockRequest{Name: MediaLock, Mode: Exclusive},
		LockRequest{Name: MaintenanceLock, Mode: Exclusive},
	)
	if !errors.Is(err, ErrLockOrder) {
		t.Fatalf("expected lock order error, got %v", err)
	}

	set, err := locker.AcquireSet(context.Background(),
		LockRequest{Name: ImportLock, Mode: Exclusive},
		LockRequest{Name: ServeLock, Mode: Exclusive},
		LockRequest{Name: MaintenanceLock, Mode: Exclusive},
		LockRequest{Name: MediaLock, Mode: Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestExtendSetKeepsImportOwnershipAndReleasesInReverseOrder(t *testing.T) {
	locker, err := NewLocker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	held, err := locker.AcquireSet(context.Background(), LockRequest{Name: ImportLock, Mode: Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	if err := locker.ExtendSet(context.Background(), held,
		LockRequest{Name: ServeLock, Mode: Exclusive},
		LockRequest{Name: MaintenanceLock, Mode: Exclusive},
		LockRequest{Name: MediaLock, Mode: Exclusive},
	); err != nil {
		t.Fatal(err)
	}
	for _, name := range []LockName{ImportLock, ServeLock, MaintenanceLock, MediaLock} {
		if !held.Holds(name, Exclusive) {
			t.Fatalf("extended set does not hold %s", name)
		}
	}
	if err := held.ReleaseLock(ImportLock); err != nil {
		t.Fatalf("release import while retaining server ownership: %v", err)
	}
	for _, name := range []LockName{MediaLock, MaintenanceLock, ServeLock} {
		if err := held.ReleaseLock(name); err != nil {
			t.Fatalf("release %s: %v", name, err)
		}
	}
	if held.Holds(ImportLock, Exclusive) {
		t.Fatal("released import ownership is still reported")
	}
}

func TestExtendSetFailureLeavesOriginalLeaseOwned(t *testing.T) {
	root := t.TempDir()
	locker, err := NewLocker(root)
	if err != nil {
		t.Fatal(err)
	}
	held, err := locker.AcquireSet(context.Background(), LockRequest{Name: ImportLock, Mode: Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	if err := locker.ExtendSet(context.Background(), held, LockRequest{Name: ImportLock, Mode: Shared}); !errors.Is(err, ErrInvalidLock) {
		t.Fatalf("duplicate extension error = %v", err)
	}
	if !held.Holds(ImportLock, Exclusive) {
		t.Fatal("failed extension released the original import lease")
	}
}

func TestCrossProcessSharedExclusiveAndCrashRelease(t *testing.T) {
	root := t.TempDir()
	locker, err := NewLocker(root)
	if err != nil {
		t.Fatal(err)
	}

	sharedHolder := startLockHolder(t, root, MaintenanceLock, Shared)
	localShared, err := locker.Acquire(context.Background(), MaintenanceLock, Shared)
	if err != nil {
		sharedHolder.stop(t, true)
		t.Fatalf("shared ownership must work across processes: %v", err)
	}
	if err := localShared.Release(); err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = locker.Acquire(timeout, MaintenanceLock, Exclusive)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		sharedHolder.stop(t, true)
		t.Fatalf("exclusive ownership should contend with another process reader, got %v", err)
	}
	sharedHolder.stop(t, true)

	exclusiveHolder := startLockHolder(t, root, MaintenanceLock, Exclusive)
	canceled, cancelNow := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		_, acquireErr := locker.Acquire(canceled, MaintenanceLock, Shared)
		waitResult <- acquireErr
	}()
	cancelNow()
	select {
	case err := <-waitResult:
		if !errors.Is(err, context.Canceled) {
			exclusiveHolder.stop(t, false)
			t.Fatalf("cross-process wait did not return cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		exclusiveHolder.stop(t, false)
		t.Fatal("cross-process canceled wait did not return")
	}

	// Kill without giving the helper a chance to unlock. The OS must release
	// ownership even though the .lock file remains on disk.
	exclusiveHolder.stop(t, false)
	afterCrash, afterCrashCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer afterCrashCancel()
	lease, err := locker.Acquire(afterCrash, MaintenanceLock, Exclusive)
	if err != nil {
		t.Fatalf("OS did not release lock after process crash: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(locker.root + string(os.PathSeparator) + "locks" + string(os.PathSeparator) + "maintenance.lock"); err != nil {
		t.Fatalf("persistent lock file disappeared: %v", err)
	}
}

func TestLockOrderIsRejectedAcrossProcessBoundary(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	command.Env = append(os.Environ(),
		helperEnvironment+"=order",
		"MOMENT_RUNTIME_LOCK_ROOT="+t.TempDir(),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("lock-order helper failed: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("order-rejected")) {
		t.Fatalf("child process did not enforce lock order: %s", output)
	}
}

func TestLockHelperProcess(t *testing.T) {
	action := os.Getenv(helperEnvironment)
	if action == "" {
		return
	}
	root := os.Getenv("MOMENT_RUNTIME_LOCK_ROOT")
	locker, err := NewLocker(root)
	if err != nil {
		t.Fatal(err)
	}
	if action == "order" {
		_, err := locker.AcquireSet(context.Background(),
			LockRequest{Name: MediaLock, Mode: Exclusive},
			LockRequest{Name: MaintenanceLock, Mode: Exclusive},
		)
		if !errors.Is(err, ErrLockOrder) {
			t.Fatalf("expected order rejection, got %v", err)
		}
		fmt.Println("order-rejected")
		return
	}

	name := LockName(os.Getenv("MOMENT_RUNTIME_LOCK_NAME"))
	mode := Shared
	if os.Getenv("MOMENT_RUNTIME_LOCK_MODE") == "exclusive" {
		mode = Exclusive
	}
	lease, err := locker.Acquire(context.Background(), name, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Println("locked"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

type lockHolder struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stderr  *bytes.Buffer
}

func startLockHolder(t *testing.T, root string, name LockName, mode LockMode) *lockHolder {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	modeValue := "shared"
	if mode == Exclusive {
		modeValue = "exclusive"
	}
	command.Env = append(os.Environ(),
		helperEnvironment+"=hold",
		"MOMENT_RUNTIME_LOCK_ROOT="+root,
		"MOMENT_RUNTIME_LOCK_NAME="+string(name),
		"MOMENT_RUNTIME_LOCK_MODE="+modeValue,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	ready := make(chan string, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		if readErr != nil {
			ready <- "read helper readiness: " + readErr.Error()
			return
		}
		ready <- strings.TrimSpace(line)
	}()
	select {
	case message := <-ready:
		if message != "locked" {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("lock helper did not become ready: %s; stderr=%s", message, stderr)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("timed out waiting for lock helper; stderr=%s", stderr)
	}
	return &lockHolder{command: command, stdin: stdin, stderr: stderr}
}

func (holder *lockHolder) stop(t *testing.T, clean bool) {
	t.Helper()
	if clean {
		if err := holder.stdin.Close(); err != nil {
			t.Errorf("close helper stdin: %v", err)
		}
		if err := holder.command.Wait(); err != nil {
			t.Errorf("wait for lock helper: %v; stderr=%s", err, holder.stderr)
		}
		return
	}
	if err := holder.command.Process.Kill(); err != nil {
		t.Errorf("kill lock helper: %v", err)
	}
	_ = holder.stdin.Close()
	if err := holder.command.Wait(); err == nil {
		t.Error("crashed lock helper exited successfully")
	}
}
