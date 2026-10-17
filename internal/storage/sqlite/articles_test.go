package sqlite

import (
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
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES (?, 'article', NULL, 1, 1, 1, '', '0123456789abcdef0123456789abcdef')`, articleEntryID)
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
	return db.WriteTx(testAccountContext(), func(queries Queries) error {
		store := newPostStore(queries)
		if replace {
			return store.ReplaceArticle(testAccountContext(), id, article)
		}
		return store.InsertArticle(testAccountContext(), id, article)
	})
}

func articleMediaRows(t *testing.T, db *Database, entryID string) []string {
	t.Helper()
	var rows []string
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		result, err := queries.QueryContext(testAccountContext(), `
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
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		read, found, readErr := newPostStore(queries).ArticleByEntryID(testAccountContext(), id)
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
	err = db.WriteTx(testAccountContext(), func(queries Queries) error {
		return newPostStore(queries).ReplaceEntryMediaUsages(testAccountContext(), momentID, usages)
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
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		read, readErr := newPostStore(queries).ArticleCards(testAccountContext(), []domain.ID{id, momentID})
		cards = read
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("a moment has no article card: %+v", cards)
	}
	card := cards[id]
	if card.Title != "卡片标题" || card.Excerpt != "卡片摘要..." || card.Cover == nil || card.Cover.MediaID.String() != second {
		t.Fatalf("card = %+v", card)
	}
	// The cover carries geometry so the browser can reserve its space.
	if card.Cover.MIMEType != "image/jpeg" || card.Cover.Width != 10 || card.Cover.Height != 10 {
		t.Fatalf("cover = %+v", card.Cover)
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
	err = db.WriteTx(testAccountContext(), func(queries Queries) error {
		return newPostStore(queries).InsertArticle(testAccountContext(), momentID, article)
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

func readArticleDerivations(t *testing.T, db *Database) []app.ArticleDerivation {
	t.Helper()
	var derivations []app.ArticleDerivation
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		var err error
		derivations, err = newPostStore(queries).ArticleDerivations(testAccountContext())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return derivations
}

// The excerpt a card reads and the entry_media rows reclamation counts are both
// copies taken from the body. This is how a repair finds the ones that no
// longer agree with it -- and how it tells those apart from a body it cannot
// read, which it must leave alone.
func TestArticleDerivationsFindEveryCopyThatLeftItsBodyBehind(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	first, _ := seedArticleEntry(t, db)
	body := articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"正文说了什么"}]},`+articleImage(first, "图"))
	article, err := domain.NewArticle("标题", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// What was just written agrees with itself.
	derivations := readArticleDerivations(t, db)
	if len(derivations) != 1 {
		t.Fatalf("derivations = %#v", derivations)
	}
	if derivations[0].Unparsable || derivations[0].Drifted() {
		t.Fatalf("a freshly written article must not look drifted: %#v", derivations[0])
	}

	// An excerpt left behind by a change to how excerpts are derived.
	mustWrite(t, db, `UPDATE entry_article SET excerpt = '这是上一版的摘要' WHERE entry_id = ?`, articleEntryID)
	derivations = readArticleDerivations(t, db)
	if !derivations[0].Drifted() || derivations[0].StoredExcerpt != "这是上一版的摘要" {
		t.Fatalf("a stale excerpt must be reported: %#v", derivations[0])
	}
	if got := derivations[0].Article.Excerpt(); got != article.Excerpt() {
		t.Fatalf("the body is the authority, so it must still derive %q, got %q", article.Excerpt(), got)
	}

	// A projection lost while the excerpt is correct.
	mustWrite(t, db, `UPDATE entry_article SET excerpt = ? WHERE entry_id = ?`, article.Excerpt(), articleEntryID)
	mustWrite(t, db, `DELETE FROM entry_media WHERE entry_id = ?`, articleEntryID)
	derivations = readArticleDerivations(t, db)
	if !derivations[0].Drifted() || derivations[0].ProjectedMedia != 0 {
		t.Fatalf("a lost picture projection must be reported: %#v", derivations[0])
	}

	// Writing the body back is the repair, and it restores both copies.
	if err := writeArticle(t, db, derivations[0].Article, true); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if rows := articleMediaRows(t, db, articleEntryID); len(rows) != 1 || rows[0] != first+"@图" {
		t.Fatalf("projection after repair = %#v", rows)
	}
	if derivations = readArticleDerivations(t, db); derivations[0].Drifted() {
		t.Fatalf("nothing should drift after a repair: %#v", derivations[0])
	}

	// A body that no longer parses is reported, not returned as an article and
	// not counted as drift: there is nothing to repair it from.
	mustWrite(t, db, `UPDATE entry_article SET body_json = ? WHERE entry_id = ?`,
		`{"schema_version":1,"type":"doc","content":[{"type":"这不是一个节点类型"}]}`, articleEntryID)
	derivations = readArticleDerivations(t, db)
	if !derivations[0].Unparsable || derivations[0].Drifted() {
		t.Fatalf("an unreadable body must be reported and left alone: %#v", derivations[0])
	}
}
