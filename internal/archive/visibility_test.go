package archive

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// A hidden post travels through the archive as a hidden post; a public one
// travels exactly as it did before visibility existed, so an archive of a
// site that never hid anything is what earlier versions wrote.
func TestVisibilitySurvivesPortableImportAndDefaultStaysAbsent(t *testing.T) {
	fixture := newExportFixture(t, true)
	ctx := context.Background()
	untouched, _ := readArchive(t, exportFixtureBytes(t, fixture))
	if records := string(untouched["data/entries.jsonl"]); !strings.Contains(records, `"id":"`+entryID+`"`) || strings.Contains(records, `"visibility"`) {
		t.Fatalf("a site with no hidden posts names a visibility: %s", records)
	}
	if err := fixture.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(ctx, `UPDATE entries SET visibility = 'only_me' WHERE id = ?`, entryID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	exported := exportFixtureBytes(t, fixture)
	members, _ := readArchive(t, exported)
	var hiddenLine string
	for _, line := range strings.Split(strings.TrimSpace(string(members["data/entries.jsonl"])), "\n") {
		if strings.Contains(line, `"id":"`+entryID+`"`) {
			hiddenLine = line
		}
	}
	if !strings.Contains(hiddenLine, `"visibility":"only_me"`) {
		t.Fatalf("hidden entry record = %s", hiddenLine)
	}

	now := time.Now()
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(ctx, writeArchiveBytes(t, exported), testAuthorization(7))
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
	runtimeState := &runtimefs.Runtime{Root: fixture.runtime.Root, Instance: fixture.runtime.Instance, Current: stagingGeneration(stage, inspection.GenerationID), Locks: fixture.runtime.Locks}
	db, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	snapshot, err := db.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	seen := map[string]domain.Visibility{}
	if err := snapshot.Entries(ctx, func(entry sqlitestore.ArchiveEntry) error {
		seen[entry.ID] = entry.Visibility
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen[entryID] != domain.VisibilityOnlyMe {
		t.Fatalf("import lost the audience: %v", seen)
	}
	for id, visibility := range seen {
		if id != entryID && visibility != domain.VisibilityEveryone {
			t.Fatalf("import changed %s to %q", id, visibility)
		}
	}
}

// A value this build does not know is refused at inspection, before anything
// is staged: a post it cannot promise to hide is not imported as public.
func TestUnknownArchiveVisibilityIsRefused(t *testing.T) {
	fixture := newExportFixture(t, true)
	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	entries := string(members["data/entries.jsonl"])
	tampered := strings.Replace(entries, `"id":"`+entryID+`"`, `"id":"`+entryID+`","visibility":"friends"`, 1)
	if tampered == entries {
		t.Fatal("tamper target missing")
	}
	members["data/entries.jsonl"] = []byte(tampered)
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "unknown-visibility.zip")
	writeArchive(t, name, members, order)
	now := time.Now()
	_, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), name, testAuthorization(1))
	if !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("unknown visibility inspected as %v", err)
	}
	assertNoReadyStaging(t, fixture.runtime.Root)
}
