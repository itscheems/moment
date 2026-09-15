package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

type searchBootstrapSource struct {
	id        string
	contentMD string
	cards     []domain.LinkCard
}

// bootstrapSearchDerived upgrades existing authoritative rows exactly once
// when a new normalizer version lands. Ordinary opens do not repair later
// drift; the observable search rebuild operation owns that behavior.
func bootstrapSearchDerived(ctx context.Context, database *sql.DB) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin search bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, `SELECT normalizer_version FROM search_index_state WHERE id = 1`).Scan(&version); err != nil {
		return fmt.Errorf("read search index state: %w", err)
	}
	if version > domain.ContentNormalizerVersion {
		return fmt.Errorf("%w: search normalizer version %d is newer than %d", ErrSchemaTooNew, version, domain.ContentNormalizerVersion)
	}
	if version == domain.ContentNormalizerVersion {
		return tx.Commit()
	}

	// The cards ride along: the indexed text is the Markdown and the cards
	// combined, exactly as the write path composes it, and a bootstrap that
	// re-indexed from the Markdown alone dropped every card and left the
	// doctor reading each carded entry as drifted right after an upgrade.
	rows, err := tx.QueryContext(ctx, `
SELECT e.id, e.content_md, `+searchSourceCardsSQL+`
FROM entries AS e
WHERE e.kind = ?
ORDER BY e.id`, string(domain.KindMoment))
	if err != nil {
		return fmt.Errorf("read entries for search bootstrap: %w", err)
	}
	var sources []searchBootstrapSource
	for rows.Next() {
		var source searchBootstrapSource
		var cardsJSON string
		if err := rows.Scan(&source.id, &source.contentMD, &cardsJSON); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan entry for search bootstrap: %w", err)
		}
		if source.cards, err = decodeSearchSourceCards(cardsJSON); err != nil {
			_ = rows.Close()
			return err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate entries for search bootstrap: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close search bootstrap entries: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM entry_fts`); err != nil {
		return fmt.Errorf("clear search index for bootstrap: %w", err)
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		contentText, err := app.DerivedSearchText(source.contentMD, source.cards)
		if err != nil {
			return fmt.Errorf("normalize entry %s for search bootstrap: %w", source.id, err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE entries SET content_text = ? WHERE id = ? AND kind = ?`, contentText, source.id, string(domain.KindMoment))
		if err != nil {
			return fmt.Errorf("write entry %s search bootstrap: %w", source.id, err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("write entry %s search bootstrap changed %d rows: %w", source.id, changed, err)
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE search_index_state
SET normalizer_version = ?, revision = revision + 1
WHERE id = 1`, domain.ContentNormalizerVersion)
	if err != nil {
		return fmt.Errorf("advance search normalizer version: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("advance search normalizer version changed %d rows: %w", changed, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit search bootstrap: %w", err)
	}
	return nil
}
