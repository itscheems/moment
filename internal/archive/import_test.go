package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestInspectBuildsRestartableCurrentSchemaWithoutTouchingCurrent(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	currentBefore, err := os.ReadFile(filepath.Join(fixture.runtime.Root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	result, err := inspector.InspectFile(context.Background(), archivePath, testAuthorization(7))
	if err != nil {
		t.Fatal(err)
	}
	if result.FormatVersion != FormatVersion || result.Counts != (ImportCounts{Entries: 1, Comments: 2, Media: 2, Users: 1}) || result.MissingAlt != 1 || result.StagingToken == "" {
		t.Fatalf("inspection = %#v", result)
	}
	if !result.Disk.SpaceOK || result.Disk.RequiredAdditionalBytes <= 0 || result.Disk.SafetyMarginBytes < minimumSafetyMarginBytes {
		t.Fatalf("disk = %#v", result.Disk)
	}
	currentAfter, err := os.ReadFile(filepath.Join(fixture.runtime.Root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(currentBefore, currentAfter) {
		t.Fatalf("CURRENT changed: before=%q after=%q", currentBefore, currentAfter)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	metadataBytes, err := os.ReadFile(filepath.Join(stageRoot, "staging.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadataBytes, []byte(result.StagingToken)) {
		t.Fatal("staging metadata contains plaintext token")
	}
	stagedGeneration := stagingGeneration(stageRoot, result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{Root: fixture.runtime.Root, Instance: fixture.runtime.Instance, Current: stagedGeneration, Locks: fixture.runtime.Locks}
	stagedDatabase, err := sqlitestore.Open(context.Background(), stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	stagedSnapshot, err := stagedDatabase.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var stagedEntries []sqlitestore.ArchiveEntry
	var stagedComments []sqlitestore.ArchiveComment
	if err := stagedSnapshot.Entries(context.Background(), func(value sqlitestore.ArchiveEntry) error {
		stagedEntries = append(stagedEntries, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(stagedEntries) != 1 || len(stagedEntries[0].Media) != 3 || stagedEntries[0].Media[0].AltText != nil ||
		stagedEntries[0].Media[1].AltText == nil || *stagedEntries[0].Media[1].AltText != "" ||
		stagedEntries[0].Media[2].AltText == nil || *stagedEntries[0].Media[2].AltText != "街景" ||
		stagedEntries[0].Location == nil || stagedEntries[0].Location.Name != "静安寺" || stagedEntries[0].Location.Point == nil || stagedEntries[0].Location.Point.System != domain.CoordinateGCJ02 {
		t.Fatalf("staged entries = %#v", stagedEntries)
	}
	if err := stagedSnapshot.Comments(context.Background(), func(value sqlitestore.ArchiveComment) error {
		stagedComments = append(stagedComments, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(stagedComments) != 2 || len(stagedComments[0].Media) != 2 ||
		stagedComments[0].Media[0].MediaID != mediaOneID || stagedComments[0].Media[0].AltText != nil ||
		stagedComments[0].Media[1].MediaID != mediaTwoID || stagedComments[0].Media[1].AltText == nil || *stagedComments[0].Media[1].AltText != "街景" ||
		len(stagedComments[1].Media) != 1 || stagedComments[1].Media[0].MediaID != mediaTwoID ||
		stagedComments[1].Media[0].AltText == nil || *stagedComments[1].Media[0].AltText != "" ||
		stagedComments[0].Location != nil || stagedComments[1].Location == nil || stagedComments[1].Location.Name != "静安寺" ||
		stagedComments[1].Location.Point == nil || stagedComments[1].Location.Point.System != domain.CoordinateGCJ02 {
		t.Fatalf("staged comments = %#v", stagedComments)
	}
	if err := stagedSnapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stagedDatabase.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		var normalized string
		var matches int
		if err := queries.QueryRowContext(context.Background(), `SELECT content_text FROM entries WHERE id = ?`, entryID).Scan(&normalized); err != nil {
			return err
		}
		if err := queries.QueryRowContext(context.Background(), `SELECT count(*) FROM entry_fts WHERE entry_fts MATCH ?`, "Unicode").Scan(&matches); err != nil {
			return err
		}
		if normalized != "unicode 世界" || matches != 1 {
			return errors.New("staged normalized text or FTS query result is invalid")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := stagedDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	resumed, err := restarted.Resume(context.Background(), result.StagingToken, testAuthorization(7))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.GenerationID != result.GenerationID || resumed.StagingToken != "" || resumed.Counts != result.Counts {
		t.Fatalf("resumed = %#v", resumed)
	}
	type claimResult struct {
		claim StagingClaim
		err   error
	}
	start := make(chan struct{})
	claims := make(chan claimResult, 2)
	for range 2 {
		go func() {
			<-start
			claim, err := restarted.Claim(context.Background(), result.StagingToken, testAuthorization(7))
			claims <- claimResult{claim: claim, err: err}
		}()
	}
	close(start)
	first, second := <-claims, <-claims
	var claim StagingClaim
	switch {
	case first.err == nil && domain.HasCode(second.err, domain.CodeImportStagingInvalid):
		claim = first.claim
	case second.err == nil && domain.HasCode(first.err, domain.CodeImportStagingInvalid):
		claim = second.claim
	default:
		t.Fatalf("concurrent claims = %#v, %#v", first, second)
	}
	if claim.GenerationID != result.GenerationID || claim.Generation.Root != filepath.Join(stageRoot, "generation") {
		t.Fatalf("claim = %#v", claim)
	}
	if _, err := restarted.Claim(context.Background(), result.StagingToken, testAuthorization(7)); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestUnavailableQuoteRoundTripsThroughPortableArchive(t *testing.T) {
	const quoteID = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	fixture := newExportFixture(t, false)
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', '引用外层仍然存在', 1, 1, 1, '0123456789abcdef0123456789abcdef')`, quoteID); err != nil {
			return err
		}
		_, err := queries.ExecContext(context.Background(), `INSERT INTO entry_quotes (entry_id, quoted_entry_id) VALUES (?, NULL)`, quoteID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 16, 1, 2, 3, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		exportFixtureFile(t, fixture),
		testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{
		Root: fixture.runtime.Root, Instance: fixture.runtime.Instance,
		Current: stagingGeneration(stageRoot, result.GenerationID), Locks: fixture.runtime.Locks,
	}
	stagedDatabase, err := sqlitestore.Open(context.Background(), stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stagedDatabase.Close() }()
	snapshot, err := stagedDatabase.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var entries []sqlitestore.ArchiveEntry
	if err := snapshot.Entries(context.Background(), func(value sqlitestore.ArchiveEntry) error {
		entries = append(entries, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != quoteID || !entries[0].QuoteUnavailable || entries[0].QuotedEntryID != nil {
		t.Fatalf("round-tripped quote tombstone = %#v", entries)
	}
}

func TestPinnedCommentRoundTripsThroughPortableArchive(t *testing.T) {
	const pinnedEntryID = "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	const pinnedCommentID = "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	fixture := newExportFixture(t, false)
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', '置顶评论所在的动态', 1, 1, 1, '0123456789abcdef0123456789abcdef')`, pinnedEntryID); err != nil {
			return err
		}
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms)
VALUES (?, ?, NULL, '访客', '置顶评论', 1)`, pinnedCommentID, pinnedEntryID); err != nil {
			return err
		}
		_, err := queries.ExecContext(context.Background(), `INSERT INTO comment_pins (entry_id, comment_id) VALUES (?, ?)`, pinnedEntryID, pinnedCommentID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 22, 1, 2, 3, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		exportFixtureFile(t, fixture),
		testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{
		Root: fixture.runtime.Root, Instance: fixture.runtime.Instance,
		Current: stagingGeneration(stageRoot, result.GenerationID), Locks: fixture.runtime.Locks,
	}
	stagedDatabase, err := sqlitestore.Open(context.Background(), stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stagedDatabase.Close() }()
	snapshot, err := stagedDatabase.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var comments []sqlitestore.ArchiveComment
	if err := snapshot.Comments(context.Background(), func(value sqlitestore.ArchiveComment) error {
		comments = append(comments, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].ID != pinnedCommentID || !comments[0].Pinned {
		t.Fatalf("round-tripped pinned comment = %#v", comments)
	}
}

// The account a comment belongs to is a v1 extension: archives written before
// it existed carry no author_user_id key at all, and those archives must keep
// importing. These three tests pin both directions of that contract.
const (
	authorAccountEntryID   = "01ARZ3NDEKTSV4RRFFQ69G5FB3"
	authorAccountCommentID = "01ARZ3NDEKTSV4RRFFQ69G5FB4"
	legacyGuestCommentID   = "01ARZ3NDEKTSV4RRFFQ69G5FB5"
	commentAuthorAccount   = "0123456789abcdef0123456789abcdef"
)

func TestCommentAuthorAccountRoundTripsThroughPortableArchive(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertCommentAuthorFixture(t, fixture)

	members, _ := readArchive(t, exportFixtureBytes(t, fixture))
	exported := decodeCommentLines(t, members["data/comments.jsonl"])
	if len(exported) != 2 {
		t.Fatalf("exported comment records = %d", len(exported))
	}
	if account, present := exported[authorAccountCommentID]["author_user_id"]; !present || account != commentAuthorAccount {
		t.Fatalf("exported author_user_id = %v present=%v", account, present)
	}
	if _, present := exported[legacyGuestCommentID]["author_user_id"]; present {
		t.Fatal("a comment without an account emitted an author_user_id key")
	}

	now := time.Date(2026, 8, 22, 2, 3, 4, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		exportFixtureFile(t, fixture),
		testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	staged := stagedArchiveComments(t, fixture, result)
	if len(staged) != 2 {
		t.Fatalf("staged comments = %#v", staged)
	}
	primaryAccount := staged[0]
	if primaryAccount.ID != authorAccountCommentID || primaryAccount.AuthorUserID == nil || *primaryAccount.AuthorUserID != commentAuthorAccount {
		t.Fatalf("staged owner comment = %#v", primaryAccount)
	}
	if staged[1].ID != legacyGuestCommentID || staged[1].AuthorUserID != nil {
		t.Fatalf("staged guest comment = %#v", staged[1])
	}
}

func TestArchiveWithoutCommentAuthorAccountStillImports(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertCommentAuthorFixture(t, fixture)

	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	stripped := make([]byte, 0, len(members["data/comments.jsonl"]))
	removed := 0
	for _, line := range nonemptyLines(members["data/comments.jsonl"]) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if _, present := record["author_user_id"]; present {
			removed++
		}
		delete(record, "author_user_id")
		stripped = append(append(stripped, mustMarshalTest(t, record)...), '\n')
	}
	if removed != 1 {
		t.Fatalf("stripped %d author_user_id keys, want 1", removed)
	}
	members["data/comments.jsonl"] = stripped
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "legacy-without-comment-author-account.zip")
	writeArchive(t, name, members, order)

	if err := ValidateFile(name); err != nil {
		t.Fatalf("v1 archive without author_user_id was rejected: %v", err)
	}
	now := time.Date(2026, 8, 22, 3, 4, 5, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		name,
		testAuthorization(7),
	)
	if err != nil {
		t.Fatalf("v1 archive without author_user_id did not import: %v", err)
	}
	staged := stagedArchiveComments(t, fixture, result)
	if len(staged) != 2 {
		t.Fatalf("staged comments = %#v", staged)
	}
	for _, value := range staged {
		if value.AuthorUserID != nil {
			t.Fatalf("absent author_user_id was invented: %#v", value)
		}
	}
}

func TestLegacyGuestCommentWithoutAuthorAccountRoundTrips(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertCommentAuthorFixture(t, fixture)

	now := time.Date(2026, 8, 22, 4, 5, 6, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		exportFixtureFile(t, fixture),
		testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	staged := stagedArchiveComments(t, fixture, result)
	if len(staged) != 2 {
		t.Fatalf("staged comments = %#v", staged)
	}
	guest := staged[1]
	if guest.ID != legacyGuestCommentID || guest.AuthorName != "访客" ||
		guest.ContentMD != "匿名访客留下的评论" || guest.AuthorUserID != nil {
		t.Fatalf("round-tripped legacy guest comment = %#v", guest)
	}
}

// Who a reply answers is a v1 extension like the author account, with one
// difference that matters everywhere identities move: it names a comment in the
// same archive. The canonical thread order is (created_at_ms, id), so a reply
// can be written before the sibling it answers whenever the two share a
// millisecond — the case these IDs are chosen to produce.
const (
	threadEntryID    = "01ARZ3NDEKTSV4RRFFQ69G5FB6"
	threadRootID     = "01ARZ3NDEKTSV4RRFFQ69G5FB7"
	replyToLaterID   = "01ARZ3NDEKTSV4RRFFQ69G5FC0"
	replyToRootID    = "01ARZ3NDEKTSV4RRFFQ69G5FC1"
	replyToEarlierID = "01ARZ3NDEKTSV4RRFFQ69G5FC2"
)

func TestCommentReplyTargetRoundTripsThroughPortableArchive(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertReplyTargetFixture(t, fixture)

	members, _ := readArchive(t, exportFixtureBytes(t, fixture))
	order := commentLineOrder(t, members["data/comments.jsonl"])
	// Guard the premise, not just the outcome: this archive really does name a
	// target that has not been written yet, so an importer that links on insert
	// cannot pass the tests below by accident.
	if order[replyToLaterID] > order[replyToEarlierID] {
		t.Fatalf("fixture no longer exercises a forward reply reference: %#v", order)
	}

	now := time.Date(2026, 8, 22, 5, 6, 7, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		exportFixtureFile(t, fixture),
		testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	staged := indexComments(stagedArchiveComments(t, fixture, result))
	if len(staged) != 4 {
		t.Fatalf("staged comments = %#v", staged)
	}
	if staged[threadRootID].ReplyToCommentID != nil {
		t.Fatalf("a root comment gained a reply target: %#v", staged[threadRootID])
	}
	for _, expectation := range []struct{ comment, target string }{
		{replyToRootID, threadRootID},
		{replyToEarlierID, replyToRootID},
		{replyToLaterID, replyToEarlierID},
	} {
		value := staged[expectation.comment].ReplyToCommentID
		if value == nil || *value != expectation.target {
			t.Fatalf("comment %s answers %s, want %s", expectation.comment, commentTargetText(value), expectation.target)
		}
	}
}

func TestArchiveWithoutCommentReplyTargetStillImports(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertReplyTargetFixture(t, fixture)

	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	stripped := make([]byte, 0, len(members["data/comments.jsonl"]))
	removed := 0
	for _, line := range nonemptyLines(members["data/comments.jsonl"]) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if _, present := record["reply_to_comment_id"]; present {
			removed++
		}
		delete(record, "reply_to_comment_id")
		stripped = append(append(stripped, mustMarshalTest(t, record)...), '\n')
	}
	if removed != 3 {
		t.Fatalf("stripped %d reply_to_comment_id keys, want 3", removed)
	}
	members["data/comments.jsonl"] = stripped
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "legacy-without-comment-reply-target.zip")
	writeArchive(t, name, members, order)

	if err := ValidateFile(name); err != nil {
		t.Fatalf("v1 archive without reply_to_comment_id was rejected: %v", err)
	}
	now := time.Date(2026, 8, 22, 6, 7, 8, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		name,
		testAuthorization(7),
	)
	if err != nil {
		t.Fatalf("v1 archive without reply_to_comment_id did not import: %v", err)
	}
	staged := stagedArchiveComments(t, fixture, result)
	if len(staged) != 4 {
		t.Fatalf("staged comments = %#v", staged)
	}
	for _, value := range staged {
		if value.ReplyToCommentID != nil {
			t.Fatalf("absent reply_to_comment_id was invented: %#v", value)
		}
	}
}

// insertReplyTargetFixture writes one thread whose replies answer the root, an
// earlier sibling, and a later sibling. All three replies share a millisecond,
// so canonical order falls back to ID and replyToLaterID is exported before the
// sibling it names.
func insertReplyTargetFixture(t *testing.T, fixture exportFixture) {
	t.Helper()
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', '一条有对话的动态', 1, 1, 1, '0123456789abcdef0123456789abcdef')`, threadEntryID); err != nil {
			return err
		}
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms)
VALUES (?, ?, NULL, 'Owner', '根评论', 1)`, threadRootID, threadEntryID); err != nil {
			return err
		}
		// Written in answering order so the insert guard sees each target, then
		// exported in canonical order, which is a different order.
		for _, reply := range []struct{ id, target, body string }{
			{replyToRootID, threadRootID, "回复根评论"},
			{replyToEarlierID, replyToRootID, "回复更早的同级"},
			{replyToLaterID, replyToEarlierID, "回复更晚的同级"},
		} {
			if _, err := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_name, reply_to_comment_id, content_md, created_at_ms)
VALUES (?, ?, ?, 'Owner', ?, ?, 5)`, reply.id, threadEntryID, threadRootID, reply.target, reply.body); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// commentLineOrder maps each comment ID to its position in the JSONL member.
func commentLineOrder(t *testing.T, data []byte) map[string]int {
	t.Helper()
	result := make(map[string]int)
	for index, line := range nonemptyLines(data) {
		var record struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		result[record.ID] = index
	}
	return result
}

func insertCommentAuthorFixture(t *testing.T, fixture exportFixture) {
	t.Helper()
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', '带评论的动态', 1, 1, 1, '0123456789abcdef0123456789abcdef')`, authorAccountEntryID); err != nil {
			return err
		}
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_name, author_user_id, content_md, created_at_ms)
VALUES (?, ?, NULL, 'Owner', ?, '站长的评论', 1)`, authorAccountCommentID, authorAccountEntryID, commentAuthorAccount); err != nil {
			return err
		}
		_, err := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_name, author_user_id, content_md, created_at_ms)
VALUES (?, ?, NULL, '访客', NULL, '匿名访客留下的评论', 2)`, legacyGuestCommentID, authorAccountEntryID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// decodeCommentLines indexes the raw exported comment records by ID so a test
// can assert on the keys a record does and does not carry.
func decodeCommentLines(t *testing.T, data []byte) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any)
	for _, line := range nonemptyLines(data) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		id, _ := record["id"].(string)
		if id == "" {
			t.Fatalf("comment record without an ID: %s", line)
		}
		result[id] = record
	}
	return result
}

// stagedArchiveComments reads every comment back out of the staging generation
// an inspection produced, in canonical archive order.
func stagedArchiveComments(t *testing.T, fixture exportFixture, result Inspection) []sqlitestore.ArchiveComment {
	t.Helper()
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{
		Root: fixture.runtime.Root, Instance: fixture.runtime.Instance,
		Current: stagingGeneration(stageRoot, result.GenerationID), Locks: fixture.runtime.Locks,
	}
	stagedDatabase, err := sqlitestore.Open(context.Background(), stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stagedDatabase.Close() }()
	snapshot, err := stagedDatabase.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var comments []sqlitestore.ArchiveComment
	if err := snapshot.Comments(context.Background(), func(value sqlitestore.ArchiveComment) error {
		comments = append(comments, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return comments
}

func TestInspectAndPlanRejectChecksumValidUnreachableArchiveMedia(t *testing.T) {
	source := newExportFixture(t, true)
	validPath := exportFixtureFile(t, source)
	validBytes, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	members, order := readArchive(t, validBytes)
	lines := nonemptyLines(members["data/entries.jsonl"])
	var entries []byte
	for _, line := range lines {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		value["media"] = []any{}
		entries = append(entries, mustMarshalTest(t, value)...)
		entries = append(entries, '\n')
	}
	members["data/entries.jsonl"] = entries
	commentLines := nonemptyLines(members["data/comments.jsonl"])
	var comments []byte
	for _, line := range commentLines {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		value["media"] = []any{}
		comments = append(comments, mustMarshalTest(t, value)...)
		comments = append(comments, '\n')
	}
	members["data/comments.jsonl"] = comments
	var site map[string]any
	if err := json.Unmarshal(members["data/site.json"], &site); err != nil {
		t.Fatal(err)
	}
	site["avatar_media_id"] = nil
	site["cover_media_id"] = nil
	members["data/site.json"] = append(mustMarshalTest(t, site), '\n')
	setAccountPictures(t, members, nil, nil)
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	orphanPath := filepath.Join(t.TempDir(), "unreachable-media.zip")
	writeArchive(t, orphanPath, members, order)
	if err := ValidateFile(orphanPath); err != nil {
		t.Fatalf("structurally valid orphan fixture error=%v", err)
	}

	for _, mode := range []string{"inspect", "plan"} {
		t.Run(mode, func(t *testing.T) {
			target := newExportFixture(t, false)
			now := time.Date(2026, 8, 11, 1, 10, 0, 0, time.UTC)
			inspector := testInspector(t, target.runtime.Root, target.database, &now, math.MaxUint64)
			var inspectErr error
			if mode == "inspect" {
				_, inspectErr = inspector.InspectFile(context.Background(), orphanPath, testAuthorization(1))
			} else {
				file, err := os.Open(orphanPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = file.Close() }()
				_, inspectErr = inspector.Plan(context.Background(), file, testAuthorization(1))
			}
			if !domain.HasCode(inspectErr, domain.CodeExportInvalid) {
				t.Fatalf("%s orphan authority error=%v", mode, inspectErr)
			}
		})
	}
}

func TestAnotherCredentialCannotDiscardAnUnexpiredStage(t *testing.T) {
	fixture := newExportFixture(t, false)
	path := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 1, 20, 0, 0, time.UTC)
	inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	first, err := inspector.InspectFile(context.Background(), path, testAuthorization(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.InspectFile(context.Background(), path, testAuthorization(2)); !domain.HasCode(err, domain.CodeImportInProgress) {
		t.Fatalf("another credential discarded staged work: %v", err)
	}
	if _, err := inspector.Resume(context.Background(), first.StagingToken, testAuthorization(1)); err != nil {
		t.Fatalf("initiating credential lost staged work: %v", err)
	}
}

func TestInspectOfflineSnapshotLeavesActiveGenerationByteForByteUntouched(t *testing.T) {
	source := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, source)
	target, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	targetDatabase, err := sqlitestore.Open(context.Background(), target, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-journal", "-shm", "-wal"} {
		if _, err := os.Lstat(target.Current.DatabasePath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("offline target has sidecar %s before inspect: %v", suffix, err)
		}
	}
	before := fingerprintGeneration(t, target.Current.Root)
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	if _, err := testInspector(t, target.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, testAuthorization(1)); err != nil {
		t.Fatal(err)
	}
	after := fingerprintGeneration(t, target.Current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("active generation changed during offline inspect:\nbefore=%#v\nafter=%#v", before, after)
	}
	for _, suffix := range []string{"-journal", "-shm", "-wal"} {
		if _, err := os.Lstat(target.Current.DatabasePath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("offline inspect created sidecar %s: %v", suffix, err)
		}
	}
}

func TestInspectOfflineSnapshotIncludesUncleanWALWithoutTouchingActive(t *testing.T) {
	if os.Getenv("MOMENT_IMPORT_WAL_HELPER") == "1" {
		runUncleanImportWALWriter()
		return
	}
	source := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, source)
	target, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	targetDatabase, err := sqlitestore.Open(context.Background(), target, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := sqlitestore.NewAccountRepository(targetDatabase, target.Locks)
	accounts, _ := app.NewAccountService(repo, app.WithAccountIDGenerator(func() (string, error) { return fixtureAccountID, nil }))
	if _, err := accounts.InitializeSiteAccount(context.Background(), "writer"); err != nil {
		t.Fatal(err)
	}
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestInspectOfflineSnapshotIncludesUncleanWALWithoutTouchingActive$")
	command.Env = append(os.Environ(), "MOMENT_IMPORT_WAL_HELPER=1", "MOMENT_IMPORT_WAL_DATA_DIR="+target.Root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unclean WAL writer failed: %v\n%s", err, output)
	}
	walInfo, err := os.Stat(target.Current.DatabasePath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("unclean writer did not leave a non-empty WAL: %v", err)
	}
	before := fingerprintGeneration(t, target.Current.Root)
	now := time.Date(2026, 8, 11, 1, 31, 0, 0, time.UTC)
	result, err := testInspector(t, target.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, testAuthorization(1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacement.Counts.Entries != 1 {
		t.Fatalf("replacement entries = %d, want committed WAL entry", result.Replacement.Counts.Entries)
	}
	after := fingerprintGeneration(t, target.Current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("active generation changed while reading unclean WAL:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func runUncleanImportWALWriter() {
	ctx := context.Background()
	runtimeState, err := runtimefs.Open(os.Getenv("MOMENT_IMPORT_WAL_DATA_DIR"))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	err = database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(ctx, `INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'committed only in WAL', 1, 1, 1, '0123456789abcdef0123456789abcdef')`)
		return err
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type generationFingerprint struct {
	Mode      os.FileMode
	Size      int64
	Modified  int64
	Digest    [sha256.Size]byte
	IsRegular bool
}

func fingerprintGeneration(t *testing.T, root string) map[string]generationFingerprint {
	t.Helper()
	result := make(map[string]generationFingerprint)
	err := filepath.Walk(root, func(name string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		fingerprint := generationFingerprint{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime().UnixNano(), IsRegular: info.Mode().IsRegular()}
		if fingerprint.IsRegular {
			contents, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			fingerprint.Digest = sha256.Sum256(contents)
		}
		result[filepath.ToSlash(relative)] = fingerprint
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInspectRejectsMismatchedExpiredAndCorruptRestartState(t *testing.T) {
	t.Run("owner epoch mismatch", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		result, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(3))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.Resume(context.Background(), result.StagingToken, testAuthorization(4)); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
			t.Fatalf("mismatch error = %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		result, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(3))
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(defaultInspectionTTL)
		if _, err := inspector.Resume(context.Background(), result.StagingToken, testAuthorization(3)); !domain.HasCode(err, domain.CodeImportStagingExpired) {
			t.Fatalf("expiry error = %v", err)
		}
	})

	t.Run("corrupt metadata", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		result, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(3))
		if err != nil {
			t.Fatal(err)
		}
		metadataPath := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID, "staging.json")
		if err := os.WriteFile(metadataPath, []byte("{broken\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.Resume(context.Background(), result.StagingToken, testAuthorization(3)); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
			t.Fatalf("corrupt error = %v", err)
		}
	})
}

func TestInspectRejectsHostileArchivesBeforeReadyToken(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, map[string][]byte, *[]string)
		code   domain.Code
	}{
		{name: "traversal", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members["../escape"] = []byte("bad")
			*order = append(*order, "../escape")
		}, code: domain.CodeExportInvalid},
		{name: "absolute", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members["/absolute"] = []byte("bad")
			*order = append(*order, "/absolute")
		}, code: domain.CodeExportInvalid},
		{name: "windows volume", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members["C:/escape"] = []byte("bad")
			*order = append(*order, "C:/escape")
		}, code: domain.CodeExportInvalid},
		{name: "backslash traversal", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members[`..\escape`] = []byte("bad")
			*order = append(*order, `..\escape`)
		}, code: domain.CodeExportInvalid},
		{name: "unknown version", mutate: func(t *testing.T, members map[string][]byte, _ *[]string) {
			var value map[string]any
			if err := json.Unmarshal(members["manifest.json"], &value); err != nil {
				t.Fatal(err)
			}
			value["format_version"] = float64(99)
			members["manifest.json"] = append(mustMarshalTest(t, value), '\n')
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
		}, code: domain.CodeExportVersionUnsupported},
		{name: "bad checksum", mutate: func(_ *testing.T, members map[string][]byte, _ *[]string) {
			members["checksums.sha256"] = append([]byte(nil), members["checksums.sha256"]...)
			if members["checksums.sha256"][0] == '0' {
				members["checksums.sha256"][0] = '1'
			} else {
				members["checksums.sha256"][0] = '0'
			}
		}, code: domain.CodeExportChecksumMismatch},
		{name: "malformed jsonl", mutate: func(t *testing.T, members map[string][]byte, _ *[]string) {
			members["data/entries.jsonl"] = []byte("{broken}\n")
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
		}, code: domain.CodeExportInvalid},
		{name: "invalid media bytes", mutate: func(t *testing.T, members map[string][]byte, _ *[]string) {
			for name := range members {
				if strings.HasPrefix(name, "media/") {
					members[name] = []byte("not an image")
					break
				}
			}
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
		}, code: domain.CodeExportInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExportFixture(t, true)
			valid := exportFixtureBytes(t, fixture)
			members, order := readArchive(t, valid)
			test.mutate(t, members, &order)
			name := filepath.Join(t.TempDir(), "hostile.zip")
			writeArchive(t, name, members, order)
			now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
			inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
			if _, err := inspector.InspectFile(context.Background(), name, testAuthorization(1)); !domain.HasCode(err, test.code) {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
			assertNoReadyStaging(t, fixture.runtime.Root)
		})
	}
}

func TestInspectRejectsDuplicateSymlinkRatioQuotaDiskAndCancellation(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		name := filepath.Join(t.TempDir(), "duplicate.zip")
		writeArchiveHeaders(t, name, append(order, "README.txt"), members, nil)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		if _, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), name, testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("duplicate error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		name := filepath.Join(t.TempDir(), "symlink.zip")
		writeArchiveHeaders(t, name, order, members, map[string]os.FileMode{"README.txt": os.ModeSymlink | 0o777})
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		if _, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), name, testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("symlink error = %v", err)
		}
	})

	t.Run("compression ratio", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		members["README.txt"] = bytes.Repeat([]byte{'A'}, 1<<20)
		name := filepath.Join(t.TempDir(), "ratio.zip")
		writeArchive(t, name, members, order)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{9}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxCompression: 2}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), name, testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("ratio error = %v", err)
		}
	})

	t.Run("manifest quota", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		setManifestCount(t, members, "entries", 2)
		rebuildManifestExpanded(t, members)
		rebuildChecksums(members)
		name := filepath.Join(t.TempDir(), "quota.zip")
		writeArchive(t, name, members, order)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{8}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxEntries: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), name, testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("quota error = %v", err)
		}
	})

	t.Run("compressed size quota", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{8}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxArchiveBytes: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("compressed size error = %v", err)
		}
	})

	t.Run("expanded size quota", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{8}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxExpandedBytes: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("expanded size error = %v", err)
		}
	})

	t.Run("disk", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, 0)
		_, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(1))
		if !domain.HasCode(err, domain.CodeImportDiskSpaceInsufficient) {
			t.Fatalf("disk error = %v", err)
		}
		assertNoReadyStaging(t, fixture.runtime.Root)
	})

	t.Run("cancel", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		ctx, cancel := context.WithCancel(context.Background())
		reader := &cancelAfterReader{reader: bytes.NewReader(exportFixtureBytes(t, fixture)), cancel: cancel}
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		_, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).Inspect(ctx, reader, testAuthorization(1))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
		assertNoReadyStaging(t, fixture.runtime.Root)
	})
}

