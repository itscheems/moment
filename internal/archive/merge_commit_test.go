package archive

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestMergeCommitKeepsBothThroughBackupAndAtomicGenerationSwitch(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `UPDATE entries SET content_md = 'local edit', updated_at_ms = updated_at_ms + 1 WHERE id = ?`, entryID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.runtime.Root, "secrets.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	committer, err := NewCommitter(CommitOptions{
		DataDir: fixture.runtime.Root, AppVersion: "0.1.0", Clock: func() time.Time { return now },
		AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := committer.InspectMergeFileOffline(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	selection := inspection.Plan.DefaultSelection
	found := false
	for index := range selection.Items {
		if selection.Items[index].Kind == MergeEntry && selection.Items[index].ID == entryID {
			selection.Items[index].Action = MergeKeepBoth
			found = true
		}
	}
	if !found {
		t.Fatalf("entry conflict missing from plan: %#v", inspection.Plan)
	}
	result, err := committer.CommitMerge(context.Background(), inspection.StagingToken, 1, selection, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Phase != CommitSucceeded || !result.Status.BackupVerified {
		t.Fatalf("result = %#v", result)
	}
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	authority, err := ReadPortableAuthority(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if len(authority.Entries) != 2 {
		t.Fatalf("entries = %#v", authority.Entries)
	}
	contents := map[string]bool{}
	for _, entry := range authority.Entries {
		contents[entry.ContentMD] = true
	}
	if !contents["local edit"] || !contents["Unicode 世界"] {
		t.Fatalf("merged contents = %#v", contents)
	}
}
