package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// mediaReferencedSQL is the one SQLite definition of v0 reachability. Every
// resolver operation below composes this predicate; no consumer owns a local
// entry_media/avatar query.
const mediaReferencedSQL = `(
    EXISTS (SELECT 1 FROM entry_media AS usage WHERE usage.media_id = media.id)
    OR EXISTS (
        SELECT 1 FROM site_settings AS site
        WHERE site.id = 1 AND site.avatar_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM site_settings AS site
        WHERE site.id = 1 AND site.cover_media_id = media.id
    )
)`

// MediaReferenceRepository exposes the central resolver through transaction-
// bound application ports. Lock ownership remains with the calling use case.
type MediaReferenceRepository struct{ transactions Transactor }

// NewMediaReferenceRepository binds the resolver to SQLite transactions.
func NewMediaReferenceRepository(transactions Transactor) (*MediaReferenceRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	return &MediaReferenceRepository{transactions: transactions}, nil
}

func (repository *MediaReferenceRepository) Read(ctx context.Context, operation func(app.MediaReferenceStore) error) error {
	if operation == nil {
		return errors.New("media reference read operation is required")
	}
	return repository.transactions.ReadTx(ctx, func(queries Queries) error {
		return operation(newMediaReferenceStore(queries))
	})
}

func (repository *MediaReferenceRepository) Write(ctx context.Context, operation func(app.MediaReferenceStore) error) error {
	if operation == nil {
		return errors.New("media reference write operation is required")
	}
	return repository.transactions.WriteTx(ctx, func(queries Queries) error {
		return operation(newMediaReferenceStore(queries))
	})
}

type mediaReferenceStore struct{ queries Queries }

func newMediaReferenceStore(queries Queries) mediaReferenceStore {
	return mediaReferenceStore{queries: queries}
}

var _ app.MediaReferenceStore = mediaReferenceStore{}

func (store mediaReferenceStore) ResolveMediaReferences(ctx context.Context) ([]domain.ID, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT media.id
FROM media
WHERE `+mediaReferencedSQL+`
ORDER BY media.id`)
	if err != nil {
		return nil, fmt.Errorf("resolve media references: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []domain.ID
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan media reference: %w", err)
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored media ID is invalid", nil)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media references: %w", err)
	}
	return ids, nil
}

func (store mediaReferenceStore) MediaReferenceStates(ctx context.Context) ([]app.MediaReferenceState, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT media.id, `+mediaReferencedSQL+`, media.unreferenced_since_ms
FROM media
ORDER BY media.id`)
	if err != nil {
		return nil, fmt.Errorf("read media reference states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var states []app.MediaReferenceState
	for rows.Next() {
		var raw string
		var referenced int
		var unreferenced sql.NullInt64
		if err := rows.Scan(&raw, &referenced, &unreferenced); err != nil {
			return nil, fmt.Errorf("scan media reference state: %w", err)
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored media ID is invalid", nil)
		}
		state := app.MediaReferenceState{MediaID: id, Referenced: referenced == 1}
		if unreferenced.Valid {
			value := time.UnixMilli(unreferenced.Int64).UTC()
			state.UnreferencedSince = &value
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media reference states: %w", err)
	}
	return states, nil
}

func (store mediaReferenceStore) ReconcileMediaReferences(ctx context.Context, ids []domain.ID, now time.Time) error {
	unique := append([]domain.ID(nil), ids...)
	sort.Slice(unique, func(left, right int) bool { return unique[left] < unique[right] })
	nowMS := domain.TimeToMillis(domain.NormalizeTime(now))
	var previous domain.ID
	for index, id := range unique {
		if index > 0 && id == previous {
			continue
		}
		previous = id
		result, err := store.queries.ExecContext(ctx, `
UPDATE media
SET unreferenced_since_ms = CASE
    WHEN `+mediaReferencedSQL+` THEN NULL
    ELSE COALESCE(unreferenced_since_ms, ?)
END
WHERE media.id = ?`, nowMS, id.String())
		if err != nil {
			return fmt.Errorf("reconcile media reference %s: %w", id, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect media reference reconciliation: %w", err)
		}
		if changed != 1 {
			return domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", id), map[string]any{"media_id": id.String()})
		}
	}
	return nil
}

func (store mediaReferenceStore) EntryMediaIDs(ctx context.Context, entryID domain.ID) ([]domain.ID, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT DISTINCT media_id
FROM entry_media
WHERE entry_id = ?
ORDER BY media_id`, entryID.String())
	if err != nil {
		return nil, fmt.Errorf("read entry media references: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []domain.ID
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan entry media reference: %w", err)
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry media ID is invalid", nil)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry media references: %w", err)
	}
	return ids, nil
}

func (store mediaReferenceStore) PrunableMediaIDs(ctx context.Context, cutoff time.Time) ([]domain.ID, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT media.id
FROM media
WHERE NOT `+mediaReferencedSQL+`
  AND media.unreferenced_since_ms IS NOT NULL
  AND media.unreferenced_since_ms <= ?
ORDER BY media.id`, domain.TimeToMillis(domain.NormalizeTime(cutoff)))
	if err != nil {
		return nil, fmt.Errorf("read prunable media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []domain.ID
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan prunable media: %w", err)
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored media ID is invalid", nil)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prunable media: %w", err)
	}
	return ids, nil
}

func (store mediaReferenceStore) DeleteMediaIfPrunable(ctx context.Context, id domain.ID, cutoff time.Time) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `
DELETE FROM media
WHERE media.id = ?
  AND NOT `+mediaReferencedSQL+`
  AND media.unreferenced_since_ms IS NOT NULL
  AND media.unreferenced_since_ms <= ?`, id.String(), domain.TimeToMillis(domain.NormalizeTime(cutoff)))
	if err != nil {
		return false, fmt.Errorf("delete prunable media %s: %w", id, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect prunable media deletion: %w", err)
	}
	return changed == 1, nil
}