func TestImportPlanCleansTemporaryStagingOnSuccessInvalidAndCancellation(t *testing.T) {
	fixture := newExportFixture(t, true)
	now := time.Date(2026, 8, 11, 4, 30, 0, 0, time.UTC)
	inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	plan, err := inspector.Plan(context.Background(), bytes.NewReader(exportFixtureBytes(t, fixture)), testAuthorization(1))
	if err != nil || !plan.DryRun || plan.ArchiveSHA256 == "" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	assertImportStagingEmpty(t, fixture.runtime.Root)
	if _, err := inspector.Plan(context.Background(), strings.NewReader("not a zip"), testAuthorization(1)); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("invalid plan error=%v", err)
	}
	assertImportStagingEmpty(t, fixture.runtime.Root)
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterReader{reader: bytes.NewReader(exportFixtureBytes(t, fixture)), cancel: cancel}
	if _, err := inspector.Plan(ctx, reader, testAuthorization(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled plan error=%v", err)
	}
	assertImportStagingEmpty(t, fixture.runtime.Root)
}

func TestResumeRejectsDuplicateFTSAndExtraGenerationPath(t *testing.T) {
	t.Run("duplicate FTS", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 35, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		inspection, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(1))
		if err != nil {
			t.Fatal(err)
		}
		databasePath := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID, "generation", "moment.db")
		if err := mutateStagedDatabase(databasePath, `INSERT INTO entry_fts (entry_id, content_text) SELECT entry_id, content_text FROM entry_fts LIMIT 1`); err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.Resume(context.Background(), inspection.StagingToken, testAuthorization(1)); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
			t.Fatalf("duplicate FTS resume error=%v", err)
		}
	})
	t.Run("orphan tree path", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 36, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		inspection, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), testAuthorization(1))
		if err != nil {
			t.Fatal(err)
		}
		orphan := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID, "generation", "media", "original", "ff", "orphan.png")
		if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.Resume(context.Background(), inspection.StagingToken, testAuthorization(1)); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
			t.Fatalf("orphan path resume error=%v", err)
		}
	})
}

