package media

import (
	"context"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/storage/sqlite"
)

// grantIngestedMedia shares the immutable object with this uploader. Trusted
// internal capture without an account is associated when its account is created.
func grantIngestedMedia(ctx context.Context, queries sqlite.Queries, id domain.ID, at time.Time) error {
	user, ok := app.ContentActor(ctx)
	if !ok {
		return nil
	}
	_, err := queries.ExecContext(ctx, `INSERT INTO media_grants(user_id,media_id,created_at_ms) VALUES(?,?,?) ON CONFLICT(user_id,media_id) DO NOTHING`, user.ID, id.String(), at.UnixMilli())
	return err
}

func (store *Store) grantExistingMedia(ctx context.Context, id domain.ID) error {
	return store.db.WriteTx(ctx, func(queries sqlite.Queries) error { return grantIngestedMedia(ctx, queries, id, store.options.Clock()) })
}
