package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/oklog/ulid/v2"
)

// AccountImportResult reports only additions. Existing content is never replaced.
type AccountImportResult struct {
	Added   ArchiveCounts `json:"added"`
	Skipped ArchiveCounts `json:"skipped"`
}

// AppendAccountSnapshot applies a validated personal snapshot in one transaction.
// The source account is mapped to the selected target; foreign authors remain
// display snapshots. Stable target-bound IDs make retries preserve local edits.
func (db *Database) AppendAccountSnapshot(ctx context.Context, source *ArchiveSnapshot, instanceID, sourceAccount, target string, copyMedia func(ArchiveMedia, ArchiveMedia) error) (result AccountImportResult, resultErr error) {
	writer, err := db.BeginPortableImport(ctx)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, writer.Close()) }()
	var status string
	if err = writer.tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id=?`, target).Scan(&status); err != nil {
		return result, err
	}
	if status != "active" {
		return result, domain.NewError(domain.CodeUnauthorized, "an active account is required for personal import", nil)
	}
	mediaIDs := map[string]string{}
	entries := map[string]string{}
	comments := map[string]string{}
	drafts := map[string]string{}
	fresh := map[string]bool{}
	allocate := func(kind, table, id string, mapping map[string]string) error {
		// Preserve portable identifiers when possible. Collisions use stable,
		// target-bound identifiers, so retries also preserve local edits after a
		// site backup/restore without needing an additional provenance ledger.
		parsed, err := ulid.ParseStrict(id)
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(instanceID + ":" + sourceAccount + ":" + target + ":" + kind + ":" + id))
		var key ulid.ULID
		copy(key[:6], parsed[:6])
		copy(key[6:], digest[:10])
		candidate := key.String()
		query := `SELECT author_user_id FROM ` + table + ` WHERE id=?`
		if kind == "comment" {
			query = `SELECT e.author_user_id FROM comments c JOIN entries e ON e.id=c.entry_id WHERE c.id=?`
		}
		lookup := func(local string) (string, bool, error) {
			var author string
			err := writer.tx.QueryRowContext(ctx, query, local).Scan(&author)
			if errors.Is(err, sql.ErrNoRows) {
				return "", false, nil
			}
			return author, err == nil, err
		}
		author, exists, err := lookup(candidate)
		if err != nil {
			return err
		}
		if exists {
			if author != target {
				return domain.NewError(domain.CodeUnauthorized, "personal import identifier belongs to another account", nil)
			}
			mapping[id] = candidate
			return nil
		}
		author, exists, err = lookup(id)
		if err != nil {
			return err
		}
		if !exists {
			mapping[id] = id
			fresh[kind+":"+id] = true
			return nil
		}
		if author == target {
			mapping[id] = id
			return nil
		}
		mapping[id] = candidate
		fresh[kind+":"+id] = true
		return nil
	}
	if err = source.Entries(ctx, func(v ArchiveEntry) error { return allocate("entry", "entries", v.ID, entries) }); err != nil {
		return result, err
	}
	if err = source.Comments(ctx, func(v ArchiveComment) error { return allocate("comment", "comments", v.ID, comments) }); err != nil {
		return result, err
	}
	if err = source.ArticleDrafts(ctx, func(v ArchiveArticleDraft) error { return allocate("draft", "article_drafts", v.ID, drafts) }); err != nil {
		return result, err
	}
	if err = source.Media(ctx, func(v ArchiveMedia) error {
		var id string
		existing := v
		err := writer.tx.QueryRowContext(ctx, `SELECT id,storage_path,mime_type,byte_size,width,height FROM media WHERE sha256=?`, v.SHA256).Scan(&id, &existing.StoragePath, &existing.MIMEType, &existing.ByteSize, &existing.Width, &existing.Height)
		if errors.Is(err, sql.ErrNoRows) {
			output := v
			parsed, parseErr := domain.ParseID(v.ID)
			if parseErr != nil {
				return parseErr
			}
			mediaHash := sha256.Sum256([]byte(instanceID + ":" + sourceAccount + ":" + target + ":media:" + parsed.String()))
			var mediaKey ulid.ULID
			copy(mediaKey[:], mediaHash[:16])
			output.ID = mediaKey.String()
			output.Grants = nil
			output.UnreferencedSinceMS = nil
			if err = copyMedia(v, output); err != nil {
				return err
			}
			if err = writer.InsertMedia(ctx, output); err != nil {
				return err
			}
			id = output.ID
			result.Added.Media++
		} else if err != nil {
			return err
		} else {
			if existing.MIMEType != v.MIMEType || existing.ByteSize != v.ByteSize || existing.Width != v.Width || existing.Height != v.Height {
				return domain.NewError(domain.CodeDataIntegrityFailed, "existing media metadata disagrees with the personal original", nil)
			}
			existing.ID = id
			if err := copyMedia(v, existing); err != nil {
				return err
			}
			result.Skipped.Media++
		}
		mediaIDs[v.ID] = id
		_, err = writer.tx.ExecContext(ctx, `INSERT INTO media_grants(user_id,media_id,created_at_ms) VALUES(?,?,?) ON CONFLICT(user_id,media_id) DO NOTHING`, target, id, time.Now().UnixMilli())
		return err
	}); err != nil {
		return result, err
	}
	pointer := func(v *string, mapping map[string]string) *string {
		if v == nil {
			return nil
		}
		out, ok := mapping[*v]
		if !ok {
			return nil
		}
		return &out
	}
	usages := func(values []ArchiveUsage) {
		for i := range values {
			values[i].MediaID = mediaIDs[values[i].MediaID]
		}
	}
	if err = source.Entries(ctx, func(v ArchiveEntry) error {
		original := v.ID
		if !fresh["entry:"+original] {
			result.Skipped.Entries++
			if v.Kind == "article" {
				result.Skipped.Articles++
			}
			return nil
		}
		v.ID = entries[original]
		v.AuthorUserID = target
		usages(v.Media)
		v.QuotedEntryID = pointer(v.QuotedEntryID, entries)
		v.QuotedCommentID = pointer(v.QuotedCommentID, comments)
		for i := range v.LinkCards {
			card := &v.LinkCards[i].Card
			if card.ThumbnailMediaID != "" {
				card.ThumbnailMediaID = mediaIDs[card.ThumbnailMediaID]
			}
			if card.FaviconMediaID != "" {
				card.FaviconMediaID = mediaIDs[card.FaviconMediaID]
			}
		}
		var err error
		if v.Kind == "article" {
			var body ArchiveEntryArticle
			var cover sql.NullString
			if err := source.tx.QueryRowContext(ctx, `SELECT entry_id,title,body_json,body_schema_version,cover_media_id FROM entry_article WHERE entry_id=?`, original).Scan(&body.EntryID, &body.Title, &body.BodyJSON, &body.SchemaVersion, &cover); err != nil {
				return err
			}
			if cover.Valid {
				body.CoverMediaID = &cover.String
			}
			body.EntryID = v.ID
			body.CoverMediaID = pointer(body.CoverMediaID, mediaIDs)
			body.BodyJSON, err = remapPersonalArticle(body.BodyJSON, mediaIDs, false)
			if err == nil {
				err = writer.InsertArticleEntry(ctx, v, body)
			}
			result.Added.Articles++
		} else {
			err = writer.InsertEntry(ctx, v)
		}
		if err == nil {
			result.Added.Entries++
		}
		return err
	}); err != nil {
		return result, err
	}
	if err = source.Comments(ctx, func(v ArchiveComment) error {
		original := v.ID
		if !fresh["comment:"+original] {
			result.Skipped.Comments++
			return nil
		}
		v.ID = comments[original]
		v.EntryID = entries[v.EntryID]
		v.ParentID = pointer(v.ParentID, comments)
		v.ReplyToCommentID = pointer(v.ReplyToCommentID, comments)
		if v.AuthorUserID != nil {
			if *v.AuthorUserID != sourceAccount {
				return errors.New("personal comment names an unrelated account")
			}
			v.AuthorUserID = &target
		}
		if v.AuthorSnapshot != nil {
			snapshot := *v.AuthorSnapshot
			snapshot.AvatarMediaID = pointer(snapshot.AvatarMediaID, mediaIDs)
			v.AuthorSnapshot = &snapshot
		}
		usages(v.Media)
		if err := writer.InsertComment(ctx, v); err != nil {
			return err
		}
		result.Added.Comments++
		return nil
	}); err != nil {
		return result, err
	}
	if err = source.ArticleDrafts(ctx, func(v ArchiveArticleDraft) error {
		original := v.ID
		if !fresh["draft:"+original] {
			result.Skipped.ArticleDrafts++
			return nil
		}
		v.ID = drafts[original]
		v.AuthorUserID = target
		v.CoverMediaID = pointer(v.CoverMediaID, mediaIDs)
		v.PublishedEntryID = pointer(v.PublishedEntryID, entries)
		usages(v.Media)
		var err error
		v.BodyJSON, err = remapPersonalArticle(v.BodyJSON, mediaIDs, true)
		if err != nil {
			return err
		}
		if err = writer.InsertArticleDraft(ctx, v); err != nil {
			return err
		}
		result.Added.ArticleDrafts++
		return nil
	}); err != nil {
		return result, err
	}
	if _, err = writer.tx.ExecContext(ctx, `UPDATE search_index_state SET revision=revision+1 WHERE id=1`); err != nil {
		return result, err
	}
	if err = writer.commit(ctx, result.Added, true); err != nil {
		return result, err
	}
	return result, nil
}

func remapPersonalArticle(raw string, mediaIDs map[string]string, draft bool) (string, error) {
	var doc domain.ArticleDocument
	var err error
	if draft {
		doc, err = domain.ParseArticleDraftDocument([]byte(raw))
	} else {
		doc, err = domain.ParseArticleDocument([]byte(raw))
	}
	if err != nil {
		return "", err
	}
	var visit func([]domain.ArticleNode) error
	visit = func(nodes []domain.ArticleNode) error {
		for i := range nodes {
			n := &nodes[i]
			if n.MediaID != "" {
				mapped, ok := mediaIDs[n.MediaID.String()]
				if !ok {
					return errors.New("article references absent media")
				}
				n.MediaID, err = domain.ParseID(mapped)
				if err != nil {
					return err
				}
			}
			if err := visit(n.Content); err != nil {
				return err
			}
		}
		return nil
	}
	if err = visit(doc.Content); err != nil {
		return "", err
	}
	out, err := domain.MarshalArticleDocument(doc)
	return string(out), err
}
