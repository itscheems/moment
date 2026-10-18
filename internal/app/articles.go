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
	AuthorUserID     string
	SourceState      string
	SourceVisibility domain.Visibility
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

// articleRenderOptions carries the stored geometry of the pictures a body
// places, so the page reserves their space before they arrive. The projection
// is the only place that size is known at render time.
func articleRenderOptions(media []MediaUsageProjection) domain.ArticleRenderOptions {
	sizes := make(map[domain.ID][2]uint32, len(media))
	for _, usage := range media {
		sizes[usage.MediaID] = [2]uint32{usage.Width, usage.Height}
	}
	return domain.ArticleRenderOptions{
		MediaURL: ArticleMediaURL,
		MediaGeometry: func(mediaID domain.ID) (uint32, uint32, bool) {
			size, known := sizes[mediaID]
			return size[0], size[1], known
		},
	}
}

// renderEntryHTML produces the HTML a reader receives, whichever body shape the
// entry has. An entry whose body did not come along is a read that lost half of
// itself, so it fails here rather than answering an empty page.
func renderEntryHTML(entry domain.Entry, article *domain.Article, media []MediaUsageProjection, cards []domain.LinkCard) (string, error) {
	switch entry.Kind {
	case domain.KindArticle:
		if article == nil {
			return "", fmt.Errorf("article %s was read without its body", entry.ID)
		}
		return domain.RenderArticleHTML(article.Body, articleRenderOptions(media))
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
	IdempotencyKey    string
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

// reconcileArticleMedia restarts or stops the reclamation clock on every
// picture a write could have changed the reachability of -- the ones it named
// before and the ones it names now.
//
// Every other write path in this service does this, and an article's did not:
// a cover on the timeline still read as unreferenced, and a picture that lost
// its last reference when a draft was thrown away kept a null clock forever,
// which is the one state prune will never collect.
func reconcileArticleMedia(ctx context.Context, store PostStore, now time.Time, groups ...[]domain.ID) error {
	var affected []domain.ID
	for _, group := range groups {
		affected = append(affected, group...)
	}
	if len(affected) == 0 {
		return nil
	}
	return store.ReconcileMediaReferences(ctx, affected, now)
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
			author, err := contentActorID(ctx, store)
			if err != nil {
				return err
			}
			scope := "articles.drafts.create.v1." + author
			digest := sha256.Sum256(append([]byte(input.Title+"\x00"+optionalDraftCover(input.CoverMediaID)+"\x00"), input.BodyJSON...))
			requestHash := hex.EncodeToString(digest[:])
			if input.IdempotencyKey != "" {
				key, found, err := store.IdempotencyRecord(ctx, scope, input.IdempotencyKey)
				if err != nil {
					return err
				}
				if found {
					if key.RequestHash != requestHash {
						return domain.NewError(domain.CodeIdempotencyKeyReused, "draft create key reused", nil)
					}
					saved, found, err = store.ArticleDraftByID(ctx, key.ResponseResourceID)
					if err != nil {
						return err
					}
					if !found {
						return articleDraftNotFound(key.ResponseResourceID)
					}
					return requireContentAuthor(ctx, store, saved.AuthorUserID)
				}
			}
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
			authorID, authorErr := contentActorID(ctx, store)
			if authorErr != nil {
				return authorErr
			}
			saved = ArticleDraftRecord{AuthorUserID: authorID, ID: id, Draft: draft, CreatedAt: now, UpdatedAt: now}
			if insertErr := store.InsertArticleDraft(ctx, saved); insertErr != nil {
				return insertErr
			}
			if input.IdempotencyKey != "" {
				if err := store.InsertIdempotencyRecord(ctx, IdempotencyRecord{AuthorUserID: author, Scope: scope, Key: input.IdempotencyKey, RequestHash: requestHash, ResponseResourceID: id, CreatedAt: now}); err != nil {
					return err
				}
			}
			return reconcileArticleMedia(ctx, store, now, draft.CoverAndInlineMedia())
		}

		existing, found, readErr := store.ArticleDraftByID(ctx, *input.ID)
		if readErr != nil {
			return readErr
		}
		if !found {
			return articleDraftNotFound(*input.ID)
		}
		if err := requireContentAuthor(ctx, store, existing.AuthorUserID); err != nil {
			return err
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
			AuthorUserID: existing.AuthorUserID, SourceState: existing.SourceState, SourceVisibility: existing.SourceVisibility, ID: existing.ID, Draft: draft, PublishedEntryID: existing.PublishedEntryID,
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
		// Both sides: a picture the replacement dropped has just lost a
		// reference, and one it added has just gained one.
		return reconcileArticleMedia(ctx, store, now,
			existing.Draft.CoverAndInlineMedia(), draft.CoverAndInlineMedia())
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
	var record ArticleDraftRecord
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, err := store.EntryByID(ctx, entryID)
		if err != nil {
			return err
		}
		if !found || entry.Kind != domain.KindArticle {
			return postNotFound(entryID)
		}
		if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
			return err
		}
		record, err = service.articleWorkingCopy(ctx, store, entry, domain.NormalizeTime(service.clock()))
		return err
	})
	return record, normalizePostRepositoryError(err)
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
		if err := requireContentAuthor(ctx, store, record.AuthorUserID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ArticleDraftRecord{}, normalizePostRepositoryError(err)
	}
	return record, nil
}

