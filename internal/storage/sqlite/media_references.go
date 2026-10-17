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
//
// Avatars and covers are read from users rather than site_settings because
// 0022 moved them there. Every account's picture has to appear here, not just
// the owner's: a name missing from this predicate is not a stale query, it is
// a file the collector is entitled to delete.
//
// An article contributes three ways: its cover, a draft's cover, and the
// pictures a draft body places. Its published body pictures arrive through
// entry_media like any other post's, which is the reason they are projected
// there at all. Note what is deliberately absent: none of the draft rows
// appear in the visibility query below. Being referenced by a draft keeps a
// file alive; it does not make it public, because a draft is not published.
const mediaReferencedSQL = `(
    EXISTS (SELECT 1 FROM entry_media AS usage WHERE usage.media_id = media.id)
    OR EXISTS (SELECT 1 FROM comment_media AS usage WHERE usage.media_id = media.id)
    OR EXISTS (
        SELECT 1 FROM users AS account WHERE account.avatar_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM users AS account WHERE account.cover_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM entry_link_card AS card
        WHERE card.thumbnail_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM entry_link_card AS card
        WHERE card.favicon_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM entry_article AS article
        WHERE article.cover_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM article_drafts AS draft
        WHERE draft.cover_media_id = media.id
    )
    OR EXISTS (
        SELECT 1 FROM article_draft_media AS usage
        WHERE usage.media_id = media.id
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
FROM (
    SELECT media_id
    FROM entry_media
    WHERE entry_id = ?
    UNION ALL
    -- The cover is the one article picture that is not an entry_media row.
    SELECT cover_media_id
    FROM entry_article
    WHERE entry_id = ? AND cover_media_id IS NOT NULL
    UNION ALL
    SELECT comment_media.media_id
    FROM comment_media
    INNER JOIN comments ON comments.id = comment_media.comment_id
    WHERE comments.entry_id = ?
)
ORDER BY media_id`, entryID.String(), entryID.String(), entryID.String())
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

// DeleteMediaIfUnreferenced is an explicit selected delete, without the GC grace
// period. All reference sources still participate in the transaction predicate.
func (store mediaReferenceStore) DeleteMediaIfUnreferenced(ctx context.Context, id domain.ID) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `DELETE FROM media WHERE id = ? AND NOT `+mediaReferencedSQL, id.String())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// Visibility reports the widest audience that may fetch a media file, derived
// from what references it.
//
// A file inherits the audience of the posts it appears in: through an entry's
// own media, through a comment under that entry, or through a link card the
// entry carries. One public reference is enough to make it public. Avatars and
// covers are always public. A file nothing references yet -- an upload the
// composer is still holding -- is public too, because it always has been and
// a member previewing their own upload has no other standing to fetch it by.
//
// Only a file every reference of which sits under a hidden post is narrowed
// to the owner.
func (repository *MediaReferenceRepository) audience(ctx context.Context, id domain.ID, viewer app.Viewer) (bool, bool, error) {
	public, publicArguments := visibilityPredicate(viewer, "entry.visibility")
	if viewer.UserID() != "" {
		public = "((" + public + ") OR (entry.kind = 'article' AND entry.publication_state = 'unpublished' AND entry.author_user_id = ?))"
		publicArguments = append(publicArguments, viewer.UserID())
	}
	var referenced, exposed int
	err := repository.transactions.ReadTx(ctx, func(queries Queries) error {
		arguments := []any{id.String()}
		// One pass per EXISTS block below that joins entries and applies the
		// visibility predicate. Adding a block without adding a pass here
		// shifts every argument after it.
		for range 5 {
			arguments = append(arguments, id.String())
			arguments = append(arguments, publicArguments...)
		}
		arguments = append(arguments, id.String(), id.String(), id.String())
		account := viewer.UserID()
		draftAuthor := "0 = 1"
		if account != "" {
			draftAuthor = "draft.author_user_id = ?"
			arguments = append(arguments, account)
		}
		arguments = append(arguments, account)
		return queries.QueryRowContext(ctx, `
WITH media_id_value(id) AS (VALUES (?))
SELECT
  EXISTS (SELECT 1 FROM media WHERE media.id = media_id_value.id AND `+mediaReferencedSQL+`),
  (
    EXISTS (
      SELECT 1 FROM entry_media AS usage
      JOIN entries AS entry ON entry.id = usage.entry_id
      WHERE usage.media_id = ? AND `+public+`
    )
    OR EXISTS (
      SELECT 1 FROM comment_media AS usage
      JOIN comments AS comment ON comment.id = usage.comment_id
      JOIN entries AS entry ON entry.id = comment.entry_id
      WHERE usage.media_id = ? AND `+public+`
    )
    OR EXISTS (
      SELECT 1 FROM entry_link_card AS card
      JOIN entries AS entry ON entry.id = card.entry_id
      WHERE card.thumbnail_media_id = ? AND `+public+`
    )
    OR EXISTS (
      SELECT 1 FROM entry_link_card AS card
      JOIN entries AS entry ON entry.id = card.entry_id
      WHERE card.favicon_media_id = ? AND `+public+`
    )
    OR EXISTS (
      SELECT 1 FROM entry_article AS article
      JOIN entries AS entry ON entry.id = article.entry_id
      WHERE article.cover_media_id = ? AND `+public+`
    )
    OR EXISTS (SELECT 1 FROM users AS account WHERE account.avatar_media_id = ? AND account.status='active')
    OR EXISTS (SELECT 1 FROM users AS account WHERE account.cover_media_id = ? AND account.status='active')
    OR EXISTS (SELECT 1 FROM article_drafts AS draft WHERE (draft.cover_media_id = ? OR EXISTS (SELECT 1 FROM article_draft_media AS usage WHERE usage.draft_id = draft.id AND usage.media_id = media_id_value.id)) AND `+draftAuthor+`)
  OR EXISTS(SELECT 1 FROM media_grants AS access WHERE access.media_id=media_id_value.id AND access.user_id=?)
  ) FROM media_id_value`, arguments...).Scan(&referenced, &exposed)
	})
	if err != nil {
		return false, false, fmt.Errorf("resolve media visibility: %w", err)
	}
	return referenced == 1, exposed == 1, nil
}

// Visibility projects the public audience for HTTP cache policy.
func (repository *MediaReferenceRepository) Visibility(ctx context.Context, id domain.ID) (domain.Visibility, error) {
	_, exposed, err := repository.audience(ctx, id, app.PublicViewer())
	if err != nil {
		return "", err
	}
	if !exposed {
		return domain.VisibilityOnlyMe, nil
	}
	return domain.VisibilityEveryone, nil
}

// CanRead checks references against the actual account, including private drafts.
func (repository *MediaReferenceRepository) CanRead(ctx context.Context, id domain.ID, viewer app.Viewer) (bool, error) {
	_, exposed, err := repository.audience(ctx, id, viewer)
	return exposed, err
}
