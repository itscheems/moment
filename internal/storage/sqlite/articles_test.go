package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const (
	articleEntryID = "01ARZ3NDEKTSV4RRFFQ69G5FE9"
	momentEntryID  = "01ARZ3NDEKTSV4RRFFQ69G5FEA"
)

func articleTestBody(t *testing.T, content string) domain.ArticleDocument {
	t.Helper()
	document, err := domain.ParseArticleDocument([]byte(`{"schema_version":1,"type":"doc","content":[` + content + `]}`))
	if err != nil {
		t.Fatalf("parse body: %v", err)
	}
	return document
}

func articleImage(mediaID, alt string) string {
	if alt == "" {
		return `{"type":"image","attrs":{"media_id":"` + mediaID + `"}}`
	}
	return `{"type":"image","attrs":{"media_id":"` + mediaID + `","alt":"` + alt + `"}}`
}

// seedArticleEntry installs the timeline envelope an article body hangs from,
// plus two media objects for its pictures to point at.
func seedArticleEntry(t *testing.T, db *Database) (string, string) {
	t.Helper()
	first, second := articleMediaID(t, db, "a"), articleMediaID(t, db, "b")
	mustWrite(t, db, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES (?, 'article', NULL, 1, 1, 1, '')`, articleEntryID)
	mustWrite(t, db, entryInsertSQL, momentEntryID, "moment", "一条普通帖子", int64(1), int64(1), int64(1))
	return first, second
}

func articleMediaID(t *testing.T, db *Database, fill string) string {
	t.Helper()
	sha := strings.Repeat(fill, 64)
	id := "01ARZ3NDEKTSV4RRFFQ69G5F" + strings.ToUpper(fill) + "1"
	mustWrite(t, db, `
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, 'p.jpg', 'image/jpeg', 1024, 10, 10, 1, NULL)`,
		id, sha, "media/original/"+sha[:2]+"/"+sha+".jpg")
	return id
}

func writeArticle(t *testing.T, db *Database, article domain.Article, replace bool) error {
	t.Helper()
	id, err := domain.ParseID(articleEntryID)
	if err != nil {
		t.Fatal(err)
	}
	return db.WriteTx(context.Background(), func(queries Queries) error {
		store := newPostStore(queries)
		if replace {
			return store.ReplaceArticle(context.Background(), id, article)
		}
		return store.InsertArticle(context.Background(), id, article)
	})
}

func articleMediaRows(t *testing.T, db *Database, entryID string) []string {
	t.Helper()
	var rows []string
	if err := db.ReadTx(context.Background(), func(queries Queries) error {
		result, err := queries.QueryContext(context.Background(), `
SELECT position, media_id, coalesce(alt_text, '<none>') FROM entry_media WHERE entry_id = ? ORDER BY position`, entryID)
		if err != nil {
			return err
		}
		defer func() { _ = result.Close() }()
		for result.Next() {
			var position int
			var mediaID, alt string
			if err := result.Scan(&position, &mediaID, &alt); err != nil {
				return err
			}
			rows = append(rows, mediaID+"@"+alt)
		}
		return result.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestArticleStorageProjectsBodyPicturesAndReadsBack(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	first, second := seedArticleEntry(t, db)

	cover, err := domain.ParseID(second)
	if err != nil {
		t.Fatal(err)
	}
	body := articleTestBody(t, strings.Join([]string{
		`{"type":"paragraph","content":[{"type":"text","text":"开头的话"}]}`,
		articleImage(first, "第一张"),
		`{"type":"paragraph","content":[{"type":"text","text":"中间"}]}`,
		articleImage(second, ""),
		articleImage(first, "又是第一张"),
	}, ","))
	article, err := domain.NewArticle("一篇文章", body, &cover)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatalf("insert: %v", err)
	}

	want := []string{first + "@第一张", second + "@<none>", first + "@又是第一张"}
	if rows := articleMediaRows(t, db, articleEntryID); !equalStrings(rows, want) {
		t.Fatalf("projection = %v, want %v", rows, want)
	}

	id, _ := domain.ParseID(articleEntryID)
	var stored domain.Article
	if err := db.ReadTx(context.Background(), func(queries Queries) error {
		read, found, readErr := newPostStore(queries).ArticleByEntryID(context.Background(), id)
		if readErr != nil || !found {
			t.Fatalf("read back: found=%v err=%v", found, readErr)
		}
		stored = read
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stored.Title != "一篇文章" || stored.CoverID == nil || stored.CoverID.String() != second {
		t.Fatalf("stored article = %+v", stored)
	}
	if stored.Excerpt() != "开头的话 中间" {
		t.Fatalf("excerpt = %q", stored.Excerpt())
	}
	if len(stored.InlineMedia()) != 3 {
		t.Fatalf("round-tripped body lost pictures: %d", len(stored.InlineMedia()))
	}
}

// The projection is derived, so editing the body has to rewrite it whole.
func TestArticleStorageRecomputesTheProjectionOnEveryEdit(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	first, second := seedArticleEntry(t, db)

	body := articleTestBody(t, strings.Join([]string{
		articleImage(first, ""), articleImage(second, ""), articleImage(first, ""),
	}, ","))
	article, err := domain.NewArticle("三张图", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}

	// Remove the middle picture; positions must close up rather than leave a gap.
	edited, err := domain.NewArticle("两张图",
		articleTestBody(t, articleImage(first, "")+","+articleImage(first, "留下的")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, edited, true); err != nil {
		t.Fatalf("replace: %v", err)
	}
	want := []string{first + "@<none>", first + "@留下的"}
	if rows := articleMediaRows(t, db, articleEntryID); !equalStrings(rows, want) {
		t.Fatalf("projection after edit = %v, want %v", rows, want)
	}
}

func TestArticleStorageHoldsThePictureLimitsOfEachKind(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	first, _ := seedArticleEntry(t, db)

	images := make([]string, 0, domain.MaxArticleInlineImages+1)
	for range domain.MaxArticleInlineImages {
		images = append(images, articleImage(first, ""))
	}
	full, err := domain.NewArticle("满图", articleTestBody(t, strings.Join(images, ",")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, full, false); err != nil {
		t.Fatalf("fifty pictures must be accepted: %v", err)
	}

	// A moment is still nine, now that the column no longer says so.
	momentID, err := domain.ParseID(momentEntryID)
	if err != nil {
		t.Fatal(err)
	}
	mediaID, err := domain.ParseID(first)
	if err != nil {
		t.Fatal(err)
	}
	usages := make([]domain.EntryMediaUsage, 0, domain.MaxEntryMediaUsages+1)
	for index := range domain.MaxEntryMediaUsages + 1 {
		usages = append(usages, domain.EntryMediaUsage{MediaID: mediaID, Position: index})
	}
	err = db.WriteTx(context.Background(), func(queries Queries) error {
		return newPostStore(queries).ReplaceEntryMediaUsages(context.Background(), momentID, usages)
	})
	if err == nil {
		t.Fatal("a moment must still be held to nine pictures")
	}
}

func TestArticleCardsCarryTheListFieldsAndNotTheBody(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	_, second := seedArticleEntry(t, db)
	cover, err := domain.ParseID(second)
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("卡片标题",
		articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"卡片摘要"}]}`), &cover)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}

	id, _ := domain.ParseID(articleEntryID)
	momentID, _ := domain.ParseID(momentEntryID)
	var cards map[domain.ID]app.ArticleCard
	if err := db.ReadTx(context.Background(), func(queries Queries) error {
		read, readErr := newPostStore(queries).ArticleCards(context.Background(), []domain.ID{id, momentID})
		cards = read
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("a moment has no article card: %+v", cards)
	}
	card := cards[id]
	if card.Title != "卡片标题" || card.Excerpt != "卡片摘要" || card.CoverMediaID == nil || card.CoverMediaID.String() != second {
		t.Fatalf("card = %+v", card)
	}
}

// The satellite belongs to an article, and the database is what says so.
func TestArticleBodyCannotHangOffAMoment(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	seedArticleEntry(t, db)
	momentID, err := domain.ParseID(momentEntryID)
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("不该存在",
		articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"x"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WriteTx(context.Background(), func(queries Queries) error {
		return newPostStore(queries).InsertArticle(context.Background(), momentID, article)
	})
	if err == nil {
		t.Fatal("a moment must not carry an article body")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