// DiscardArticleDraft removes a draft belonging to the authenticated author.
func (service *PostService) DiscardArticleDraft(ctx context.Context, id domain.ID) error {
	now := domain.NormalizeTime(service.clock())
	err := service.transactions.Write(ctx, func(store PostStore) error {
		// Read what it pointed at before it is gone: afterwards there is
		// nothing left to ask, and a picture whose last reference this was
		// would keep a null clock and never be collected.
		existing, found, readErr := store.ArticleDraftByID(ctx, id)
		if readErr != nil {
			return readErr
		}
		if !found {
			return articleDraftNotFound(id)
		}
		if err := requireContentAuthor(ctx, store, existing.AuthorUserID); err != nil {
			return err
		}
		var withdrawnMedia []domain.ID
		if existing.PublishedEntryID != nil {
			entry, found, err := store.EntryByID(ctx, *existing.PublishedEntryID)
			if err != nil {
				return err
			}
			if found && entry.PublicationState == "unpublished" {
				withdrawnMedia, err = store.EntryMediaIDs(ctx, entry.ID)
				if err != nil {
					return err
				}
				if _, err := store.DeleteEntry(ctx, entry.ID); err != nil {
					return err
				}
			}
		}
		deleted, writeErr := store.DeleteArticleDraft(ctx, id)
		if writeErr != nil {
			return writeErr
		}
		if !deleted {
			return articleDraftNotFound(id)
		}
		return reconcileArticleMedia(ctx, store, now, existing.Draft.CoverAndInlineMedia(), withdrawnMedia)
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

	requestHash := hashPublishArticle(input.DraftID, visibility)
	var published Post
	err = service.transactions.Write(ctx, func(store PostStore) error {
		author, err := contentActorID(ctx, store)
		if err != nil {
			return err
		}
		scope := articlePublishIdempotencyScope + "." + author
		if input.IdempotencyKey != "" {
			record, found, storeErr := store.IdempotencyRecord(ctx, scope, input.IdempotencyKey)
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
				if err := requireContentAuthor(ctx, store, replayed.Entry.AuthorUserID); err != nil {
					return err
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
		if err := requireContentAuthor(ctx, store, draft.AuthorUserID); err != nil {
			return err
		}
		article, publishErr := draft.Draft.Publish()
		if publishErr != nil {
			return publishErr
		}
		contentText := DerivedArticleSearchText(article.Title, article.Body)

		now := domain.NormalizeTime(service.clock())
		entryID := draft.PublishedEntryID
		// What the version being replaced pointed at, read before it is
		// overwritten: a picture this publish drops loses its last reference
		// here and its clock has to start.
		var superseded []domain.ID
		if entryID == nil {
			created, createErr := service.insertArticleEntry(ctx, store, article, contentText, visibility, now)
			if createErr != nil {
				return createErr
			}
			entryID = &created
		} else {
			previous, hadOne, readPreviousErr := store.ArticleByEntryID(ctx, *entryID)
			if readPreviousErr != nil {
				return readPreviousErr
			}
			if hadOne {
				superseded = previous.CoverAndInlineMedia()
			}
			if replaceErr := service.replaceArticleEntry(ctx, store, *entryID, article, contentText, now); replaceErr != nil {
				return replaceErr
			}
		}

		if _, deleteErr := store.DeleteArticleDraft(ctx, draft.ID); deleteErr != nil {
			return deleteErr
		}
		// The draft's rows are gone and the article's are in place, so this is
		// the moment the reachability of all three sets is finally true.
		if reconcileErr := reconcileArticleMedia(ctx, store, now,
			superseded, draft.Draft.CoverAndInlineMedia(), article.CoverAndInlineMedia()); reconcileErr != nil {
			return reconcileErr
		}
		if input.IdempotencyKey != "" {
			if recordErr := store.InsertIdempotencyRecord(ctx, IdempotencyRecord{
				AuthorUserID: author, Scope: scope, Key: input.IdempotencyKey,
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
	entry.AuthorUserID, err = contentActorID(ctx, store)
	if err != nil {
		return "", err
	}
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
	if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
		return err
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
	if management, ok := store.(ArticleManagementStore); ok {
		if err := management.SetArticlePublication(ctx, entryID, "published"); err != nil {
			return err
		}
	}
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
	if err := attachContentAuthor(ctx, store, &post); err != nil {
		return Post{}, err
	}
	return post, nil
}

// hashPublishArticle identifies one publish request. Publishing the same draft
// to the same audience twice is a retry; publishing a different draft, or the
// same one to a different audience, is a mistake worth naming.
//
// The audience belongs in here for the reason it belongs in hashCreatePost: it
// changes who can read the result, and a retry that quietly keeps the first
// answer would publish to an audience the caller did not ask for. This scope is
// new, so unlike hashCreatePost there are no keys minted before it existed and
// no carve-out to keep them replaying.
func hashPublishArticle(draftID domain.ID, visibility domain.Visibility) string {
	digest := sha256.Sum256([]byte("moment-article-publish-v1\x00" + draftID.String() +
		"\x00visibility-v1\x00" + string(visibility)))
	return hex.EncodeToString(digest[:])
}

func articleDraftNotFound(id domain.ID) error {
	return domain.NewError(domain.CodeArticleDraftNotFound, "article draft does not exist", map[string]any{"id": id.String()})
}

func optionalDraftCover(id *domain.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
