package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/storage/sqlite"
)

const (
	previewResponseLimit = int64(32 << 20)
	pruneGracePeriod     = 24 * time.Hour
)

// Get returns authoritative metadata while preventing prune from invalidating
// the lookup boundary.
func (store *Store) Get(ctx context.Context, id domain.ID) (Media, error) {
	lease, err := store.acquireMedia(ctx, runtimefs.Shared)
	if err != nil {
		return Media{}, fmt.Errorf("coordinate media read: %w", err)
	}
	defer func() { _ = lease.Release() }()
	return store.getLocked(ctx, id)
}

func (store *Store) getLocked(ctx context.Context, id domain.ID) (Media, error) {
	value, found, err := store.findByID(ctx, id)
	if err != nil {
		return Media{}, err
	}
	if !found {
		return Media{}, mediaNotFound(id)
	}
	return value, nil
}

// ReadOriginal returns an explicitly requested immutable original. GIF bytes
// are never selected by preview delivery and are available only through this
// explicit method/route.
func (store *Store) ReadOriginal(ctx context.Context, id domain.ID) (Asset, error) {
	lease, err := store.acquireMedia(ctx, runtimefs.Shared)
	if err != nil {
		return Asset{}, fmt.Errorf("coordinate original delivery: %w", err)
	}
	defer func() { _ = lease.Release() }()
	value, err := store.getLocked(ctx, id)
	if err != nil {
		return Asset{}, err
	}
	contents, err := store.readOriginal(ctx, value)
	if err != nil {
		return Asset{}, err
	}
	return Asset{Media: value, Contents: contents, MIMEType: value.MIMEType, ETag: `"sha256-` + value.SHA256 + `"`}, nil
}

// ReadPreview reads only a complete current-recipe artifact. A cache miss is
// rebuilt through EnsurePreview; errors never fall back to original bytes.
func (store *Store) ReadPreview(ctx context.Context, id domain.ID) (Asset, error) {
	preview, err := store.EnsurePreview(ctx, id)
	if err != nil {
		return Asset{}, err
	}
	lease, err := store.acquireMedia(ctx, runtimefs.Shared)
	if err != nil {
		return Asset{}, fmt.Errorf("coordinate preview delivery: %w", err)
	}
	defer func() { _ = lease.Release() }()
	value, err := store.getLocked(ctx, id)
	if err != nil {
		return Asset{}, err
	}
	if value.SHA256 != preview.SHA256 {
		return Asset{}, domain.NewError(domain.CodeDataIntegrityFailed, "preview cache key does not match media authority", map[string]any{"media_id": id.String()})
	}
	file, err := os.Open(preview.Path)
	if err != nil {
		return Asset{}, domain.NewError(domain.CodeMediaPreviewFailed, "preview cache could not be opened", map[string]any{"media_id": id.String()})
	}
	defer func() { _ = file.Close() }()
	contents, err := readBounded(ctx, file, previewResponseLimit)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Asset{}, err
		}
		return Asset{}, domain.NewError(domain.CodeMediaPreviewFailed, "preview cache is corrupt", map[string]any{"media_id": id.String()})
	}
	return Asset{Contents: contents, MIMEType: preview.MIMEType, ETag: `"preview-` + RecipeVersion + `-` + preview.SHA256 + `"`}, nil
}

// List joins metadata, the resolver, and derived cache status without treating
// a stale preview as authority corruption.
func (store *Store) List(ctx context.Context) (ListResult, error) {
	lease, err := store.acquireMedia(ctx, runtimefs.Shared)
	if err != nil {
		return ListResult{}, fmt.Errorf("coordinate media listing: %w", err)
	}
	defer func() { _ = lease.Release() }()
	return store.listLocked(ctx)
}

func (store *Store) listLocked(ctx context.Context) (ListResult, error) {
	values, err := store.findAll(ctx)
	if err != nil {
		return ListResult{}, err
	}
	states := make(map[domain.ID]app.MediaReferenceState, len(values))
	if err := store.references.Read(ctx, func(resolver app.MediaReferenceStore) error {
		resolved, err := resolver.MediaReferenceStates(ctx)
		if err != nil {
			return err
		}
		for _, state := range resolved {
			states[state.MediaID] = state
		}
		return nil
	}); err != nil {
		return ListResult{}, err
	}
	result := ListResult{RecipeVersion: RecipeVersion, Items: make([]ListItem, 0, len(values))}
	for _, value := range values {
		state, ok := states[value.ID]
		consistent := ok && ((state.Referenced && state.UnreferencedSince == nil) || (!state.Referenced && state.UnreferencedSince != nil))
		result.Items = append(result.Items, ListItem{
			Media:               value,
			Referenced:          state.Referenced,
			ReferenceConsistent: consistent,
			PreviewStatus:       store.previewStatus(value.SHA256),
		})
	}
	return result, nil
}

