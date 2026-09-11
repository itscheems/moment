package media

import (
	"context"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// DeleteResult distinguishes authority deletion from best-effort blob cleanup.
type DeleteResult struct {
	MediaID         string `json:"media_id"`
	DryRun          bool   `json:"dry_run"`
	Deleted         bool   `json:"deleted"`
	CleanupFailures int    `json:"cleanup_failures"`
}

// Delete removes a selected unreferenced object, serialized with ingestion,
// reference mutations and prune. The global pool has no per-uploader ownership;
// callers must authorize the site owner, never an arbitrary active member.
func (store *Store) Delete(ctx context.Context, id domain.ID, dryRun bool) (DeleteResult, error) {
	if _, err := domain.ParseID(id.String()); err != nil {
		return DeleteResult{}, err
	}
	mode := runtimefs.Exclusive
	if dryRun {
		mode = runtimefs.Shared
	}
	leases, err := store.acquireMaintenanceAndMedia(ctx, mode)
	if err != nil {
		return DeleteResult{}, err
	}
	defer func() { _ = leases.Release() }()
	value, err := store.getLocked(ctx, id)
	if err != nil {
		return DeleteResult{}, err
	}
	result := DeleteResult{MediaID: id.String(), DryRun: dryRun}
	inUse := func() error {
		return domain.NewError(domain.CodeMediaInUse, "media is still referenced", map[string]any{"media_id": id.String()})
	}
	if dryRun {
		err = store.references.Read(ctx, func(resolver app.MediaReferenceStore) error {
			ids, err := resolver.ResolveMediaReferences(ctx)
			if err != nil {
				return err
			}
			for _, referenced := range ids {
				if referenced == id {
					return inUse()
				}
			}
			return nil
		})
		return result, err
	}
	err = store.references.Write(ctx, func(resolver app.MediaReferenceStore) error {
		deleted, err := resolver.DeleteMediaIfUnreferenced(ctx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return inUse()
		}
		return nil
	})
	if err != nil {
		return DeleteResult{}, err
	}
	result.Deleted = true
	original, err := store.resolveOriginal(value)
	if err != nil || store.removeFile(original) != nil {
		result.CleanupFailures++
		return result, nil
	}
	store.removePreviewArtifacts(value.SHA256)
	return result, nil
}
