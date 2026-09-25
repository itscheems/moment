package sqlite

import (
	"context"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func mediaVisibility(t *testing.T, db *Database, mediaID string) domain.Visibility {
	t.Helper()
	repository, err := NewMediaReferenceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.ParseID(mediaID)
	if err != nil {
		t.Fatal(err)
	}
	visibility, err := repository.Visibility(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return visibility
}

func mediaIsReferenced(t *testing.T, db *Database, mediaID string) bool {
	t.Helper()
	var referenced int
	if err := db.ReadTx(context.Background(), func(queries Queries) error {
		return queries.QueryRowContext(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM media WHERE media.id = ? AND `+mediaReferencedSQL+`)`,
			mediaID).Scan(&referenced)
	}); err != nil {
		t.Fatal(err)
	}
	return referenced == 1
}

// A cover is the one article picture that does not travel through entry_media,
// so it needs its own branch in both halves of the resolver. Missing it from
// the reference half hands the file to the collector; missing it from the
// public half makes a published article's cover unreadable.
func TestArticleCoverIsReferencedAndFollowsTheArticleVisibility(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	_, cover := seedArticleEntry(t, db)
	coverID, err := domain.ParseID(cover)
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("公开文章",
		articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"正文"}]}`), &coverID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}

	if !mediaIsReferenced(t, db, cover) {
		t.Fatal("an article cover must count as a reference, or the collector will delete it")
	}
	if got := mediaVisibility(t, db, cover); got != domain.VisibilityEveryone {
		t.Fatalf("a public article's cover must be readable, got %q", got)
	}

	mustWrite(t, db, `UPDATE entries SET visibility = 'only_me' WHERE id = ?`, articleEntryID)
	if got := mediaVisibility(t, db, cover); got != domain.VisibilityOnlyMe {
		t.Fatalf("a hidden article's cover must not be public, got %q", got)
	}
}

func TestArticleBodyPicturesFollowTheArticleVisibility(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	inline, _ := seedArticleEntry(t, db)
	article, err := domain.NewArticle("隐藏文章",
		articleTestBody(t, `{"type":"image","attrs":{"media_id":"`+inline+`"}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}
	if got := mediaVisibility(t, db, inline); got != domain.VisibilityEveryone {
		t.Fatalf("a public article's picture must be readable, got %q", got)
	}
	mustWrite(t, db, `UPDATE entries SET visibility = 'only_me' WHERE id = ?`, articleEntryID)
	if got := mediaVisibility(t, db, inline); got != domain.VisibilityOnlyMe {
		t.Fatalf("a hidden article's picture must not be public, got %q", got)
	}
}

// A draft keeps its uploads alive without publishing them. Both halves of that
// sentence are the test.
func TestDraftPicturesAreKeptAliveButNeverPublic(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	inline := articleMediaID(t, db, "d")
	cover := articleMediaID(t, db, "e")
	coverID, err := domain.ParseID(cover)
	if err != nil {
		t.Fatal(err)
	}
	body, err := domain.ParseArticleDraftDocument(
		[]byte(`{"schema_version":1,"type":"doc","content":[{"type":"image","attrs":{"media_id":"` + inline + `"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := domain.NewArticleDraft("草稿", body, &coverID)
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := domain.ParseID(draftOneID)
	if err != nil {
		t.Fatal(err)
	}
	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(context.Background(), recordFor(draftID, draft))
	}); err != nil {
		t.Fatal(err)
	}

	for _, mediaID := range []string{inline, cover} {
		if !mediaIsReferenced(t, db, mediaID) {
			t.Fatalf("a draft upload must not look like an orphan: %s", mediaID)
		}
		if got := mediaVisibility(t, db, mediaID); got != domain.VisibilityOnlyMe {
			t.Fatalf("a draft is not published, so %s must not be public, got %q", mediaID, got)
		}
	}

	// Once the draft is gone the files are ordinary orphans again.
	if err := withPostStore(t, db, true, func(store postStore) error {
		_, err := store.DeleteArticleDraft(context.Background(), draftID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, mediaID := range []string{inline, cover} {
		if mediaIsReferenced(t, db, mediaID) {
			t.Fatalf("%s should be unreferenced once its draft is gone", mediaID)
		}
	}
}

// A file that a draft holds and a public article also uses stays public: the
// resolver answers "is anything public pointing at this", not "is everything".
func TestAPictureSharedByADraftAndAPublicArticleStaysPublic(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	shared, _ := seedArticleEntry(t, db)
	article, err := domain.NewArticle("公开文章",
		articleTestBody(t, `{"type":"image","attrs":{"media_id":"`+shared+`"}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}
	body, err := domain.ParseArticleDraftDocument(
		[]byte(`{"schema_version":1,"type":"doc","content":[{"type":"image","attrs":{"media_id":"` + shared + `"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := domain.NewArticleDraft("也用这张", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := domain.ParseID(draftOneID)
	if err != nil {
		t.Fatal(err)
	}
	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(context.Background(), recordFor(draftID, draft))
	}); err != nil {
		t.Fatal(err)
	}
	if got := mediaVisibility(t, db, shared); got != domain.VisibilityEveryone {
		t.Fatalf("a public article still uses it, got %q", got)
	}
}

func TestEntryMediaIDsIncludesTheArticleCover(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	inline, cover := seedArticleEntry(t, db)
	coverID, err := domain.ParseID(cover)
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("带封面",
		articleTestBody(t, `{"type":"image","attrs":{"media_id":"`+inline+`"}}`), &coverID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}
	entryID, err := domain.ParseID(articleEntryID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []domain.ID
	if err := db.ReadTx(context.Background(), func(queries Queries) error {
		var readErr error
		ids, readErr = mediaReferenceStore{queries: queries}.EntryMediaIDs(context.Background(), entryID)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("the cover belongs to the entry's media too, got %v", ids)
	}
}
