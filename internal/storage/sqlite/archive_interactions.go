package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/domain"
)

// ArchiveInteraction is one portable interaction relation or addressed event.
type ArchiveInteraction struct {
	Kind            string                           `json:"kind"`
	SubjectID       string                           `json:"subject_id,omitempty"`
	UserID          *string                          `json:"user_id,omitempty"`
	Handle          string                           `json:"handle,omitempty"`
	Notified        bool                             `json:"notified,omitempty"`
	CreatedAtMS     int64                            `json:"created_at_ms,omitempty"`
	Snapshot        *domain.ActivityIdentitySnapshot `json:"snapshot,omitempty"`
	ActorName       string                           `json:"-"`
	ActorUsername   string                           `json:"-"`
	Notification    *domain.Notification             `json:"notification,omitempty"`
	Scope           string                           `json:"scope,omitempty"`
	ThroughSequence int64                            `json:"through_sequence,omitempty"`
}

const interactionCountSQL = `(SELECT count(*) FROM post_likes)+(SELECT count(*) FROM comment_likes)+(SELECT count(*) FROM entry_mentions)+(SELECT count(*) FROM comment_mentions)+(SELECT count(*) FROM comment_reply_recipients)+(SELECT count(*) FROM notifications)+(SELECT count(*) FROM notification_seen)`

