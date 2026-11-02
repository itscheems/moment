package media

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/storage/sqlite"
)

// RetainLinkDraft replaces one browser draft's thumbnail and site-mark references.
// An empty list releases the draft; publication owns its own references first.
func (store *Store) RetainLinkDraft(ctx context.Context, key string, rawIDs []string) error {
	user, ok := app.ContentActor(ctx)
	if !ok {
		return domain.NewError(domain.CodeUnauthorized, "an account is required", nil)
	}
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 || len(rawIDs) > 2 {
		return domain.NewError(domain.CodeInvalidArgument, "invalid draft resources", nil)
	}
	ids := make([]domain.ID, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := domain.ParseID(raw)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	lease, err := store.acquireMaintenanceAndMedia(ctx, runtimefs.Shared)
	if err != nil {
		return err
	}
	defer lease.Release()
	return sqlite.ReplaceLinkDraftMedia(ctx, store.db, user.ID, key, ids, store.options.Clock())
}

// PruneLinkPreviews is the automatic subset of media maintenance: unrelated
// uploads are excluded even when they have never been attached to content.
func (store *Store) PruneLinkPreviews(ctx context.Context, now time.Time, dryRun bool) (PruneResult, error) {
	return store.prune(ctx, now, dryRun, true)
}