func TestLiveInspectorFailsFastBehindCommitCoordinator(t *testing.T) {
	fixture := newExportFixture(t, false)
	inspector, err := NewInspector(InspectOptions{
		DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, ImportLockWait: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	held, err := fixture.runtime.Locks.AcquireSet(context.Background(), runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	started := time.Now()
	_, err = inspector.Inspect(context.Background(), bytes.NewReader(exportFixtureBytes(t, fixture)), testAuthorization(1))
	if !domain.HasCode(err, domain.CodeImportInProgress) || time.Since(started) > time.Second {
		t.Fatalf("waiting live inspect error=%v elapsed=%s", err, time.Since(started))
	}
	assertImportStagingEmpty(t, fixture.runtime.Root)
}

func testInspector(t *testing.T, dataDir string, database *sqlitestore.Database, now *time.Time, available uint64) *Inspector {
	t.Helper()
	inspector, err := NewInspector(InspectOptions{
		DataDir: dataDir, ActiveDatabase: database, Clock: func() time.Time { return *now },
		Random:         bytes.NewReader(bytes.Repeat([]byte{7}, 256)),
		AvailableBytes: func(string) (uint64, error) { return available, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return inspector
}

func exportFixtureBytes(t *testing.T, fixture exportFixture) []byte {
	t.Helper()
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func exportFixtureFile(t *testing.T, fixture exportFixture) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "portable.zip")
	if err := os.WriteFile(name, exportFixtureBytes(t, fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func assertNoReadyStaging(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "staging", "import"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		metadata, err := readStagingMetadata(filepath.Join(root, "staging", "import", entry.Name()))
		if err == nil && metadata.State == stagingReady {
			t.Fatalf("unexpected ready staging %s", entry.Name())
		}
	}
}

func assertImportStagingEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "staging", "import"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry-run left import staging entries: %#v", entries)
	}
}

func writeArchiveHeaders(t *testing.T, name string, order []string, members map[string][]byte, modes map[string]os.FileMode) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, memberName := range order {
		header := &zip.FileHeader{Name: memberName, Method: zip.Deflate}
		if mode, exists := modes[memberName]; exists {
			header.SetMode(mode)
		} else {
			header.SetMode(0o644)
		}
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(members[memberName]); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(writer.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestFinishClaimRejectsLeaseFromAnotherInstance(t *testing.T) {
	first, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := NewInspector(InspectOptions{DataDir: second.Root})
	if err != nil {
		t.Fatal(err)
	}
	held, err := first.Locks.AcquireSet(context.Background(), runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	id := "import-20260811T000000000Z-0011223344556677"
	claim := StagingClaim{GenerationID: id, StageRoot: filepath.Join(second.Root, "staging", "import", id)}
	if err := inspector.FinishClaim(claim, false, held); !errors.Is(err, runtimefs.ErrInsufficientLocks) {
		t.Fatalf("cross-instance claim finalization error=%v", err)
	}
}

type cancelAfterReader struct {
	reader io.Reader
	cancel context.CancelFunc
	done   bool
}

func (reader *cancelAfterReader) Read(data []byte) (int, error) {
	read, err := reader.reader.Read(data)
	if !reader.done {
		reader.done = true
		reader.cancel()
	}
	return read, err
}

// TestLinkCardRoundTripsThroughPortableArchive proves a captured card survives
// export and import intact, including its thumbnail. The thumbnail is named by
// content hash in the archive because media identifiers are reassigned on
// import, so this also proves the hash is resolved back to a real reference.
func TestLinkCardRoundTripsThroughPortableArchive(t *testing.T) {
	const cardEntryID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	fixture := newExportFixture(t, true)
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', '看看这个视频', 1, 1, 1, '0123456789abcdef0123456789abcdef')`, cardEntryID); err != nil {
			return err
		}
		_, err := queries.ExecContext(context.Background(), `
INSERT INTO entry_link_card (
    entry_id, position, source_url, canonical_url, provider, card_kind, title,
    description, site_name, author_name, duration_seconds, thumbnail_media_id,
    embed_url, captured_at_ms
) VALUES (?, 0, 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
          'https://www.youtube.com/watch?v=dQw4w9WgXcQ', 'youtube', 'video',
          'A captured title', 'A captured description', 'YouTube', 'A channel',
          212, ?, 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ', 1700000000000)`,
			cardEntryID, mediaOneID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 16, 1, 2, 3, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(),
		exportFixtureFile(t, fixture),
		testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{
		Root: fixture.runtime.Root, Instance: fixture.runtime.Instance,
		Current: stagingGeneration(stageRoot, result.GenerationID), Locks: fixture.runtime.Locks,
	}
	stagedDatabase, err := sqlitestore.Open(context.Background(), stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stagedDatabase.Close() }()
	snapshot, err := stagedDatabase.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()

	var restored *sqlitestore.ArchiveEntry
	if err := snapshot.Entries(context.Background(), func(value sqlitestore.ArchiveEntry) error {
		if value.ID == cardEntryID {
			clone := value
			restored = &clone
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if restored == nil || len(restored.LinkCards) != 1 {
		t.Fatalf("restored entry = %#v", restored)
	}
	card := restored.LinkCards[0]
	if card.Card.Title != "A captured title" || card.Card.Description != "A captured description" {
		t.Fatalf("card text did not survive: %#v", card.Card)
	}
	if card.Card.Provider != domain.LinkProviderYouTube || card.Card.Kind != domain.LinkKindVideo {
		t.Fatalf("card identity did not survive: %#v", card.Card)
	}
	if card.Card.DurationSeconds == nil || *card.Card.DurationSeconds != 212 {
		t.Fatalf("card duration did not survive: %#v", card.Card.DurationSeconds)
	}
	if card.Card.EmbedURL != "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" {
		t.Fatalf("embed URL did not survive: %q", card.Card.EmbedURL)
	}
	if card.ThumbnailSHA256 != fixture.mediaOneSHA {
		t.Fatalf("thumbnail hash = %q want %q", card.ThumbnailSHA256, fixture.mediaOneSHA)
	}
	// The hash must have been resolved back to a live managed media row, not
	// silently dropped.
	var thumbnailID string
	if err := stagedDatabase.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(),
			`SELECT thumbnail_media_id FROM entry_link_card WHERE entry_id = ?`, cardEntryID).Scan(&thumbnailID)
	}); err != nil {
		t.Fatal(err)
	}
	if thumbnailID == "" {
		t.Fatal("thumbnail reference was not restored")
	}
	// The restored entry is indexed under its Markdown and its card together,
	// as the write path indexes it. An import that normalized the Markdown
	// alone left every carded entry reading as drifted after a restore.
	var contentText string
	if err := stagedDatabase.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(),
			`SELECT content_text FROM entries WHERE id = ?`, cardEntryID).Scan(&contentText)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contentText, "a captured title") || !strings.Contains(contentText, "youtube.com") {
		t.Fatalf("restored search text = %q, want the card's title and host in it", contentText)
	}
}

func TestValidateFileRejectsReplyTargetOutsideTheConversation(t *testing.T) {
	fixture := newExportFixture(t, true)
	insertReplyTargetFixture(t, fixture)
	archiveBytes := exportFixtureBytes(t, fixture)
	if err := ValidateFile(writeArchiveBytes(t, archiveBytes)); err != nil {
		t.Fatalf("unmodified fixture archive was rejected: %v", err)
	}
	for name, target := range map[string]string{
		// replyID is a reply under a different entry, so it fails the same
		// same-conversation rule the database trigger applies.
		"another entry": replyID,
		// A target the archive never carries would reach staging as a
		// foreign-key failure instead of a rejection.
		"absent comment": "01ARZ3NDEKTSV4RRFFQ69G5FD9",
	} {
		t.Run(name, func(t *testing.T) {
			members, order := readArchive(t, archiveBytes)
			rewritten := make([]byte, 0, len(members["data/comments.jsonl"]))
			retargeted := 0
			for _, line := range nonemptyLines(members["data/comments.jsonl"]) {
				var record map[string]any
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if record["id"] == replyToRootID {
					record["reply_to_comment_id"] = target
					retargeted++
				}
				rewritten = append(append(rewritten, mustMarshalTest(t, record)...), '\n')
			}
			if retargeted != 1 {
				t.Fatalf("retargeted %d records, want 1", retargeted)
			}
			members["data/comments.jsonl"] = rewritten
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			hostile := filepath.Join(t.TempDir(), "cross-conversation-reply.zip")
			writeArchive(t, hostile, members, order)
			if err := ValidateFile(hostile); !domain.HasCode(err, domain.CodeExportInvalid) {
				t.Fatalf("cross-conversation reply target = %v", err)
			}
		})
	}
}

func writeArchiveBytes(t *testing.T, data []byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "portable.zip")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}