// Interactions streams all authoritative relations from the same archive snapshot.
func (snapshot *ArchiveSnapshot) Interactions(ctx context.Context, yield func(ArchiveInteraction) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	for _, item := range []struct{ kind, table, column string }{{"post_like", "post_likes", "post_id"}, {"comment_like", "comment_likes", "comment_id"}, {"reply_recipient", "comment_reply_recipients", "comment_id"}} {
		at := "0"
		if item.kind != "reply_recipient" {
			at = "r.created_at_ms"
		}
		rows, err := snapshot.tx.QueryContext(ctx, "SELECT r."+item.column+",r.user_id,r.snapshot_key,r.snapshot_name,r.snapshot_username,"+at+",coalesce(u.display_name,''),coalesce(u.username,'') FROM "+item.table+" r LEFT JOIN users u ON u.id=r.user_id ORDER BY r."+item.column+",coalesce(r.user_id,r.snapshot_key)")
		if err != nil {
			return err
		}
		for rows.Next() {
			r := ArchiveInteraction{Kind: item.kind}
			var id, key, name, username sql.NullString
			if err := rows.Scan(&r.SubjectID, &id, &key, &name, &username, &r.CreatedAtMS, &r.ActorName, &r.ActorUsername); err != nil {
				_ = rows.Close()
				return err
			}
			if id.Valid {
				value := id.String
				r.UserID = &value
			}
			if key.Valid {
				r.Snapshot = &domain.ActivityIdentitySnapshot{OriginKey: key.String, DisplayName: name.String, Username: username.String}
				r.ActorName = ""
				r.ActorUsername = ""
			}
			if err := yield(r); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	for _, item := range []struct{ kind, table, column string }{{"post_mention", "entry_mentions", "entry_id"}, {"comment_mention", "comment_mentions", "comment_id"}} {
		rows, err := snapshot.tx.QueryContext(ctx, "SELECT r."+item.column+",r.handle,r.user_id,r.notified,r.snapshot_key,r.snapshot_name,r.snapshot_username,coalesce(u.display_name,''),coalesce(u.username,'') FROM "+item.table+" r LEFT JOIN users u ON u.id=r.user_id ORDER BY r."+item.column+",r.handle")
		if err != nil {
			return err
		}
		for rows.Next() {
			r := ArchiveInteraction{Kind: item.kind}
			var id, key, name, username sql.NullString
			if err := rows.Scan(&r.SubjectID, &r.Handle, &id, &r.Notified, &key, &name, &username, &r.ActorName, &r.ActorUsername); err != nil {
				_ = rows.Close()
				return err
			}
			if id.Valid {
				value := id.String
				r.UserID = &value
			}
			if key.Valid {
				r.Snapshot = &domain.ActivityIdentitySnapshot{OriginKey: key.String, DisplayName: name.String, Username: username.String}
				r.ActorName = ""
				r.ActorUsername = ""
			}
			if err := yield(r); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	rows, err := snapshot.tx.QueryContext(ctx, "SELECT "+notificationColumns+" FROM notifications ORDER BY sequence")
	if err != nil {
		return err
	}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		if err := yield(ArchiveInteraction{Kind: "notification", Notification: &n}); err != nil {
			_ = rows.Close()
			return err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	rows, err = snapshot.tx.QueryContext(ctx, "SELECT user_id,scope,through_sequence FROM notification_seen ORDER BY user_id,scope")
	if err != nil {
		return err
	}
	for rows.Next() {
		r := ArchiveInteraction{Kind: "seen"}
		var id string
		if err := rows.Scan(&id, &r.Scope, &r.ThroughSequence); err != nil {
			_ = rows.Close()
			return err
		}
		r.UserID = &id
		if err := yield(r); err != nil {
			_ = rows.Close()
			return err
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

// InsertInteraction restores records directly without generating new activities.
func (importer *PortableImporter) InsertInteraction(ctx context.Context, r ArchiveInteraction) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	var err error
	key, name, username := snapshotSQLValues(r.Snapshot)
	switch r.Kind {
	case "post_like", "comment_like", "reply_recipient":
		table, column := "post_likes", "post_id"
		if r.Kind == "comment_like" {
			table, column = "comment_likes", "comment_id"
		}
		if r.Kind == "reply_recipient" {
			table, column = "comment_reply_recipients", "comment_id"
		}
		columns := column + ",user_id,snapshot_key,snapshot_name,snapshot_username"
		marks := "?,?,?,?,?"
		args := []any{r.SubjectID, nullableString(r.UserID), key, name, username}
		if r.Kind != "reply_recipient" {
			columns += ",created_at_ms"
			marks += ",?"
			args = append(args, r.CreatedAtMS)
		}
		_, err = importer.tx.ExecContext(ctx, "INSERT INTO "+table+" ("+columns+") VALUES ("+marks+")", args...)
	case "post_mention", "comment_mention":
		table, column := "entry_mentions", "entry_id"
		if r.Kind == "comment_mention" {
			table, column = "comment_mentions", "comment_id"
		}
		_, err = importer.tx.ExecContext(ctx, "INSERT INTO "+table+" ("+column+",handle,user_id,notified,snapshot_key,snapshot_name,snapshot_username) VALUES (?,?,?,?,?,?,?)", r.SubjectID, r.Handle, nullableString(r.UserID), r.Notified, key, name, username)
	case "notification":
		if r.Notification == nil {
			return errors.New("notification payload is required")
		}
		n := r.Notification
		_, err = importer.tx.ExecContext(ctx, `INSERT INTO notifications(sequence,id,recipient_id,actor_id,actor_name,event_type,subject_kind,subject_id,post_id,post_kind,reply_to_comment_id,reasons,occurred_at_ms,historical,force_unavailable) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, n.Sequence, n.ID.String(), n.RecipientID, nullableString(n.ActorID), n.ActorName, n.EventType, n.Subject.Kind, n.Subject.ID.String(), n.PostID.String(), string(n.PostKind), nullableCommentID(n.ReplyToCommentID), int(n.Reasons), domain.TimeToMillis(n.OccurredAt), n.Historical, n.ForceUnavailable)
	case "seen":
		_, err = importer.tx.ExecContext(ctx, "INSERT INTO notification_seen(user_id,scope,through_sequence) VALUES (?,?,?)", nullableString(r.UserID), r.Scope, r.ThroughSequence)
	default:
		return errors.New("unsupported interaction kind")
	}
	if err != nil {
		return fmt.Errorf("restore %s interaction: %w", r.Kind, err)
	}
	importer.interactions++
	return nil
}
