package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestPersonalImportAppendsFullConversationAndArticlesWithoutReplacingExistingData(t *testing.T) {
	ctx := context.Background()
	source := newExportFixture(t, true)
	insertAccountsFixture(t, source)
	seedArticleFixture(t, source)
	if err := source.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(ctx, `UPDATE users SET avatar_media_id=? WHERE id=?`, mediaTwoID, memberAccountID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := source.exporter.ExportAccount(ctx, &output, fixtureAccountID); err != nil {
		t.Fatal(err)
	}
	target := newExportFixture(t, true)
	insertAccountsFixture(t, target)
	counts := func() sqlitestore.ArchiveCounts {
		t.Helper()
		snap, err := target.database.BeginArchiveSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = snap.Close() }()
		return snap.Counts()
	}
	before := counts()
	plan, err := target.exporter.ImportAccount(ctx, bytes.NewReader(output.Bytes()), memberAccountID, AccountImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := counts(); got != before {
		t.Fatalf("preview changed active data: %+v -> %+v", before, got)
	}
	if plan.Counts.Entries == 0 || plan.Counts.Comments == 0 || plan.Counts.Articles != 1 || plan.Counts.ArticleDrafts != 1 {
		t.Fatalf("incomplete personal preview: %+v", plan)
	}
	applied, err := target.exporter.ImportAccount(ctx, bytes.NewReader(output.Bytes()), memberAccountID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256})
	if err != nil {
		t.Fatal(err)
	}
	after := counts()
	if after.Entries != before.Entries+applied.Added.Entries || after.Comments != before.Comments+applied.Added.Comments || after.Users != before.Users || after.Identities != before.Identities || after.Articles != before.Articles+1 || after.ArticleDrafts != before.ArticleDrafts+1 {
		t.Fatalf("personal import replaced data: before=%+v result=%+v after=%+v", before, applied, after)
	}
	repeated, err := target.exporter.ImportAccount(ctx, bytes.NewReader(output.Bytes()), memberAccountID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Added.Entries != 0 || repeated.Added.Comments != 0 || repeated.Added.ArticleDrafts != 0 || counts() != after {
		t.Fatalf("repeat duplicated data: %+v", repeated)
	}
	snap, err := target.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Close() }()
	importedComments := 0
	snapshots := 0
	if err := snap.Comments(ctx, func(v sqlitestore.ArchiveComment) error {
		if v.ID != rootCommentID && v.ID != replyID {
			importedComments++
			if v.AuthorSnapshot != nil {
				snapshots++
				if v.AuthorSnapshot.Username == "" || v.AuthorSnapshot.AvatarMediaID == nil {
					t.Fatalf("lost author display data: %+v", v.AuthorSnapshot)
				}
			}
			if v.AuthorUserID != nil && *v.AuthorUserID != memberAccountID {
				t.Fatalf("restored an external account identity: %+v", v)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if importedComments == 0 || snapshots == 0 {
		t.Fatal("conversation was omitted")
	}
	// The imported graph must remain portable through a full-site backup too.
	var backup bytes.Buffer
	if _, err := target.exporter.Export(ctx, &backup); err != nil {
		t.Fatal(err)
	}
	backupPath := writeArchiveBytes(t, backup.Bytes())
	if err := ValidateFileContext(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	restored := stageArchive(t, target, backupPath)
	restoredSnapshot := stagedSnapshot(t, restored)
	restoredProfiles := 0
	if err := restoredSnapshot.Comments(ctx, func(v sqlitestore.ArchiveComment) error {
		if v.AuthorSnapshot != nil {
			restoredProfiles++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if restoredProfiles != snapshots {
		t.Fatalf("site restore lost historical authors: %d -> %d", snapshots, restoredProfiles)
	}
}

func TestPersonalImportRejectsChangedForeignCancelledAndLowDiskArchives(t *testing.T) {
	ctx := context.Background()
	source := newExportFixture(t, true)
	target := newExportFixture(t, false)
	var zip bytes.Buffer
	if _, err := source.exporter.ExportAccount(ctx, &zip, fixtureAccountID); err != nil {
		t.Fatal(err)
	}
	plan, err := target.exporter.ImportAccount(ctx, bytes.NewReader(zip.Bytes()), fixtureAccountID, AccountImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	before, err := target.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := before.Counts()
	_ = before.Close()
	if _, err = target.exporter.ImportAccount(ctx, bytes.NewReader(zip.Bytes()), fixtureAccountID, AccountImportOptions{ExpectedSHA: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("unreviewed archive accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = target.exporter.ImportAccount(cancelled, bytes.NewReader(zip.Bytes()), fixtureAccountID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256}); err == nil {
		t.Fatal("cancelled import succeeded")
	}
	members, order := readArchive(t, zip.Bytes())
	members["data/entries.jsonl"] = rewriteEntryLine(t, members["data/entries.jsonl"], entryID, func(value map[string]json.RawMessage) {
		value["author_user_id"] = json.RawMessage(`"` + memberAccountID + `"`)
	})
	rebuildChecksums(members)
	forged := filepath.Join(t.TempDir(), "forged.zip")
	writeArchive(t, forged, members, order)
	data, err := os.ReadFile(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.exporter.ImportAccount(ctx, bytes.NewReader(data), fixtureAccountID, AccountImportOptions{DryRun: true}); err == nil {
		t.Fatal("another account's authored records accepted")
	}
	target.exporter.availableBytes = func(string) (uint64, error) { return 0, nil }
	if _, err = target.exporter.ImportAccount(ctx, bytes.NewReader(zip.Bytes()), fixtureAccountID, AccountImportOptions{DryRun: true}); !domain.HasCode(err, domain.CodeImportDiskSpaceInsufficient) {
		t.Fatalf("low disk=%v", err)
	}
	after, err := target.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = after.Close() }()
	if after.Counts() != counts {
		t.Fatal("rejected import changed existing data")
	}
	stages, err := filepath.Glob(filepath.Join(target.runtime.Root, "staging", "account-import-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("private staging was retained: %v %v", stages, err)
	}
}

func TestPersonalImportRollsBackRowsAndPublishedMediaWhenACopyFails(t *testing.T) {
	ctx := context.Background()
	source := newExportFixture(t, true)
	target := newExportFixture(t, false)
	var zip bytes.Buffer
	if _, err := source.exporter.ExportAccount(ctx, &zip, fixtureAccountID); err != nil {
		t.Fatal(err)
	}
	plan, err := target.exporter.ImportAccount(ctx, bytes.NewReader(zip.Bytes()), fixtureAccountID, AccountImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := source.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err = snap.Media(ctx, func(v sqlitestore.ArchiveMedia) error { paths = append(paths, v.StoragePath); return nil }); err != nil {
		t.Fatal(err)
	}
	_ = snap.Close()
	if len(paths) < 2 {
		t.Fatal("fixture needs at least two originals")
	}
	blocked := filepath.Join(target.runtime.Current.Root, filepath.FromSlash(paths[len(paths)-1]))
	if err = os.MkdirAll(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := target.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := before.Counts()
	_ = before.Close()
	if _, err = target.exporter.ImportAccount(ctx, bytes.NewReader(zip.Bytes()), fixtureAccountID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256}); err == nil {
		t.Fatal("copy failure unexpectedly committed")
	}
	after, err := target.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = after.Close() }()
	if after.Counts() != counts {
		t.Fatal("failed copy partially committed rows")
	}
	if _, err = os.Lstat(filepath.Join(target.runtime.Current.Root, filepath.FromSlash(paths[0]))); !os.IsNotExist(err) {
		t.Fatalf("a partially published original survived rollback: %v", err)
	}
}

func TestPersonalExportRestoresLinkCardOriginalsSelectedByContentHash(t *testing.T) {
	ctx := context.Background()
	source := newExportFixture(t, true)
	thumbID, iconID := "01ARZ3NDEKTSV4RRFFQ69G5FEC", "01ARZ3NDEKTSV4RRFFQ69G5FED"
	_, thumbSHA := insertArchiveMedia(t, source.database, source.runtime, thumbID, distinctTestImage(t, 7), "image/png", "thumbnail.png", false, exportFixtureClock)
	_, iconSHA := insertArchiveMedia(t, source.database, source.runtime, iconID, distinctTestImage(t, 8), "image/png", "icon.png", false, exportFixtureClock)
	if err := source.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(ctx, `INSERT INTO entry_link_card(entry_id,position,source_url,canonical_url,provider,card_kind,title,description,site_name,author_name,duration_seconds,thumbnail_media_id,favicon_media_id,embed_url,captured_at_ms) VALUES(?,0,'https://example.com/reference','https://example.com/reference','generic','link','Captured reference','','Example','',NULL,?,?,'',1700000000000)`, entryID, thumbID, iconID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := source.exporter.ExportAccount(ctx, &output, fixtureAccountID); err != nil {
		t.Fatal(err)
	}
	members, _ := readArchive(t, output.Bytes())
	if len(members["media/"+thumbSHA+".png"]) == 0 || len(members["media/"+iconSHA+".png"]) == 0 {
		t.Fatal("personal export omitted a card thumbnail or favicon referenced only by its hash")
	}
	target := newExportFixture(t, false)
	insertAccountsFixture(t, target)
	plan, err := target.exporter.ImportAccount(ctx, bytes.NewReader(output.Bytes()), memberAccountID, AccountImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.exporter.ImportAccount(ctx, bytes.NewReader(output.Bytes()), memberAccountID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := target.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	found := false
	if err = snapshot.Entries(ctx, func(v sqlitestore.ArchiveEntry) error {
		if v.AuthorUserID == memberAccountID && len(v.LinkCards) == 1 {
			found = v.LinkCards[0].ThumbnailSHA256 == thumbSHA && v.LinkCards[0].FaviconSHA256 == iconSHA
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("restored card lost its images")
	}
}