// Verify checks immutable bytes and reference clock consistency. It completes
// all records so agents receive deterministic diagnostics in one document.
func (store *Store) Verify(ctx context.Context, selected *domain.ID) (VerifyResult, error) {
	lease, err := store.acquireMedia(ctx, runtimefs.Shared)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("coordinate media verification: %w", err)
	}
	defer func() { _ = lease.Release() }()
	listed, err := store.listLocked(ctx)
	if err != nil {
		return VerifyResult{}, err
	}
	result := VerifyResult{}
	foundSelected := selected == nil
	for _, item := range listed.Items {
		if selected != nil && item.Media.ID != *selected {
			continue
		}
		foundSelected = true
		if err := ctx.Err(); err != nil {
			return VerifyResult{}, err
		}
		result.Total++
		verified := VerifyItem{MediaID: item.Media.ID.String(), Status: "healthy"}
		if !item.ReferenceConsistent {
			result.ReferenceInconsistent++
			verified.Status = "reference_inconsistent"
			verified.Code = string(domain.CodeDataIntegrityFailed)
		}
		if _, err := store.readOriginal(ctx, item.Media); err != nil {
			result.Corrupt++
			verified.Status = "corrupt"
			verified.Code = string(domain.CodeMediaCorrupt)
		} else if item.ReferenceConsistent {
			result.Healthy++
		}
		result.Items = append(result.Items, verified)
	}
	if !foundSelected {
		return VerifyResult{}, mediaNotFound(*selected)
	}
	return result, nil
}

// RebuildPreviews repairs only derived cache artifacts. A dry run performs no
// writes; actual work is deterministic, bounded by Store preview limits, and
// safely resumable after cancellation.
func (store *Store) RebuildPreviews(ctx context.Context, dryRun bool) (RebuildResult, error) {
	mediaLease, err := store.acquireMaintenanceAndMedia(ctx, runtimefs.Shared)
	if err != nil {
		return RebuildResult{}, fmt.Errorf("coordinate preview rebuild: %w", err)
	}
	values, err := store.findAll(ctx)
	if err != nil {
		_ = mediaLease.Release()
		return RebuildResult{}, err
	}
	result := RebuildResult{RecipeVersion: RecipeVersion, Total: len(values), DryRun: dryRun}
	statuses := make([]string, len(values))
	for index, value := range values {
		statuses[index] = store.previewStatus(value.SHA256)
		switch statuses[index] {
		case "ready":
			result.Ready++
		case "stale":
			result.Stale++
			result.EstimatedAdditionalBytes += value.ByteSize
		default:
			result.Missing++
			result.EstimatedAdditionalBytes += value.ByteSize
		}
	}
	if err := mediaLease.Release(); err != nil {
		return RebuildResult{}, err
	}
	if dryRun {
		return result, nil
	}
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return RebuildResult{}, err
		}
		if statuses[index] == "ready" {
			continue
		}
		if _, err := store.EnsurePreview(ctx, value.ID); err != nil {
			return RebuildResult{}, err
		}
		result.Rebuilt++
	}
	return result, nil
}

