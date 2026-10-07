package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func (store postStore) SetArticlePublication(ctx context.Context, id domain.ID, state string) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE entries SET publication_state = ? WHERE id = ? AND kind = 'article'`, state, id.String())
	return err
}

func (store postStore) ArticleDraftSummaries(ctx context.Context, author string) ([]app.ArticleDraftSummary, error) {
	rows, err := store.queries.QueryContext(ctx, `SELECT draft.id,draft.author_user_id,draft.title,draft.published_entry_id,
 CASE WHEN entry.id IS NULL THEN 'draft' WHEN entry.publication_state = 'unpublished' THEN 'withdrawn' ELSE 'working_copy' END,
 COALESCE(entry.visibility,'everyone'), draft.created_at_ms,draft.updated_at_ms
 FROM article_drafts AS draft LEFT JOIN entries AS entry ON entry.id = draft.published_entry_id
 WHERE draft.author_user_id = ? ORDER BY draft.updated_at_ms DESC,draft.id DESC LIMIT ?`, author, app.MaxArticleDrafts)
	if err != nil {
		return nil, fmt.Errorf("list article draft summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]app.ArticleDraftSummary, 0)
	for rows.Next() {
		var summary app.ArticleDraftSummary
		var linked sql.NullString
		var created, updated int64
		if err := rows.Scan(&summary.ID, &summary.AuthorUserID, &summary.Title, &linked, &summary.SourceState, &summary.SourceVisibility, &created, &updated); err != nil {
			return nil, err
		}
		if linked.Valid {
			target := domain.ID(linked.String)
			summary.PublishedEntryID = &target
		}
		summary.CreatedAt = domain.TimeFromMillis(created)
		summary.UpdatedAt = domain.TimeFromMillis(updated)
		result = append(result, summary)
	}
	return result, rows.Err()
}
