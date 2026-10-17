package sqlite

import (
	"context"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func (store mediaReferenceStore) MediaAvailableForAccount(ctx context.Context, userID string, id domain.ID) (bool, error) {
	var present bool
	err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_grants WHERE user_id=? AND media_id=?)`, userID, id.String()).Scan(&present)
	return present, err
}

func (store mediaReferenceStore) grantMedia(ctx context.Context, userID string, id domain.ID, atMS int64) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO media_grants(user_id,media_id,created_at_ms) VALUES(?,?,?) ON CONFLICT(user_id,media_id) DO NOTHING`, userID, id.String(), atMS)
	return err
}

func contentMediaActorID(ctx context.Context) string {
	user, ok := app.ContentActor(ctx)
	if !ok || !user.CanWritePublicContent() {
		return ""
	}
	return user.ID
}

func (store accountStore) grantProfileMedia(ctx context.Context, user domain.User) error {
	for _, raw := range []*string{user.AvatarMediaID, user.CoverMediaID} {
		if raw == nil {
			continue
		}
		id, err := domain.ParseID(*raw)
		if err != nil {
			return err
		}
		if err := store.grantMedia(ctx, user.ID, id, user.UpdatedAtMS); err != nil {
			return fmt.Errorf("grant captured profile media: %w", err)
		}
	}
	return nil
}
