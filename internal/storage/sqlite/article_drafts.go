package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const articleDraftColumns = `author_user_id, id, title, body_json, body_schema_version, cover_media_id,
       published_entry_id, created_at_ms, updated_at_ms`

func (store postStore) InsertArticleDraft(ctx context.Context, record app.ArticleDraftRecord) error {
	if err := store.checkMediaPresent(ctx, record.Draft.CoverID); err != nil {
		return err
	}
	body, err := domain.MarshalArticleDocument(record.Draft.Body)
	if err != nil {
		return err
	}
	if _, err := store.queries.ExecContext(ctx, `
INSERT INTO article_drafts (`+articleDraftColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		optionalAuthorID(record.AuthorUserID), record.ID.String(), record.Draft.Title, string(body), record.Draft.Body.SchemaVersion,
		optionalMediaID(record.Draft.CoverID), optionalEntryID(record.PublishedEntryID),
		domain.TimeToMillis(record.CreatedAt), domain.TimeToMillis(record.UpdatedAt),
	); err != nil {
		return fmt.Errorf("insert article draft: %w", err)
	}
	return store.replaceArticleDraftMedia(ctx, record.ID, record.Draft)
}

// UpdateArticleDraft writes over a draft only if nobody else has since the
// caller read it. Two tabs editing the same draft is ordinary, and the loser
// should be told rather than have its words disappear, so a stale write
// answers false instead of overwriting.
func (store postStore) UpdateArticleDraft(ctx context.Context, record app.ArticleDraftRecord, expected time.Time) (bool, error) {
	if err := store.checkMediaPresent(ctx, record.Draft.CoverID); err != nil {
		return false, err
	}
	body, err := domain.MarshalArticleDocument(record.Draft.Body)
	if err != nil {
		return false, err
	}
	result, err := store.queries.ExecContext(ctx, `
UPDATE article_drafts
SET title = ?, body_json = ?, body_schema_version = ?, cover_media_id = ?,
    published_entry_id = ?, updated_at_ms = ?
WHERE id = ? AND updated_at_ms = ?`,
		record.Draft.Title, string(body), record.Draft.Body.SchemaVersion,
		optionalMediaID(record.Draft.CoverID), optionalEntryID(record.PublishedEntryID),
		domain.TimeToMillis(record.UpdatedAt), record.ID.String(), domain.TimeToMillis(expected),
	)
	if err != nil {
		return false, fmt.Errorf("update article draft: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update article draft: %w", err)
	}
	if affected == 0 {
		return false, nil
	}
	return true, store.replaceArticleDraftMedia(ctx, record.ID, record.Draft)
}

func (store postStore) ArticleDraftByID(ctx context.Context, id domain.ID) (app.ArticleDraftRecord, bool, error) {
	return store.scanArticleDraft(store.queries.QueryRowContext(ctx, `
SELECT `+articleDraftColumns+`
FROM article_drafts
WHERE id = ?`, id.String()))
}

func (store postStore) ArticleDraftForEntry(ctx context.Context, entryID domain.ID) (app.ArticleDraftRecord, bool, error) {
	return store.scanArticleDraft(store.queries.QueryRowContext(ctx, `
SELECT `+articleDraftColumns+`
FROM article_drafts
WHERE published_entry_id = ?`, entryID.String()))
}

func (store postStore) ListArticleDrafts(ctx context.Context, limit int) ([]app.ArticleDraftRecord, error) {
	if limit <= 0 {
		limit = app.MaxArticleDrafts
	}
	rows, err := store.queries.QueryContext(ctx, `
SELECT `+articleDraftColumns+`
FROM article_drafts
ORDER BY updated_at_ms DESC, id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list article drafts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]app.ArticleDraftRecord, 0, limit)
	for rows.Next() {
		record, _, err := store.scanArticleDraft(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read article drafts: %w", err)
	}
	return records, nil
}

func (store postStore) CountArticleDrafts(ctx context.Context) (int, error) {
	var count int
	if err := store.queries.QueryRowContext(ctx, `SELECT count(*) FROM article_drafts`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count article drafts: %w", err)
	}
	return count, nil
}

func (store postStore) DeleteArticleDraft(ctx context.Context, id domain.ID) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `DELETE FROM article_drafts WHERE id = ?`, id.String())
	if err != nil {
		return false, fmt.Errorf("delete article draft: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete article draft: %w", err)
	}
	return affected > 0, nil
}

func (store postStore) replaceArticleDraftMedia(ctx context.Context, draftID domain.ID, draft domain.ArticleDraft) error {
	usages := draft.InlineMedia()
	if err := domain.ValidateArticleMediaUsages(usages); err != nil {
		return err
	}
	if err := store.checkMediaUsages(ctx, usages, domain.MaxArticleMediaBytes); err != nil {
		return err
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM article_draft_media WHERE draft_id = ?`, draftID.String()); err != nil {
		return fmt.Errorf("clear article draft media: %w", err)
	}
	for _, usage := range usages {
		var alt any
		if usage.AltText != nil {
			alt = *usage.AltText
		}
		if _, err := store.queries.ExecContext(ctx, `
INSERT INTO article_draft_media (draft_id, media_id, position, alt_text)
VALUES (?, ?, ?, ?)`, draftID.String(), usage.MediaID.String(), usage.Position, alt); err != nil {
			return fmt.Errorf("insert article draft media: %w", err)
		}
	}
	return nil
}

func (store postStore) scanArticleDraft(scanner entryScanner) (app.ArticleDraftRecord, bool, error) {
	var (
		author    sql.NullString
		idString  string
		title     string
		bodyJSON  string
		version   int
		cover     sql.NullString
		published sql.NullString
		createdAt int64
		updatedAt int64
	)
	if err := scanner.Scan(&author, &idString, &title, &bodyJSON, &version, &cover, &published, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return app.ArticleDraftRecord{}, false, nil
		}
		return app.ArticleDraftRecord{}, false, fmt.Errorf("scan article draft: %w", err)
	}
	id, err := domain.ParseID(idString)
	if err != nil {
		return app.ArticleDraftRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed, "stored article draft has an invalid ID", nil)
	}
	// A draft is read through the grammar that let it in, minus the rule that
	// it has to say something: an empty draft is still a draft.
	body, err := domain.ParseArticleDraftDocument([]byte(bodyJSON))
	if err != nil {
		return app.ArticleDraftRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed,
			"stored article draft no longer parses", map[string]any{"id": idString})
	}
	var coverID *domain.ID
	if cover.Valid {
		parsed, parseErr := domain.ParseID(cover.String)
		if parseErr != nil {
			return app.ArticleDraftRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed, "stored draft cover has an invalid ID", nil)
		}
		coverID = &parsed
	}
	draft, err := domain.NewArticleDraft(title, body, coverID)
	if err != nil {
		return app.ArticleDraftRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed,
			"stored article draft is not valid", map[string]any{"id": idString})
	}
	record := app.ArticleDraftRecord{
		AuthorUserID: author.String, ID: id,
		Draft:     draft,
		CreatedAt: domain.TimeFromMillis(createdAt),
		UpdatedAt: domain.TimeFromMillis(updatedAt),
	}
	if published.Valid {
		entryID, parseErr := domain.ParseID(published.String)
		if parseErr != nil {
			return app.ArticleDraftRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed, "stored draft names an invalid entry", nil)
		}
		record.PublishedEntryID = &entryID
	}
	return record, true, nil
}

func optionalEntryID(id *domain.ID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
