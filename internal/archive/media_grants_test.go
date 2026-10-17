package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"path/filepath"
	"testing"
)

func TestPortableRestoreKeepsAllAccountsDuplicateUploadRights(t *testing.T) {
	source := newExportFixture(t, true)
	insertAccountsFixture(t, source)
	ctx := context.Background()
	repo, _ := sqlitestore.NewAccountRepository(source.database, source.runtime.Locks)
	accounts, _ := app.NewAccountService(repo)
	first, err := accounts.Account(ctx, fixtureAccountID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.Account(ctx, memberAccountID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := media.NewStore(source.runtime, source.database, media.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []domain.User{first, second} {
		value, _, err := store.Ingest(app.WithContentActor(ctx, user), "uploaded.jpg", bytes.NewReader(source.mediaOneBytes))
		if err != nil || value.ID.String() != mediaOneID {
			t.Fatalf("duplicate source upload=%+v,%v", value, err)
		}
	}
	target := newExportFixture(t, false)
	restored := stageArchive(t, target, exportFixtureFile(t, source))
	if err := restored.ReadTx(ctx, func(q sqlitestore.Queries) error {
		for _, user := range []domain.User{first, second} {
			var present bool
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_grants WHERE user_id=? AND media_id=?)`, user.ID, mediaOneID).Scan(&present); err != nil {
				return err
			}
			if !present {
				t.Fatalf("restore lost uploader rights for %s", user.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPortableMediaGrantsRejectUnknownAccounts(t *testing.T) {
	fixture := newExportFixture(t, true)
	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	var rebuilt []byte
	for _, line := range nonemptyLines(members["data/media.jsonl"]) {
		var value mediaRecord
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		value.Grants = []mediaGrantRecord{{UserID: "ffffffffffffffffffffffffffffffff", CreatedAt: portableTime(1)}}
		rebuilt = append(rebuilt, mustMarshalTest(t, value)...)
		rebuilt = append(rebuilt, '\n')
	}
	members["data/media.jsonl"] = rebuilt
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	path := filepath.Join(t.TempDir(), "unknown-grant.zip")
	writeArchive(t, path, members, order)
	if err := ValidateFile(path); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("unknown reuse account accepted=%v", err)
	}
}

func TestMergeUnionsDuplicateUploadRightsAfterMediaRemapping(t *testing.T) {
	const first = "0123456789abcdef0123456789abcdef"
	const second = "fedcba9876543210fedcba9876543210"
	resolved := PortableAuthority{Users: []sqlitestore.ArchiveUser{{ID: first}, {ID: second}}, Media: []sqlitestore.ArchiveMedia{{ID: "local-media"}}}
	local := PortableAuthority{Media: []sqlitestore.ArchiveMedia{{ID: "local-media", Grants: []sqlitestore.ArchiveMediaGrant{{UserID: first, CreatedAtMS: 10}}}}}
	incoming := PortableAuthority{Media: []sqlitestore.ArchiveMedia{{ID: "other-media", Grants: []sqlitestore.ArchiveMediaGrant{{UserID: first, CreatedAtMS: 20}, {UserID: second, CreatedAtMS: 30}}}}}
	mergeMediaPermissions(&resolved, local, incoming, map[string]string{"other-media": "local-media"})
	grants := resolved.Media[0].Grants
	if len(grants) != 2 || grants[0].UserID != first || grants[0].CreatedAtMS != 10 || grants[1].UserID != second {
		t.Fatalf("merge lost account rights=%+v", grants)
	}
}
