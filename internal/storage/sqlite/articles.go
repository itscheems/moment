package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// InsertArticle writes the body of an article beside the entry that carries it
// in the timeline, and projects the pictures the body places into entry_media.
//
// The projection is derived, not authored: the body is the only place a
// picture's position is recorded. It exists so that reference counting,
// reclamation and media visibility keep working as one SQL predicate over rows
// rather than having to parse a document in three more places.
func (store postStore) InsertArticle(ctx context.Context, entryID domain.ID, article domain.Article) error {
	body, err := domain.MarshalArticleDocument(article.Body)
	if err != nil {
		return err
	}
	if _, err := store.queries.ExecContext(ctx, `
INSERT INTO entry_article (entry_id, title, body_json, body_schema_version, cover_media_id, excerpt)
VALUES (?, ?, ?, ?, ?, ?)`,
		entryID.String(), article.Title, string(body),
		article.Body.SchemaVersion, optionalMediaID(article.CoverID), article.Excerpt(),
	); err != nil {
		return fmt.Errorf("insert article: %w", err)
	}
	return store.replaceArticleMedia(ctx, entryID, article)
}

// ReplaceArticle rewrites the whole body at once. An article is edited as a
// document, so there is no partial update to express.
func (store postStore) ReplaceArticle(ctx context.Context, entryID domain.ID, article domain.Article) error {
	body, err := domain.MarshalArticleDocument(article.Body)
	if err != nil {
		return err
	}
	result, err := store.queries.ExecContext(ctx, `
UPDATE entry_article
SET title = ?, body_json = ?, body_schema_version = ?, cover_media_id = ?, excerpt = ?
WHERE entry_id = ?`,
		article.Title, string(body), article.Body.SchemaVersion,
		optionalMediaID(article.CoverID), article.Excerpt(), entryID.String(),
	)
	if err != nil {
		return fmt.Errorf("update article: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update article: %w", err)
	}
	if affected == 0 {
		return domain.NewError(domain.CodePostNotFound, "article does not exist", map[string]any{"id": entryID.String()})
	}
	return store.replaceArticleMedia(ctx, entryID, article)
}

// UpdateArticleEntry moves the timeline row of an article: its derived text,
// its visibility and its timestamps. The body is replaced separately because
// it is rewritten as a whole document, not patched.
func (store postStore) UpdateArticleEntry(ctx context.Context, entry domain.Entry, contentText string) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `
UPDATE entries
SET content_text = ?, visibility = ?, updated_at_ms = ?, edited_at_ms = ?
WHERE id = ? AND kind = ?`,
		contentText, string(entry.Visibility), domain.TimeToMillis(entry.UpdatedAt),
		optionalTimeMillis(entry.EditedAt), entry.ID.String(), string(domain.KindArticle))
	if err != nil {
		return false, fmt.Errorf("update article entry: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update article entry: %w", err)
	}
	return changed == 1, nil
}

func (store postStore) replaceArticleMedia(ctx context.Context, entryID domain.ID, article domain.Article) error {
	usages := article.InlineMedia()
	if err := domain.ValidateArticleMediaUsages(usages); err != nil {
		return err
	}
	return store.replaceEntryMedia(ctx, entryID, usages, domain.MaxArticleMediaBytes)
}

// ArticleByEntryID reads a stored body back through the same grammar that let
// it in. A row that no longer parses is a data integrity failure, not an
// article that renders differently.
func (store postStore) ArticleByEntryID(ctx context.Context, entryID domain.ID) (domain.Article, bool, error) {
	var (
		title    string
		bodyJSON string
		version  int
		cover    sql.NullString
		excerpt  string
	)
	err := store.queries.QueryRowContext(ctx, `
SELECT title, body_json, body_schema_version, cover_media_id, excerpt
FROM entry_article
WHERE entry_id = ?`, entryID.String()).Scan(&title, &bodyJSON, &version, &cover, &excerpt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Article{}, false, nil
	}
	if err != nil {
		return domain.Article{}, false, fmt.Errorf("query article: %w", err)
	}

	body, err := domain.ParseArticleDocument([]byte(bodyJSON))
	if err != nil {
		return domain.Article{}, false, domain.NewError(domain.CodeDataIntegrityFailed,
			"stored article body no longer parses", map[string]any{"id": entryID.String()})
	}
	var coverID *domain.ID
	if cover.Valid {
		parsed, parseErr := domain.ParseID(cover.String)
		if parseErr != nil {
			return domain.Article{}, false, domain.NewError(domain.CodeDataIntegrityFailed,
				"stored article cover has an invalid ID", map[string]any{"id": entryID.String()})
		}
		coverID = &parsed
	}
	article, err := domain.NewArticle(title, body, coverID)
	if err != nil {
		return domain.Article{}, false, domain.NewError(domain.CodeDataIntegrityFailed,
			"stored article is not valid", map[string]any{"id": entryID.String()})
	}
	return article, true, nil
}

// ArticleCards answers what a list needs and nothing else. A timeline page
// holding twenty full bodies would ship megabytes nobody reads.
func (store postStore) ArticleCards(ctx context.Context, ids []domain.ID) (map[domain.ID]app.ArticleCard, error) {
	cards := make(map[domain.ID]app.ArticleCard, len(ids))
	if len(ids) == 0 {
		return cards, nil
	}
	placeholders := make([]byte, 0, len(ids)*2)
	arguments := make([]any, 0, len(ids))
	for index, id := range ids {
		if index > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		arguments = append(arguments, id.String())
	}
	rows, err := store.queries.QueryContext(ctx, `
SELECT entry_id, title, excerpt, cover_media_id
FROM entry_article
WHERE entry_id IN (`+string(placeholders)+`)`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query article cards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			entryID string
			card    app.ArticleCard
			cover   sql.NullString
		)
		if err := rows.Scan(&entryID, &card.Title, &card.Excerpt, &cover); err != nil {
			return nil, fmt.Errorf("scan article card: %w", err)
		}
		id, err := domain.ParseID(entryID)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored article has an invalid entry ID", nil)
		}
		if cover.Valid {
			coverID, coverErr := domain.ParseID(cover.String)
			if coverErr != nil {
				return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored article cover has an invalid ID", nil)
			}
			card.CoverMediaID = &coverID
		}
		cards[id] = card
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read article cards: %w", err)
	}
	return cards, nil
}

func optionalMediaID(id *domain.ID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
