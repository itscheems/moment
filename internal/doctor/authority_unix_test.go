//go:build darwin || linux

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDoctorRejectsAuthorityFIFOWithoutBlocking(t *testing.T) {
	for _, name := range []string{"CURRENT", "instance.json"} {
		t.Run(name, func(t *testing.T) {
			fixture := newDoctorFixture(t)
			path := filepath.Join(fixture.root, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			service, err := New(Options{DataDir: fixture.root})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			started := time.Now()
			report, err := service.Run(ctx, ModeDeep)
			if err != nil {
				t.Fatal(err)
			}
			if time.Since(started) > 500*time.Millisecond {
				t.Fatal("doctor blocked while rejecting an authority FIFO")
			}
			if check := findCheck(t, report, CheckCurrentGeneration); check.Status != StatusFail || report.Failure() != FailureIntegrity {
				t.Fatalf("check=%#v failure=%s", check, report.Failure())
			}
		})
	}
}