// Prune removes only media that the resolver rechecks as continuously
// unreferenced for at least 24 hours inside the destructive transaction. Rows
// commit before originals are removed, so an injected filesystem failure can
// leave only a recoverable orphan blob, never a row pointing at missing bytes.
func (store *Store) Prune(ctx context.Context, now time.Time, dryRun bool) (PruneResult, error) {
	mode := runtimefs.Exclusive
	if dryRun {
		mode = runtimefs.Shared
	}
	leases, err := store.acquireMaintenanceAndMedia(ctx, mode)
	if err != nil {
		return PruneResult{}, fmt.Errorf("coordinate media prune: %w", err)
	}
	defer func() { _ = leases.Release() }()
	cutoff := domain.NormalizeTime(now).Add(-pruneGracePeriod)
	var candidateIDs []domain.ID
	if err := store.references.Read(ctx, func(resolver app.MediaReferenceStore) error {
		var err error
		candidateIDs, err = resolver.PrunableMediaIDs(ctx, cutoff)
		return err
	}); err != nil {
		return PruneResult{}, err
	}
	result := PruneResult{Eligible: len(candidateIDs), DryRun: dryRun, Items: make([]PruneItem, 0, len(candidateIDs))}
	candidates := make([]Media, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		value, err := store.getLocked(ctx, id)
		if err != nil {
			return PruneResult{}, err
		}
		if value.UnreferencedSince == nil {
			return PruneResult{}, domain.NewError(domain.CodeDataIntegrityFailed, "prunable media has no unreferenced clock", map[string]any{"media_id": id.String()})
		}
		candidates = append(candidates, value)
		result.Items = append(result.Items, PruneItem{MediaID: id.String(), SHA256: value.SHA256, ByteSize: value.ByteSize, UnreferencedSince: *value.UnreferencedSince})
	}
	if dryRun {
		return result, nil
	}
	for _, value := range candidates {
		if err := ctx.Err(); err != nil {
			return PruneResult{}, err
		}
		if store.beforePruneDelete != nil {
			if err := store.beforePruneDelete(); err != nil {
				return PruneResult{}, err
			}
		}
		deleted := false
		if err := store.references.Write(ctx, func(resolver app.MediaReferenceStore) error {
			var err error
			deleted, err = resolver.DeleteMediaIfPrunable(ctx, value.ID, cutoff)
			return err
		}); err != nil {
			return PruneResult{}, err
		}
		if !deleted {
			continue
		}
		result.Deleted++
		original, err := store.resolveOriginal(value)
		if err != nil || store.removeFile(original) != nil {
			result.CleanupFailures++
			continue
		}
		result.BytesReclaimed += value.ByteSize
		store.removePreviewArtifacts(value.SHA256)
	}
	return result, nil
}

func (store *Store) findAll(ctx context.Context) ([]Media, error) {
	var values []Media
	err := store.db.ReadTx(ctx, func(queries sqlite.Queries) error {
		rows, err := queries.QueryContext(ctx, `
SELECT id, sha256, storage_path, original_name, mime_type, byte_size,
       width, height, created_at_ms, unreferenced_since_ms
FROM media
ORDER BY id`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			value, err := scanMedia(rows)
			if err != nil {
				return err
			}
			values = append(values, value)
		}
		return rows.Err()
	})
	return values, err
}

type mediaScanner interface{ Scan(...any) error }

func scanMedia(row mediaScanner) (Media, error) {
	var value Media
	var raw string
	var width, height, createdAt int64
	var unreferenced sql.NullInt64
	if err := row.Scan(&raw, &value.SHA256, &value.StoragePath, &value.OriginalName,
		&value.MIMEType, &value.ByteSize, &width, &height, &createdAt, &unreferenced); err != nil {
		return Media{}, err
	}
	id, err := domain.ParseID(raw)
	if err != nil {
		return Media{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored media ID is invalid", nil)
	}
	if width <= 0 || height <= 0 {
		return Media{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored media dimensions are invalid", map[string]any{"media_id": raw})
	}
	value.ID = id
	value.Width, value.Height = uint32(width), uint32(height)
	value.CreatedAt = time.UnixMilli(createdAt).UTC()
	if unreferenced.Valid {
		clock := time.UnixMilli(unreferenced.Int64).UTC()
		value.UnreferencedSince = &clock
	}
	value.AnimatedOriginal = value.MIMEType == "image/gif"
	return value, nil
}

func (store *Store) previewStatus(sha string) string {
	if _, ok := store.existingPreview(sha); ok {
		return "ready"
	}
	current := filepath.Join(store.runtime.Root, "cache", "previews", RecipeVersion, sha[:2])
	if matches, _ := filepath.Glob(filepath.Join(current, sha+".*")); len(matches) > 0 {
		return "stale"
	}
	root := filepath.Join(store.runtime.Root, "cache", "previews")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == RecipeVersion || strings.Contains(entry.Name(), string(filepath.Separator)) {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, entry.Name(), sha[:2], sha+".*"))
		if len(matches) > 0 {
			return "stale"
		}
	}
	return "missing"
}

func (store *Store) removePreviewArtifacts(sha string) {
	root := filepath.Join(store.runtime.Root, "cache", "previews")
	versions, _ := os.ReadDir(root)
	for _, version := range versions {
		if !version.IsDir() {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, version.Name(), sha[:2], sha+".*"))
		for _, match := range matches {
			_ = store.removeFile(match)
		}
	}
}

func mediaNotFound(id domain.ID) error {
	return domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", id), map[string]any{"media_id": id.String()})
}
