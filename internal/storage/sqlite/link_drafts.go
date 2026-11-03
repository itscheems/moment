package sqlite

import (
	"context"
	"time"

	"github.com/moment/moment/internal/domain"
)

// ReplaceLinkDraftMedia protects only media the selected account can reuse.
// A browser draft expires after thirty days; its media reference shares that lifetime.
func ReplaceLinkDraftMedia(ctx context.Context, db Transactor, userID, key string, ids []domain.ID, now time.Time) error {
	return db.WriteTx(ctx, func(q Queries) error {
		previous, err := linkDraftMediaIDs(ctx, q, `WHERE user_id=? AND draft_key=?`, userID, key)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var allowed bool
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_grants WHERE user_id=? AND media_id=?)`, userID, id.String()).Scan(&allowed); err != nil {
				return err
			}
			if !allowed {
				return domain.NewError(domain.CodeMediaNotFound, "draft media is unavailable", nil)
			}
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM compose_draft_link_media WHERE user_id=? AND draft_key=?`, userID, key); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := q.ExecContext(ctx, `INSERT INTO compose_draft_link_media(user_id,draft_key,media_id,expires_at_ms) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, userID, key, id.String(), now.Add(30*24*time.Hour).UnixMilli()); err != nil {
				return err
			}
		}
		return newMediaReferenceStore(q).ReconcileMediaReferences(ctx, append(previous, ids...), now)
	})
}

// ExpireLinkDraftMedia releases expired references and starts their normal GC grace period.
func ExpireLinkDraftMedia(ctx context.Context, db Transactor, now time.Time) error {
	return db.WriteTx(ctx, func(q Queries) error {
		ids, err := linkDraftMediaIDs(ctx, q, `WHERE expires_at_ms<=?`, now.UnixMilli())
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM compose_draft_link_media WHERE expires_at_ms<=?`, now.UnixMilli()); err != nil {
			return err
		}
		return newMediaReferenceStore(q).ReconcileMediaReferences(ctx, ids, now)
	})
}

func linkDraftMediaIDs(ctx context.Context, q Queries, predicate string, args ...any) ([]domain.ID, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT media_id FROM compose_draft_link_media `+predicate, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []domain.ID
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
