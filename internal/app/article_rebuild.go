package app

import (
	"context"

	"github.com/moment/moment/internal/domain"
)

// ArticleDerivation is one article's authoritative body beside the two copies
// taken from it: the excerpt a card reads and the entry_media rows reclamation
// counts. Unparsable marks a body the repair cannot read, and therefore one it
// leaves exactly as it found it.
type ArticleDerivation struct {
	EntryID        domain.ID
	Article        domain.Article
	StoredExcerpt  string
	ProjectedMedia int
	Unparsable     bool
}

// Drifted reports whether either copy disagrees with the body it came from.
func (derivation ArticleDerivation) Drifted() bool {
	if derivation.Unparsable {
		return false
	}
	return derivation.StoredExcerpt != derivation.Article.Excerpt() ||
		derivation.ProjectedMedia != len(derivation.Article.InlineMedia())
}

// ArticleRebuildResult is what one inspection or repair found and did.
type ArticleRebuildResult struct {
	DryRun     bool `json:"dry_run"`
	Total      int  `json:"total"`
	Drifted    int  `json:"drifted"`
	Rebuilt    int  `json:"rebuilt"`
	Unparsable int  `json:"unparsable"`
}

// RebuildArticleDerivations repairs derived article values without replacing authoritative documents.
func (service *PostService) RebuildArticleDerivations(ctx context.Context, dryRun bool) (ArticleRebuildResult, error) {
	if err := contextError(ctx); err != nil {
		return ArticleRebuildResult{}, err
	}
	result := ArticleRebuildResult{DryRun: dryRun}
	operation := func(store PostStore) error {
		result = ArticleRebuildResult{DryRun: dryRun}
		derivations, err := store.ArticleDerivations(ctx)
		if err != nil {
			return err
		}
		for _, derivation := range derivations {
			result.Total++
			if derivation.Unparsable {
				result.Unparsable++
				continue
			}
			if !derivation.Drifted() {
				continue
			}
			result.Drifted++
			if dryRun {
				continue
			}
			// The same write publishing uses, so a repaired article is written
			// by the one path that knows how both copies are made.
			if err := store.ReplaceArticle(ctx, derivation.EntryID, derivation.Article); err != nil {
				return err
			}
			// A drifted projection was a disagreement about which pictures the
			// article holds, so repairing it changes reachability and the
			// reclamation clock has to follow.
			if err := reconcileArticleMedia(ctx, store, service.clock(),
				derivation.Article.CoverAndInlineMedia()); err != nil {
				return err
			}
			result.Rebuilt++
		}
		return nil
	}
	var err error
	if dryRun {
		err = service.transactions.Read(ctx, operation)
	} else {
		err = service.transactions.Write(ctx, operation)
	}
	if err != nil {
		return ArticleRebuildResult{}, err
	}
	return result, nil
}
