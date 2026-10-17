package archive

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestEditedAtSurvivesPortableImport(t *testing.T) {
	fixture := newExportFixture(t, true)
	ctx := context.Background()
	var edited int64
	if err := fixture.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		if _, err := q.ExecContext(ctx, `UPDATE entries SET updated_at_ms = updated_at_ms + 1, edited_at_ms = updated_at_ms + 1 WHERE id = ?`, entryID); err != nil {
			return err
		}
		return q.QueryRowContext(ctx, `SELECT edited_at_ms FROM entries WHERE id = ?`, entryID).Scan(&edited)
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(ctx, exportFixtureFile(t, fixture), testAuthorization(7))
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
	found := false
	if err := snapshot.Entries(ctx, func(entry sqlitestore.ArchiveEntry) error {
		if entry.ID == entryID {
			found = entry.EditedAtMS != nil && *entry.EditedAtMS == edited
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("import lost authoritative edit timestamp")
	}
}
