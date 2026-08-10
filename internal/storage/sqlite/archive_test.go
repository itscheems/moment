package sqlite

import (
	"context"
	"testing"

	"github.com/moment/moment/internal/runtimefs"
)

func TestArchiveSnapshotRejectsCrossRecordCorruption(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*testing.T, *Database)
	}{
		{
			name: "missing media reference",
			corrupt: func(t *testing.T, database *Database) {
				t.Helper()
				if _, err := database.write.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
					t.Fatal(err)
				}
				if _, err := database.write.Exec(`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, archiveEntryID, archiveMissingMediaID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "reply targets another reply",
			corrupt: func(t *testing.T, database *Database) {
				t.Helper()
				if _, err := database.write.Exec(`DROP TRIGGER comments_parent_insert_guard`); err != nil {
					t.Fatal(err)
				}
				if _, err := database.write.Exec(`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'owner', 'Owner', 'nested', 3)`, archiveNestedID, archiveEntryID, archiveReplyID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			runtimeState, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			database, err := Open(ctx, runtimeState, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = database.Close() }()
			if _, err := database.write.Exec(`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms) VALUES (?, 'moment', 'entry', 1, 1, 1)`, archiveEntryID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.write.Exec(`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'guest', 'Guest', 'root', 1)`, archiveRootID, archiveEntryID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.write.Exec(`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'owner', 'Owner', 'reply', 2)`, archiveReplyID, archiveEntryID, archiveRootID); err != nil {
				t.Fatal(err)
			}
			test.corrupt(t, database)
			snapshot, err := database.BeginArchiveSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = snapshot.Close() }()
			if err := snapshot.ValidateIntegrity(ctx); err == nil {
				t.Fatal("corrupt cross-record graph passed archive validation")
			}
		})
	}
}

const (
	archiveEntryID        = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	archiveRootID         = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	archiveReplyID        = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	archiveNestedID       = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	archiveMissingMediaID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
)
