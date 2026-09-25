package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediacontract"
)

// ArticleCard is what a list needs of an article: enough to draw its card
// without shipping the body. A timeline page holding twenty full bodies would
// send megabytes nobody reads.
type ArticleCard struct {
	Title   string
	Excerpt string
	Cover   *ArticleCover
}

// ArticleCover is the picture a card leads with. It carries geometry for the
// same reason every media projection does: the browser has to reserve the
// space before the bytes arrive, or the page jumps when they do.
type ArticleCover struct {
	MediaID  domain.ID
	MIMEType string
	Width    uint32
	Height   uint32
}

// ArticleDraftRecord is one stored draft. PublishedEntryID is nil for a draft
// that was never published and names the entry when the draft is the working
// copy of one: editing a published article must not change what readers see
// with every keystroke.
type ArticleDraftRecord struct {
	ID               domain.ID
	Draft            domain.ArticleDraft
	PublishedEntryID *domain.ID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// MaxArticleDrafts bounds how many unfinished articles one site keeps.
const MaxArticleDrafts = 50

// ArticleMediaURL addresses a managed picture the way a browser reaches it.
//
// The recipe version rides in the query because it is part of the browser's
// cache key: when the derived-media recipe changes, a cached rendition must not
// be reused at the same address. The Web client builds the same URL, and the
// shared constant is what keeps the two spellings from drifting.
func ArticleMediaURL(mediaID domain.ID) string {
	return "/api/v1/media/" + url.PathEscape(mediaID.String()) +
		"/preview?v=" + url.QueryEscape(mediacontract.RecipeVersion)
}

func articleRenderOptions() domain.ArticleRenderOptions {
	return domain.ArticleRenderOptions{MediaURL: ArticleMediaURL}
}

// renderEntryHTML produces the HTML a reader receives, whichever body shape the
// entry has. An entry whose body did not come along is a read that lost half of
// itself, so it fails here rather than answering an empty page.
func renderEntryHTML(entry domain.Entry, article *domain.Article, cards []domain.LinkCard) (string, error) {
	switch entry.Kind {
	case domain.KindArticle:
		if article == nil {
			return "", fmt.Errorf("article %s was read without its body", entry.ID)
		}
		return domain.RenderArticleHTML(article.Body, articleRenderOptions())
	default:
		body, present := entry.Markdown()
		if !present {
			return "", fmt.Errorf("moment %s was read without its body", entry.ID)
		}
		return domain.RenderMarkdownWithLinkCards(body, cards)
	}
}

// validateStoredEntryBody re-checks what was read back, the way the Markdown
// path already did. An article body was validated when it was parsed, so the
// only thing left to insist on is that it is there.
func validateStoredEntryBody(entry domain.Entry, article *domain.Article, mediaCount int) error {
	if entry.Kind == domain.KindArticle {
		if article == nil {
			return fmt.Errorf("article %s was read without its body", entry.ID)
		}
		return nil
	}
	body, present := entry.Markdown()
	if !present {
		return fmt.Errorf("moment %s was read without its body", entry.ID)
	}
	return domain.ValidatePostMarkdown(body, mediaCount)
}

// articlePublishIdempotencyScope keeps a retried publish from producing a
// second article, the way posts.create.v1 does for a moment.
const articlePublishIdempotencyScope = "articles.publish.v1"

// SaveArticleDraftInput creates a draft when ID is nil and replaces one
// otherwise. A replacement must say which revision it was based on, so that
// two tabs editing the same draft cannot silently overwrite each other.
type SaveArticleDraftInput struct {
	ID                *domain.ID
	Title             string
	BodyJSON          []byte
	CoverMediaID      *domain.ID
	ExpectedUpdatedAt *time.Time
}

// PublishArticleDraftInput turns a draft into a post, or replaces the article
// the draft is a working copy of.
type PublishArticleDraftInput struct {
	DraftID        domain.ID
	Visibility     domain.Visibility
	IdempotencyKey string
}

// SaveArticleDraft writes an unfinished article. The body goes through the
// grammar but not the rule that a document has to say something: a draft is
// allowed to be empty, and publishing is where that stops being allowed.
func (service *PostService) SaveArticleDraft(ctx context.Context, input SaveArticleDraftInput) (ArticleDraftRecord, error) {
	body, err := domain.ParseArticleDraftDocument(input.BodyJSON)
	if err != nil {
		return ArticleDraftRecord{}, err
	}
	draft, err := domain.NewArticleDraft(input.Title, body, input.CoverMediaID)
	if err != nil {
		return ArticleDraftRecord{}, err
	}

	now := domain.NormalizeTime(service.clock())
	var saved ArticleDraftRecord
	err = service.transactions.Write(ctx, func(store PostStore) error {
		if input.ID == nil {
			count, countErr := store.CountArticleDrafts(ctx)
			if countErr != nil {
				return countErr
			}
			if count >= MaxArticleDrafts {
				return domain.NewError(domain.CodeArticleDraftLimit,
					"this site already keeps the maximum number of drafts",
					map[string]any{"limit": MaxArticleDrafts})
			}
			id, idErr := service.ids(now)
			if idErr != nil {
				return idErr
			}
			saved = ArticleDraftRecord{ID: id, Draft: draft, CreatedAt: now, UpdatedAt: now}
			return store.InsertArticleDraft(ctx, saved)
		}

		existing, found, readErr := store.ArticleDraftByID(ctx, *input.ID)
		if readErr != nil {
			return readErr
		}
		if !found {
			return articleDraftNotFound(*input.ID)
		}
		if input.ExpectedUpdatedAt == nil {
			return domain.NewError(domain.CodeInvalidArgument,
				"replacing a draft must say which revision it is based on",
				map[string]any{"field": "expected_updated_at"})
		}
		// The clock can repeat within a millisecond, and a revision that does
		// not move would let the next stale write look current.
		next := now
		if !next.After(existing.UpdatedAt) {
			next = existing.UpdatedAt.Add(time.Millisecond)
		}
		saved = ArticleDraftRecord{
			ID: existing.ID, Draft: draft, PublishedEntryID: existing.PublishedEntryID,
			CreatedAt: existing.CreatedAt, UpdatedAt: next,
		}
		applied, writeErr := store.UpdateArticleDraft(ctx, saved, *input.ExpectedUpdatedAt)
		if writeErr != nil {
			return writeErr
		}
		if !applied {
			return domain.NewError(domain.CodeArticleDraftStale,
				"this draft changed since it was read",
				map[string]any{"id": existing.ID.String(), "updated_at": existing.UpdatedAt.UTC()})
		}
		return nil
	})
	if err != nil {
		return ArticleDraftRecord{}, normalizePostRepositoryError(err)
	}
	return saved, nil
}

// EditArticle opens the working copy of a published article, reusing the one
// that already exists. Editing must not change what readers see with every
// keystroke, which is the whole reason the copy exists.
func (service *PostService) EditArticle(ctx context.Context, entryID domain.ID) (ArticleDraftRecord, error) {
	now := domain.NormalizeTime(service.clock())
	var record ArticleDraftRecord
	err := service.transactions.Write(ctx, func(store PostStore) error {
		existing, found, readErr := store.ArticleDraftForEntry(ctx, entryID)
		if readErr != nil {
			return readErr
		}
		if found {
			record = existing
			return nil
		}
		article, found, readErr := store.ArticleByEntryID(ctx, entryID)
		if readErr != nil {
			return readErr
		}
		if !found {
			return postNotFound(entryID)
		}
		draft, draftErr := domain.NewArticleDraft(article.Title, article.Body, article.CoverID)
		if draftErr != nil {
			return draftErr
		}
		id, idErr := service.ids(now)
		if idErr != nil {
			return idErr
		}
		target := entryID
		record = ArticleDraftRecord{ID: id, Draft: draft, PublishedEntryID: &target, CreatedAt: now, UpdatedAt: now}
		return store.InsertArticleDraft(ctx, record)
	})
	if err != nil {
		return ArticleDraftRecord{}, normalizePostRepositoryError(err)
	}
	return record, nil
}

// ArticleDrafts lists what is unfinished, newest first.
func (service *PostService) ArticleDrafts(ctx context.Context) ([]ArticleDraftRecord, error) {
	var records []ArticleDraftRecord
	err := service.transactions.Read(ctx, func(store PostStore) error {
		var readErr error
		records, readErr = store.ListArticleDrafts(ctx, MaxArticleDrafts)
		return readErr
	})
	if err != nil {
		return nil, normalizePostRepositoryError(err)
	}
	return records, nil
}

// ArticleDraft reads one draft back.
func (service *PostService) ArticleDraft(ctx context.Context, id domain.ID) (ArticleDraftRecord, error) {
	var record ArticleDraftRecord
	err := service.transactions.Read(ctx, func(store PostStore) error {
		found := false
		var readErr error
		record, found, readErr = store.ArticleDraftByID(ctx, id)
		if readErr != nil {
			return readErr
		}
		if !found {
			return articleDraftNotFound(id)
		}
		return nil
	})
	if err != nil {
		return ArticleDraftRecord{}, normalizePostRepositoryError(err)
	}
	return record, nil
}

// DiscardArticleDraft throws away unfinished work at the owner's word.
func (service *PostService) DiscardArticleDraft(ctx context.Context, id domain.ID) error {
	err := service.transactions.Write(ctx, func(store PostStore) error {
		deleted, writeErr := store.DeleteArticleDraft(ctx, id)
		if writeErr != nil {
			return writeErr
		}
		if !deleted {
			return articleDraftNotFound(id)
		}
		return nil
	})
	if err != nil {
		return normalizePostRepositoryError(err)
	}
	return nil
}

// PublishArticleDraft makes a draft public, or replaces the article it is a
// working copy of, and throws the draft away once it has.
//
// The whole thing is one transaction. A half-published article -- a timeline
// row with no body, or a body with no row -- is the failure this shape exists
// to make impossible.
func (service *PostService) PublishArticleDraft(ctx context.Context, input PublishArticleDraftInput) (Post, error) {
	visibility, err := createVisibility(input.Visibility)
	if err != nil {
		return Post{}, err
	}

	requestHash := hashPublishArticle(input.DraftID)
	var published Post
	err = service.transactions.Write(ctx, func(store PostStore) error {
		if input.IdempotencyKey != "" {
			record, found, storeErr := store.IdempotencyRecord(ctx, articlePublishIdempotencyScope, input.IdempotencyKey)
			if storeErr != nil {
				return storeErr
			}
			if found {
				// The key identifies one request. Reusing it for a different
				// draft is a mistake worth naming rather than answering with
				// somebody else's article.
				if record.RequestHash != requestHash {
					return domain.NewError(domain.CodeIdempotencyKeyReused,
						"this idempotency key was used for a different publish",
						map[string]any{"field": "idempotency_key"})
				}
				// A replay re-reads what was written rather than rebuilding it
				// from an input the draft no longer backs.
				replayed, replayErr := loadArticlePost(ctx, store, record.ResponseResourceID)
				if replayErr != nil {
					return replayErr
				}
				published = replayed
				return nil
			}
		}

		draft, found, storeErr := store.ArticleDraftByID(ctx, input.DraftID)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return articleDraftNotFound(input.DraftID)
		}
		article, publishErr := draft.Draft.Publish()
		if publishErr != nil {
			return publishErr
		}
		contentText := DerivedArticleSearchText(article.Title, article.Body)

		now := domain.NormalizeTime(service.clock())
		entryID := draft.PublishedEntryID
		if entryID == nil {
			created, createErr := service.insertArticleEntry(ctx, store, article, contentText, visibility, now)
			if createErr != nil {
				return createErr
			}
			entryID = &created
		} else if replaceErr := service.replaceArticleEntry(ctx, store, *entryID, article, contentText, now); replaceErr != nil {
			return replaceErr
		}

		if _, deleteErr := store.DeleteArticleDraft(ctx, draft.ID); deleteErr != nil {
			return deleteErr
		}
		if input.IdempotencyKey != "" {
			if recordErr := store.InsertIdempotencyRecord(ctx, IdempotencyRecord{
				Scope: articlePublishIdempotencyScope, Key: input.IdempotencyKey,
				RequestHash: requestHash, ResponseResourceID: *entryID, CreatedAt: now,
			}); recordErr != nil {
				return recordErr
			}
		}
		result, readErr := loadArticlePost(ctx, store, *entryID)
		if readErr != nil {
			return readErr
		}
		published = result
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	return published, nil
}

func (service *PostService) insertArticleEntry(ctx context.Context, store PostStore, article domain.Article, contentText string, visibility domain.Visibility, now time.Time) (domain.ID, error) {
	id, err := service.ids(now)
	if err != nil {
		return "", err
	}
	entry, err := domain.NewArticleEntry(id, now)
	if err != nil {
		return "", err
	}
	entry.Visibility = visibility
	if err := entry.Validate(); err != nil {
		return "", err
	}
	if err := store.InsertEntry(ctx, entry, contentText); err != nil {
		return "", err
	}
	if err := store.InsertArticle(ctx, id, article); err != nil {
		return "", err
	}
	return id, nil
}

func (service *PostService) replaceArticleEntry(ctx context.Context, store PostStore, entryID domain.ID, article domain.Article, contentText string, now time.Time) error {
	entry, found, err := store.EntryByID(ctx, entryID)
	if err != nil {
		return err
	}
	if !found {
		return postNotFound(entryID)
	}
	if entry.Kind != domain.KindArticle {
		return domain.NewError(domain.CodeInvalidArgument, "that post is not an article", map[string]any{"id": entryID.String()})
	}
	if err := store.ReplaceArticle(ctx, entryID, article); err != nil {
		return err
	}
	// The clock can repeat inside a millisecond, and updated_at must move.
	updated := now
	if !updated.After(entry.UpdatedAt) {
		updated = entry.UpdatedAt.Add(time.Millisecond)
	}
	edited := updated
	entry.UpdatedAt = updated
	entry.EditedAt = &edited
	changed, err := store.UpdateArticleEntry(ctx, entry, contentText)
	if err != nil {
		return err
	}
	if !changed {
		return postNotFound(entryID)
	}
	return nil
}

// loadArticlePost reads a published article back as the post a reader sees.
func loadArticlePost(ctx context.Context, store PostStore, entryID domain.ID) (Post, error) {
	entry, found, err := store.EntryByID(ctx, entryID)
	if err != nil {
		return Post{}, err
	}
	if !found {
		return Post{}, postNotFound(entryID)
	}
	view, err := loadArticleView(ctx, store, entry)
	if err != nil {
		return Post{}, err
	}
	media, err := store.EntryMediaUsages(ctx, entryID)
	if err != nil {
		return Post{}, err
	}
	counts, err := store.CommentCounts(ctx, []domain.ID{entryID})
	if err != nil {
		return Post{}, err
	}
	post, err := renderPost(entry, view, media, nil, nil, counts[entryID])
	if err != nil {
		return Post{}, err
	}
	post.TimelineKind = TimelineItemOriginal
	return post, nil
}

// hashPublishArticle identifies one publish request. The draft is the request:
// publishing the same draft twice is a retry, publishing a different one under
// the same key is a mistake.
func hashPublishArticle(draftID domain.ID) string {
	digest := sha256.Sum256([]byte("moment-article-publish-v1\x00" + draftID.String()))
	return hex.EncodeToString(digest[:])
}

func articleDraftNotFound(id domain.ID) error {
	return domain.NewError(domain.CodeArticleDraftNotFound, "article draft does not exist", map[string]any{"id": id.String()})
}
