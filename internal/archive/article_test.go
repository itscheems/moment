package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	articleEntryID  = "01ARZ3NDEKTSV4RRFFQ69G5FD9"
	articleDraftID  = "01ARZ3NDEKTSV4RRFFQ69G5FDA"
	articleCoverID  = "01ARZ3NDEKTSV4RRFFQ69G5FDB"
	articleInlineID = "01ARZ3NDEKTSV4RRFFQ69G5FDC"
	draftCoverID    = "01ARZ3NDEKTSV4RRFFQ69G5FDD"
	draftPictureID  = "01ARZ3NDEKTSV4RRFFQ69G5FDE"

	articleTitle = "一篇长文"
	draftTitle   = "还没写完"
)

// articleFixture remembers what was seeded, so a round trip can be compared
// against it rather than against whatever came back.
type articleFixture struct {
	body      []byte
	draftBody []byte
	// The four pictures no moment, comment or account points at. They are in
	// the archive only because an article or a draft names them, which is what
	// makes them the ones a packaging mistake loses.
	coverSHA, inlineSHA, draftCoverSHA, draftPictureSHA string
}

// seedArticleFixture adds one published article and one draft of it to an
// export fixture, with pictures nothing else refers to.
//
// The rows are written as SQL rather than through the article store because
// this package cannot reach that store, and because stating the projection by
// hand is the point: the archive has to agree with what the columns say, not
// with what a helper would have derived a second time.
func seedArticleFixture(t *testing.T, fixture exportFixture) articleFixture {
	t.Helper()
	ctx := context.Background()
	body := articleBodyJSON(t, `
{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"第一节"}]},
{"type":"paragraph","content":[
  {"type":"text","text":"正文 "},
  {"type":"text","text":"重点","marks":[{"type":"bold"}]},
  {"type":"text","text":" 与 "},
  {"type":"text","text":"链接","marks":[{"type":"link","attrs":{"href":"https://example.com/a"}}]}
]},
{"type":"image","attrs":{"media_id":"`+articleInlineID+`","alt":"街景"}},
{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"package main"}]}`)
	draftBody := articleBodyJSON(t, `
{"type":"paragraph","content":[{"type":"text","text":"草稿正文"}]},
{"type":"image","attrs":{"media_id":"`+draftPictureID+`"}}`)

	seeded := articleFixture{body: body, draftBody: draftBody}
	pictures := []struct {
		id     string
		seed   int
		target *string
	}{
		{articleCoverID, 1, &seeded.coverSHA},
		{articleInlineID, 2, &seeded.inlineSHA},
		{draftCoverID, 3, &seeded.draftCoverSHA},
		{draftPictureID, 4, &seeded.draftPictureSHA},
	}
	for _, picture := range pictures {
		_, sha := insertArchiveMedia(t, fixture.database, fixture.runtime, picture.id,
			distinctTestImage(t, picture.seed), "image/png", "article.png", false, exportFixtureClock)
		*picture.target = sha
	}

	document, err := domain.ParseArticleDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	moment := exportFixtureClock.UnixMilli()
	if err := fixture.database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES (?, 'article', NULL, ?, ?, ?, ?, '0123456789abcdef0123456789abcdef')`, []any{articleEntryID, moment, moment, moment, domain.ArticleSearchText(articleTitle, document)}},
			{`INSERT INTO entry_article (entry_id, title, body_json, body_schema_version, cover_media_id, excerpt)
VALUES (?, ?, ?, 1, ?, ?)`, []any{articleEntryID, articleTitle, string(body), articleCoverID, domain.ArticleExcerpt(document)}},
			{`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, '街景')`, []any{articleEntryID, articleInlineID}},
			{`INSERT INTO article_drafts (
    id, title, body_json, body_schema_version, cover_media_id, published_entry_id, created_at_ms, updated_at_ms
, author_user_id) VALUES (?, ?, ?, 1, ?, ?, ?, ?, '0123456789abcdef0123456789abcdef')`, []any{articleDraftID, draftTitle, string(draftBody), draftCoverID, articleEntryID, moment, moment}},
			{`INSERT INTO article_draft_media (draft_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, []any{articleDraftID, draftPictureID}},
		}
		for _, statement := range statements {
			if _, err := queries.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return seeded
}

