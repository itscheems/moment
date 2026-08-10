package archive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

func TestDiagnosePersistentStateClassifiesBoundedRedactedStaging(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		state    string
		expires  time.Time
		complete bool
	}{
		{name: "import-ready", state: stagingReady, expires: now.Add(time.Hour), complete: true},
		{name: "import-expired", state: stagingReady, expires: now.Add(-time.Second), complete: true},
		{name: "import-committing", state: stagingCommitting, expires: now.Add(time.Hour), complete: false},
		{name: "import-used", state: stagingUsed, expires: now.Add(time.Hour)},
		{name: "import-failed", state: stagingFailed, expires: now.Add(time.Hour)},
	}
	for _, test := range tests {
		stageRoot := filepath.Join(runtimeState.Root, "staging", "import", test.name)
		if err := os.Mkdir(stageRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if test.complete {
			for _, path := range []string{"archive.zip", filepath.Join("generation", "moment.db")} {
				full := filepath.Join(stageRoot, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(stageRoot, "generation", "media", "original"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeStagingMetadata(stageRoot, stagingMetadata{
			TokenHash: strings.Repeat("a", 64), ArchiveSHA: strings.Repeat("b", 64), InstanceID: runtimeState.Instance.ID,
			SessionEpoch: 1, GenerationID: test.name, ExpiresAtMS: test.expires.UnixMilli(), State: test.state,
		}); err != nil {
			t.Fatal(err)
		}
	}
	invalidRoot := filepath.Join(runtimeState.Root, "staging", "import", "import-invalid")
	if err := os.Mkdir(invalidRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeStagingMetadata(invalidRoot, stagingMetadata{
		TokenHash: "CANARY_TOKEN_HASH", ArchiveSHA: strings.Repeat("b", 64), InstanceID: runtimeState.Instance.ID,
		SessionEpoch: 1, GenerationID: "different-generation", ExpiresAtMS: now.Add(time.Hour).UnixMilli(), State: stagingReady,
	}); err != nil {
		t.Fatal(err)
	}

	staging, recovery, err := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 32)
	if err != nil {
		t.Fatal(err)
	}
	if staging.Total != 6 || staging.Ready != 1 || staging.Expired != 1 || staging.Committing != 1 || staging.Used != 1 || staging.Failed != 1 || staging.Invalid != 1 || staging.Incomplete != 1 || staging.Bounded {
		t.Fatalf("staging = %#v", staging)
	}
	if recovery.Journals != 0 || recovery.Invalid != 0 {
		t.Fatalf("recovery = %#v", recovery)
	}
	encoded, err := json.Marshal(struct {
		Staging  StagingDiagnostic
		Recovery RecoveryDiagnostic
	}{staging, recovery})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "CANARY") || strings.Contains(string(encoded), strings.Repeat("a", 64)) || strings.Contains(string(encoded), runtimeState.Root) {
		t.Fatalf("diagnostic leaked private staging identity: %s", encoded)
	}

	bounded, _, err := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 2)
	if err != nil || !bounded.Bounded || bounded.Total != 2 {
		t.Fatalf("bounded = %#v, %v", bounded, err)
	}
}
