package sqlite

import (
	"context"
	"errors"

	"github.com/moment/moment/internal/domain"
)

type personalInteractionTargets struct {
	sourceAccount string
	account       string
	entries       map[string]string
	comments      map[string]string
	notifications map[string]string
	fresh         map[string]bool
}

func (writer *PortableImporter) appendPersonalInteractions(ctx context.Context, source *ArchiveSnapshot, targets personalInteractionTargets) (added, skipped int64, resultErr error) {
	var sequence int64
	if err := writer.tx.QueryRowContext(ctx, `SELECT max(coalesce((SELECT seq FROM sqlite_sequence WHERE name='notifications'),0),coalesce((SELECT max(sequence) FROM notifications),0))`).Scan(&sequence); err != nil {
		return 0, 0, err
	}
	err := source.Interactions(ctx, func(record ArchiveInteraction) error {
		original := record.SubjectID
		if record.Kind == "seen" {
			skipped++
			return nil
		}
		if record.UserID != nil {
			if *record.UserID != targets.sourceAccount {
				return domain.NewError(domain.CodeUnauthorized, "personal interaction cannot transfer another account's authority", nil)
			}
			account := targets.account
			record.UserID = &account
		}
		switch record.Kind {
		case "notification":
			n := *record.Notification
			if n.RecipientID != targets.sourceAccount {
				return domain.NewError(domain.CodeUnauthorized, "personal notification belongs to another account", nil)
			}
			if !targets.fresh["notification:"+n.ID.String()] {
				skipped++
				return nil
			}
			n.ID = domain.ID(targets.notifications[n.ID.String()])
			n.RecipientID = targets.account
			n.Historical = true
			if n.ActorID != nil {
				if *n.ActorID == targets.sourceAccount {
					id := targets.account
					n.ActorID = &id
				} else {
					n.ActorID = nil
				}
			}
			if mapped, ok := targets.entries[n.PostID.String()]; ok {
				n.PostID = domain.ID(mapped)
			} else {
				n.ForceUnavailable = true
			}
			mapping := targets.entries
			if n.Subject.Kind == domain.SubjectComment {
				mapping = targets.comments
			}
			if mapped, ok := mapping[n.Subject.ID.String()]; ok {
				n.Subject.ID = domain.ID(mapped)
			} else {
				n.ForceUnavailable = true
			}
			if n.ReplyToCommentID != nil {
				if mapped, ok := targets.comments[n.ReplyToCommentID.String()]; ok {
					id := domain.ID(mapped)
					n.ReplyToCommentID = &id
				} else {
					n.ReplyToCommentID = nil
				}
			}
			if !n.ForceUnavailable {
				var kind string
				if err := writer.tx.QueryRowContext(ctx, "SELECT kind FROM entries WHERE id=?", n.PostID.String()).Scan(&kind); err != nil {
					return err
				}
				n.ForceUnavailable = kind != string(n.PostKind)
			}
			sequence++
			n.Sequence = sequence
			record.Notification = &n
		case "post_like", "comment_like":
			mapping := targets.entries
			if record.Kind == "comment_like" {
				mapping = targets.comments
			}
			mapped, ok := mapping[original]
			if !ok {
				skipped++
				return nil
			}
			record.SubjectID = mapped
			exists, err := writer.hasInteraction(ctx, record)
			if err != nil {
				return err
			}
			if exists {
				skipped++
				return nil
			}
		case "post_mention", "comment_mention", "reply_recipient":
			mapping, kind := targets.entries, "entry"
			if record.Kind != "post_mention" {
				mapping, kind = targets.comments, "comment"
			}
			mapped, ok := mapping[original]
			if !ok || !targets.fresh[kind+":"+original] {
				skipped++
				return nil
			}
			record.SubjectID = mapped
		default:
			return errors.New("unsupported personal interaction kind")
		}
		if err := writer.InsertInteraction(ctx, record); err != nil {
			return err
		}
		added++
		return nil
	})
	return added, skipped, err
}

func (writer *PortableImporter) hasInteraction(ctx context.Context, record ArchiveInteraction) (bool, error) {
	table, column := "post_likes", "post_id"
	if record.Kind == "comment_like" {
		table, column = "comment_likes", "comment_id"
	}
	query := "SELECT count(*) FROM " + table + " WHERE " + column + "=? AND "
	var actor any
	if record.UserID != nil {
		query += "user_id=?"
		actor = *record.UserID
	} else if record.Snapshot != nil {
		query += "snapshot_key=?"
		actor = record.Snapshot.OriginKey
	} else {
		return false, errors.New("interaction actor is absent")
	}
	var count int
	err := writer.tx.QueryRowContext(ctx, query, record.SubjectID, actor).Scan(&count)
	return count > 0, err
}