// articleBodyJSON returns the canonical serialization of a body written as
// source, which is what the column holds and therefore what the archive must
// carry back byte for byte.
func articleBodyJSON(t *testing.T, content string) []byte {
	t.Helper()
	document, err := domain.ParseArticleDraftDocument([]byte(`{"schema_version":1,"type":"doc","content":[` + content + `]}`))
	if err != nil {
		t.Fatalf("parse article body: %v", err)
	}
	encoded, err := domain.MarshalArticleDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// distinctTestImage renders one 32x24 PNG whose bytes differ per seed, so each
// picture gets its own content hash and its own archive member.
func distinctTestImage(t *testing.T, seed int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			value.SetNRGBA(x, y, color.NRGBA{R: uint8(x*3 + seed*29), G: uint8(y*5 + seed*11), B: uint8(seed * 37), A: 0xff})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// stageArchive inspects an archive and opens the generation it staged, which is
// the rebuilt database an import would publish.
func stageArchive(t *testing.T, fixture exportFixture, archivePath string) *sqlitestore.Database {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(ctx, archivePath, testAuthorization(7))
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
	runtimeState := &runtimefs.Runtime{
		Root: fixture.runtime.Root, Instance: fixture.runtime.Instance,
		Current: stagingGeneration(stage, inspection.GenerationID), Locks: fixture.runtime.Locks,
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func stagedSnapshot(t *testing.T, database *sqlitestore.Database) *sqlitestore.ArchiveSnapshot {
	t.Helper()
	snapshot, err := database.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snapshot.Close() })
	return snapshot
}

// The export has to carry every picture an article points at, and the two that
// do not travel through entry_media are exactly the ones a packaging mistake
// loses: a cover, and everything a draft holds. Nothing else in this fixture
// refers to these four files, so an archive missing one is an archive that
// restores an article with a hole in it.
func TestExportPacksEveryPictureOnlyAnArticleOrADraftPointsAt(t *testing.T) {
	fixture := newExportFixture(t, true)
	seeded := seedArticleFixture(t, fixture)
	members, _ := readArchive(t, exportFixtureBytes(t, fixture))
	for name, sha := range map[string]string{
		"article cover": seeded.coverSHA, "article body picture": seeded.inlineSHA,
		"draft cover": seeded.draftCoverSHA, "draft picture": seeded.draftPictureSHA,
	} {
		if _, packed := members["media/"+sha+".png"]; !packed {
			t.Fatalf("the %s was left out of the archive", name)
		}
	}
	// The metadata has to name them too, or the blob is an orphan the importer
	// has no row to attach.
	catalog := string(members["data/media.jsonl"])
	for _, id := range []string{articleCoverID, articleInlineID, draftCoverID, draftPictureID} {
		if !strings.Contains(catalog, `"id":"`+id+`"`) {
			t.Fatalf("media.jsonl does not name %s", id)
		}
	}
}

// Raising the format version would make the backup a person took just before
// upgrading unreadable by the binary they upgraded to, which is the one moment
// a backup exists for. Articles are additive members, not a new version.
func TestCompleteArticleGraphUsesFormatVersionTwo(t *testing.T) {
	fixture := newExportFixture(t, true)
	seedArticleFixture(t, fixture)
	members, _ := readArchive(t, exportFixtureBytes(t, fixture))
	var document manifest
	if err := json.Unmarshal(members["manifest.json"], &document); err != nil {
		t.Fatal(err)
	}
	if document.FormatVersion != 2 || FormatVersion != 2 || FormatHeaderValue != "moment-export-v2" {
		t.Fatalf("an archive carrying an article declared %#v", document)
	}
	if document.Counts.Articles != 1 || document.Counts.ArticleDrafts != 1 {
		t.Fatalf("manifest counts = %#v", document.Counts)
	}
	// The README is compared byte for byte on read, so an archive written
	// before articles existed stops being readable the moment it changes.
	if !bytes.Equal(members["README.txt"], []byte(readme)) {
		t.Fatal("the fixed v1 README was rewritten, which makes every earlier backup unreadable")
	}
}

// A backup taken before articles existed carries no article member, and that
// is not a loss: the site it was taken from had none. Restoring it must produce
// a generation with no article and no draft rather than refuse.
func TestArchiveWithoutArticleMembersIsRejected(t *testing.T) {
	fixture := newExportFixture(t, true)
	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	order = stripArticleMembers(t, members, order)
	name := filepath.Join(t.TempDir(), "incomplete.zip")
	writeArchive(t, name, members, order)
	if err := ValidateFile(name); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("incomplete graph=%v", err)
	}
}

func TestAnArticleAndItsBodyTravelTogetherOrTheArchiveIsRefused(t *testing.T) {
	t.Run("entry without a body", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		seedArticleFixture(t, fixture)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		members["data/entry_articles.jsonl"] = nil
		setManifestCount(t, members, "articles", 0)
		rebuildManifestExpanded(t, members)
		rebuildChecksums(members)
		name := filepath.Join(t.TempDir(), "body-removed.zip")
		writeArchive(t, name, members, order)
		if err := ValidateFile(name); err == nil || !strings.Contains(err.Error(), "no body") {
			t.Fatalf("an article entry with no body was accepted as %v", err)
		}
	})
	t.Run("body without an entry", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		seedArticleFixture(t, fixture)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		dropArticleEntry(t, members)
		rebuildManifestExpanded(t, members)
		rebuildChecksums(members)
		name := filepath.Join(t.TempDir(), "entry-removed.zip")
		writeArchive(t, name, members, order)
		if err := ValidateFile(name); err == nil || !strings.Contains(err.Error(), "belongs to no article entry") {
			t.Fatalf("an orphan article body was accepted as %v", err)
		}
	})
}

