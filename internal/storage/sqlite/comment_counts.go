package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/moment/moment/internal/domain"
)

func queryCommentCounts(ctx context.Context, queries Queries, ids []domain.ID) (map[domain.ID]int, error) {
	counts := make(map[domain.ID]int, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	placeholders := make([]string, len(ids))
	arguments := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		arguments[index] = id.String()
		counts[id] = 0
	}
	rows, err := queries.QueryContext(ctx, `
SELECT entry_id, count(*)
FROM comments
WHERE entry_id IN (`+strings.Join(placeholders, ",")+`)
GROUP BY entry_id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query comment counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rawID string
		var count int
		if err := rows.Scan(&rawID, &count); err != nil {
			return nil, fmt.Errorf("scan comment count: %w", err)
		}
		id, err := domain.ParseID(rawID)
		if err != nil || count < 0 {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment count is invalid", nil)
		}
		counts[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment counts: %w", err)
	}
	return counts, nil
}
