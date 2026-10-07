package app

import (
	"context"
	"time"

	"github.com/moment/moment/internal/domain"
)

// ArticleDraftSummary projects list metadata without loading a document body.
type ArticleDraftSummary struct {
	ID               domain.ID
	AuthorUserID     string
	Title            string
	PublishedEntryID *domain.ID
	SourceState      string
	SourceVisibility domain.Visibility
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ArticleManagementStore owns the management-only projections and publication transition.
type ArticleManagementStore interface {
	ArticleDraftSummaries(context.Context, string) ([]ArticleDraftSummary, error)
	SetArticlePublication(context.Context, domain.ID, string) error
}

// ArticleDraftSummaries returns only the current author's unfinished metadata.
func (service *PostService) ArticleDraftSummaries(ctx context.Context) ([]ArticleDraftSummary, error) {
	var summaries []ArticleDraftSummary
	err := service.transactions.Read(ctx, func(store PostStore) error {
		author, err := contentActorID(ctx, store)
		if err != nil {
			return err
		}
		management, ok := store.(ArticleManagementStore)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "article management storage is unavailable", nil)
		}
		summaries, err = management.ArticleDraftSummaries(ctx, author)
		return err
	})
	return summaries, normalizePostRepositoryError(err)
}

// ManagedArticle permits its author to inspect either publication state.
func (service *PostService) ManagedArticle(ctx context.Context, id domain.ID) (Post, error) {
	var post Post
	err := service.transactions.Read(ctx, func(store PostStore) error {
		entry, found, err := store.EntryByID(ctx, id)
		if err != nil {
			return err
		}
		if !found || entry.Kind != domain.KindArticle {
			return postNotFound(id)
		}
		if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
			return err
		}
		post, err = loadArticlePost(ctx, store, id)
		return err
	})
	return post, normalizePostRepositoryError(err)
}

// PublishedArticles lists canonical publications owned by the current actor.
func (service *PostService) PublishedArticles(ctx context.Context, input ListPostsInput) (PostPage, error) {
	var author string
	err := service.transactions.Read(ctx, func(store PostStore) error { var err error; author, err = contentActorID(ctx, store); return err })
	if err != nil {
		return PostPage{}, normalizePostRepositoryError(err)
	}
	input.Kind = domain.KindArticle
	input.AuthorUserID = author
	input.OriginalsOnly = true
	input.Viewer = Viewer{userID: author}
	return service.List(ctx, input)
}

// WithdrawArticle keeps the entry, publication position and comments. A working copy
// takes precedence over the public body, so unpublished edits cannot be lost.
func (service *PostService) WithdrawArticle(ctx context.Context, id domain.ID) (ArticleDraftRecord, error) {
	var record ArticleDraftRecord
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, err := store.EntryByID(ctx, id)
		if err != nil {
			return err
		}
		if !found || entry.Kind != domain.KindArticle {
			return postNotFound(id)
		}
		if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
			return err
		}
		record, err = service.articleWorkingCopy(ctx, store, entry, domain.NormalizeTime(service.clock()))
		if err != nil {
			return err
		}
		management, ok := store.(ArticleManagementStore)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "article management storage is unavailable", nil)
		}
		if err := management.SetArticlePublication(ctx, id, "unpublished"); err != nil {
			return err
		}
		record.SourceState = "withdrawn"
		record.SourceVisibility = entry.Visibility
		return nil
	})
	return record, normalizePostRepositoryError(err)
}

func (service *PostService) articleWorkingCopy(ctx context.Context, store PostStore, entry domain.Entry, now time.Time) (ArticleDraftRecord, error) {
	record, found, err := store.ArticleDraftForEntry(ctx, entry.ID)
	if err != nil {
		return record, err
	}
	if found {
		if err := requireContentAuthor(ctx, store, record.AuthorUserID); err != nil {
			return record, err
		}
		return record, nil
	}
	count, err := store.CountArticleDrafts(ctx)
	if err != nil {
		return record, err
	}
	if count >= MaxArticleDrafts {
		return record, domain.NewError(domain.CodeArticleDraftLimit, "this account already keeps the maximum number of drafts", map[string]any{"limit": MaxArticleDrafts})
	}
	article, found, err := store.ArticleByEntryID(ctx, entry.ID)
	if err != nil {
		return record, err
	}
	if !found {
		return record, postNotFound(entry.ID)
	}
	draft, err := domain.NewArticleDraft(article.Title, article.Body, article.CoverID)
	if err != nil {
		return record, err
	}
	id, err := service.ids(now)
	if err != nil {
		return record, err
	}
	target := entry.ID
	state := "working_copy"
	if entry.PublicationState == "unpublished" {
		state = "withdrawn"
	}
	record = ArticleDraftRecord{AuthorUserID: entry.AuthorUserID, ID: id, Draft: draft, PublishedEntryID: &target, SourceState: state, SourceVisibility: entry.Visibility, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertArticleDraft(ctx, record); err != nil {
		return record, err
	}
	return record, reconcileArticleMedia(ctx, store, now, draft.CoverAndInlineMedia())
}
