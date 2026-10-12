package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

func TestArticlePreviewReadsBeyondTheLegacyCacheWithoutWritingAuthority(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTestDatabase(t, Options{})
	seedArticleEntry(t, db)
	prose := strings.Repeat("界", 160)
	body := articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"`+prose+`"},{"type":"hardBreak"},{"type":"text","text":"last"}]},{"type":"paragraph","content":[{"type":"text","text":"second"}]}`)
	article, err := domain.NewArticle("完整标题", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}
	id, _ := domain.ParseID(articleEntryID)
	want := prose + "\nlast\nsecond..."

	type authority struct {
		title, body, excerpt, searchText string
		created, updated, published      int64
		edited                           sql.NullInt64
	}
	readAuthority := func() authority {
		t.Helper()
		var state authority
		if err := db.read.QueryRowContext(ctx, `SELECT article.title,article.body_json,article.excerpt,
 entries.content_text,entries.created_at_ms,entries.updated_at_ms,entries.published_at_ms,entries.edited_at_ms
 FROM entry_article AS article JOIN entries ON entries.id=article.entry_id WHERE article.entry_id=?`, id.String()).Scan(
			&state.title, &state.body, &state.excerpt, &state.searchText, &state.created, &state.updated, &state.published, &state.edited); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := readAuthority()
	if before.excerpt != strings.Repeat("界", 140) {
		t.Fatalf("legacy bounded cache changed: %q", before.excerpt)
	}
	if err := db.ReadTx(ctx, func(q Queries) error {
		cards, err := newPostStore(q).ArticleCards(ctx, []domain.ID{id})
		if err != nil {
			return err
		}
		card := cards[id]
		if card.Excerpt != want {
			t.Fatalf("card only read the old prefix: %q", card.Excerpt)
		}
		wire := postdto.FromPost(app.Post{Entry: domain.Entry{ID: id, Kind: domain.KindArticle}, ArticleCard: &card})
		if wire.Article == nil || wire.Article.Excerpt != want || wire.Article.Body != nil || wire.Article.BodySchemaVersion != nil {
			t.Fatalf("list preview leaked its source body: %+v", wire.Article)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if after := readAuthority(); after != before {
		t.Fatalf("preview read changed authority or stored cache: before=%+v after=%+v", before, after)
	}

	// Listing retains its last cached summary if this particular body breaks.
	mustWrite(t, db, `UPDATE entry_article SET body_json='{"schema_version":1,"type":"doc","content":[{"type":"unknown"}]}' WHERE entry_id=?`, id.String())
	if err := db.ReadTx(ctx, func(q Queries) error {
		store := newPostStore(q)
		cards, err := store.ArticleCards(ctx, []domain.ID{id})
		if err != nil || cards[id].Excerpt != before.excerpt {
			t.Fatalf("damaged card lost cached fallback: %+v,%v", cards, err)
		}
		_, _, err = store.ArticleByEntryID(ctx, id)
		var failure *domain.Error
		if !errors.As(err, &failure) || failure.Code != domain.CodeDataIntegrityFailed {
			t.Fatalf("detail stopped reporting its damaged body: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDraftPreviewReadsAreAuthorScopedAndPreserveLegacyMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, service := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	a := app.WithContentActor(ctx, alice)
	b := app.WithContentActor(ctx, bob)
	prose := strings.Repeat("字", 180)
	draft, err := service.SaveArticleDraft(a, app.SaveArticleDraftInput{Title: "draft", BodyJSON: draftBodyJSON(prose)})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.SaveArticleDraft(b, app.SaveArticleDraftInput{Title: "other", BodyJSON: draftBodyJSON("other author")})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, db, `UPDATE article_drafts SET body_json='{"schema_version":1,"type":"doc","content":[{"type":"unknown"}]}' WHERE id=?`, other.ID.String())

	summaries, err := service.ArticleDraftSummaries(a)
	if err != nil || len(summaries) != 1 || summaries[0].ID != draft.ID || summaries[0].Excerpt != prose+"..." {
		t.Fatalf("author-scoped preview = %+v,%v", summaries, err)
	}
	if got := postdto.FromArticleDraft(draft).Excerpt; got != summaries[0].Excerpt {
		t.Fatalf("draft detail disagreed with list: %q", got)
	}
	var legacy, title, raw string
	var created, updated int64
	if err := db.read.QueryRowContext(ctx, `SELECT excerpt,title,body_json,created_at_ms,updated_at_ms FROM article_drafts WHERE id=?`, draft.ID.String()).Scan(
		&legacy, &title, &raw, &created, &updated); err != nil {
		t.Fatal(err)
	}
	wantRaw, err := domain.MarshalArticleDocument(draft.Draft.Body)
	if err != nil || legacy != strings.Repeat("字", 140) || title != draft.Draft.Title || raw != string(wantRaw) ||
		created != domain.TimeToMillis(draft.CreatedAt) || updated != domain.TimeToMillis(draft.UpdatedAt) {
		t.Fatal("draft preview rewrote cached text, body, title or timestamps")
	}
	summaries, err = service.ArticleDraftSummaries(b)
	if err != nil || len(summaries) != 1 || summaries[0].ID != other.ID || summaries[0].Excerpt != "other author" {
		t.Fatalf("damaged draft did not retain its cached summary: %+v,%v", summaries, err)
	}
	if _, err := service.ArticleDraft(b, other.ID); err == nil {
		t.Fatal("damaged draft detail unexpectedly succeeded")
	}
}