// The entry states which of the two body shapes it has, and content_md has to
// agree. A moment with no Markdown renders as a blank post and an article with
// Markdown has two bodies, so both are refused before anything is staged.
func TestAnEntryMustAgreeWithItselfAboutCarryingMarkdown(t *testing.T) {
	for _, mutation := range []struct {
		name    string
		id      string
		replace string
	}{
		{"a moment with no Markdown", entryID, `"content_md":null`},
		{"an article carrying Markdown", articleEntryID, `"content_md":"偷渡的正文"`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := newExportFixture(t, true)
			seedArticleFixture(t, fixture)
			members, order := readArchive(t, exportFixtureBytes(t, fixture))
			members["data/entries.jsonl"] = rewriteEntryLine(t, members["data/entries.jsonl"], mutation.id, func(value map[string]json.RawMessage) {
				var replacement map[string]json.RawMessage
				if err := json.Unmarshal([]byte(`{`+mutation.replace+`}`), &replacement); err != nil {
					t.Fatal(err)
				}
				value["content_md"] = replacement["content_md"]
			})
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			name := filepath.Join(t.TempDir(), "mismatched-body.zip")
			writeArchive(t, name, members, order)
			if err := ValidateFile(name); err == nil {
				t.Fatal("an entry that disagrees with itself about its body was accepted")
			}
		})
	}
}

// entry_media is a projection of the body: the body is the only place a
// picture's position is recorded. A record free to state a different list is
// free to keep a file alive the article no longer shows.
func TestAnArticleWhoseProjectedPicturesDisagreeWithItsBodyIsRefused(t *testing.T) {
	fixture := newExportFixture(t, true)
	seedArticleFixture(t, fixture)
	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	members["data/entries.jsonl"] = rewriteEntryLine(t, members["data/entries.jsonl"], articleEntryID, func(value map[string]json.RawMessage) {
		value["media"] = json.RawMessage(`[{"media_id":"` + articleCoverID + `","position":0,"alt_text":"街景"}]`)
	})
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "drifted-projection.zip")
	writeArchive(t, name, members, order)
	if err := ValidateFile(name); err == nil || !strings.Contains(err.Error(), "the body places") {
		t.Fatalf("a projection that does not match the body was accepted as %v", err)
	}
}

// Merge compares entries, comments, media and accounts. An article is none of
// those on its own, so every merge path refuses one rather than producing a
// merged site whose articles came back blank.
func TestMergeRefusesASiteThatHoldsAnArticle(t *testing.T) {
	fixture := newExportFixture(t, true)
	seedArticleFixture(t, fixture)
	_, err := ReadPortableAuthority(context.Background(), fixture.database)
	if !errors.Is(err, ErrMergeArticleUnsupported) {
		t.Fatalf("merge read an authority holding an article: %v", err)
	}
}

// dropArticleEntry removes the article's line from data/entries.jsonl and
// lowers the count that answers for it, leaving every other member alone.
func dropArticleEntry(t *testing.T, members map[string][]byte) {
	t.Helper()
	var kept []byte
	remaining, removed := int64(0), 0
	for _, line := range nonemptyLines(members["data/entries.jsonl"]) {
		if bytes.Contains(line, []byte(`"id":"`+articleEntryID+`"`)) {
			removed++
			continue
		}
		kept = append(kept, line...)
		kept = append(kept, '\n')
		remaining++
	}
	if removed != 1 {
		t.Fatalf("expected one article entry to drop, found %d", removed)
	}
	members["data/entries.jsonl"] = kept
	setManifestCount(t, members, "entries", remaining)
}

// rewriteEntryLine edits one entry record in place and leaves the rest of the
// member byte-identical.
func rewriteEntryLine(t *testing.T, member []byte, id string, edit func(map[string]json.RawMessage)) []byte {
	t.Helper()
	var rebuilt []byte
	edited := 0
	for _, line := range nonemptyLines(member) {
		var value map[string]json.RawMessage
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(line, []byte(`"id":"`+id+`"`)) {
			edit(value)
			edited++
		}
		rebuilt = append(rebuilt, mustMarshalTest(t, value)...)
		rebuilt = append(rebuilt, '\n')
	}
	if edited != 1 {
		t.Fatalf("expected one record for %s, edited %d", id, edited)
	}
	return rebuilt
}

// The same rule for a draft: its pictures are a projection of its body, and a
// projection that says something else keeps a file alive the draft does not
// show.
func TestADraftWhoseProjectedPicturesDisagreeWithItsBodyIsRefused(t *testing.T) {
	fixture := newExportFixture(t, true)
	seedArticleFixture(t, fixture)
	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	members["data/article_drafts.jsonl"] = rewriteEntryLine(t, members["data/article_drafts.jsonl"], articleDraftID, func(value map[string]json.RawMessage) {
		value["media"] = json.RawMessage(`[]`)
	})
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "drifted-draft.zip")
	writeArchive(t, name, members, order)
	if err := ValidateFile(name); err == nil || !strings.Contains(err.Error(), "the body places") {
		t.Fatalf("a draft projection that does not match the body was accepted as %v", err)
	}
}

// A personal data export is not a backup, but it is the copy a person is given
// of their own writing. Handing them article envelopes with no words in them,
// and no drafts at all, would make it a copy of the parts they care least
// about.
func TestPersonalExportCarriesTheAuthorsArticlesAndDrafts(t *testing.T) {
	fixture := newExportFixture(t, true)
	seeded := seedArticleFixture(t, fixture)
	ctx := context.Background()
	var primaryAccountID string
	if err := fixture.database.ReadTx(ctx, func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(ctx, `SELECT profile_user_id FROM site_settings WHERE id = 1`).Scan(&primaryAccountID)
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := fixture.exporter.ExportAccount(ctx, &output, primaryAccountID); err != nil {
		t.Fatal(err)
	}
	members, _ := readArchive(t, output.Bytes())
	articles := nonemptyLines(members["data/entry_articles.jsonl"])
	drafts := nonemptyLines(members["data/article_drafts.jsonl"])
	if len(articles) != 1 || len(drafts) != 1 {
		t.Fatalf("personal export holds %d bodies and %d drafts", len(articles), len(drafts))
	}
	var body entryArticleRecord
	if err := json.Unmarshal(articles[0], &body); err != nil {
		t.Fatal(err)
	}
	if body.EntryID != articleEntryID || body.Title != articleTitle || !bytes.Equal(body.Body, seeded.body) {
		t.Fatalf("exported body = %#v", body)
	}
	// The pictures the two members name have to come with them, or the copy
	// points at files the person was not given.
	for name, sha := range map[string]string{
		"article cover": seeded.coverSHA, "draft cover": seeded.draftCoverSHA, "draft picture": seeded.draftPictureSHA,
	} {
		if _, packed := members["media/"+sha+".png"]; !packed {
			t.Fatalf("the %s is missing from the personal export", name)
		}
	}
	file := filepath.Join(t.TempDir(), "owner.zip")
	if err := os.WriteFile(file, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccountFileContext(ctx, file); err != nil {
		t.Fatal(err)
	}
}

func TestWithdrawnArticleStateAndAuthorsSurviveBackupRestore(t *testing.T) {
	fixture := newExportFixture(t, true)
	seedArticleFixture(t, fixture)
	ctx := context.Background()
	var originalAuthor string
	if err := fixture.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		if err := q.QueryRowContext(ctx, `SELECT author_user_id FROM entries WHERE id=?`, articleEntryID).Scan(&originalAuthor); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE entries SET publication_state='unpublished' WHERE id=?`, articleEntryID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	archiveBytes := exportFixtureBytes(t, fixture)
	name := filepath.Join(t.TempDir(), "withdrawn.zip")
	if err := os.WriteFile(name, archiveBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFile(name); err != nil {
		t.Fatal(err)
	}
	target := newExportFixture(t, false)
	snapshot := stagedSnapshot(t, stageArchive(t, target, name))
	found := false
	if err := snapshot.Entries(ctx, func(entry sqlitestore.ArchiveEntry) error {
		if entry.ID == articleEntryID {
			found = true
			if entry.PublicationState != "unpublished" || entry.AuthorUserID != originalAuthor {
				t.Fatalf("restored article metadata=%+v", entry)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("withdrawn article lost from archive")
	}
	if err := snapshot.ArticleDrafts(ctx, func(draft sqlitestore.ArchiveArticleDraft) error {
		if draft.ID == articleDraftID && draft.AuthorUserID != originalAuthor {
			t.Fatal("draft author changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
