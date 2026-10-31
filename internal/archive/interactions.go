package archive

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const interactionMember = "data/interactions.jsonl"
const maxPortableInteractions = int64(2_000_000)

func supportedSiteFormat(version int) bool { return version >= 2 && version <= FormatVersion }

func interactionKey(r sqlitestore.ArchiveInteraction) string {
	actor := ""
	if r.UserID != nil {
		actor = "user:" + *r.UserID
	}
	if r.Snapshot != nil {
		actor = "snapshot:" + r.Snapshot.OriginKey
	}
	switch r.Kind {
	case "notification":
		if r.Notification != nil {
			return r.Kind + ":" + r.Notification.ID.String()
		}
	case "seen":
		return r.Kind + ":" + actor + ":" + r.Scope
	case "post_mention", "comment_mention":
		return r.Kind + ":" + r.SubjectID + ":" + r.Handle
	default:
		return r.Kind + ":" + r.SubjectID + ":" + actor
	}
	return r.Kind
}

func validateInteraction(r sqlitestore.ArchiveInteraction) error {
	if r.UserID != nil {
		if err := domain.ValidateUserID(*r.UserID); err != nil {
			return err
		}
	}
	if r.Snapshot != nil {
		if r.UserID != nil {
			return errors.New("a display snapshot cannot have local account authority")
		}
		parts := strings.Split(r.Snapshot.OriginKey, ":")
		if len(parts) != 2 || len(parts[0]) != 32 || len(parts[1]) != 32 || !isLowerHex(parts[0]) || !isLowerHex(parts[1]) {
			return errors.New("invalid historical activity identity")
		}
		if _, err := domain.NormalizeCommentAuthorName(r.Snapshot.DisplayName); err != nil {
			return err
		}
		if r.Snapshot.Username != "" {
			if err := domain.ValidateStoredUsername(r.Snapshot.Username); err != nil {
				return err
			}
		}
	}
	if r.SubjectID != "" {
		if _, err := domain.ParseID(r.SubjectID); err != nil {
			return err
		}
	}
	switch r.Kind {
	case "post_like", "comment_like", "reply_recipient":
		if r.SubjectID == "" || r.UserID == nil && r.Snapshot == nil {
			return errors.New("interaction target and actor are required")
		}
		if r.Kind != "reply_recipient" && (r.CreatedAtMS < 1 || domain.TimeFromMillis(r.CreatedAtMS).Year() > 9999) {
			return errors.New("invalid like timestamp")
		}
	case "post_mention", "comment_mention":
		if r.SubjectID == "" {
			return errors.New("mention source is required")
		}
		if err := domain.ValidateStoredUsername(r.Handle); err != nil {
			return err
		}
		if r.Handle != strings.ToLower(r.Handle) {
			return errors.New("mention handle must be canonical")
		}
	case "seen":
		if r.UserID == nil || r.ThroughSequence < 0 {
			return errors.New("invalid notification view position")
		}
		_, err := domain.NotificationScopeMask(r.Scope)
		return err
	case "notification":
		if r.Notification == nil {
			return errors.New("notification record is required")
		}
		n := r.Notification
		if _, err := domain.ParseID(n.ID.String()); err != nil {
			return err
		}
		if n.Sequence < 1 {
			return errors.New("notification sequence must be positive")
		}
		if err := domain.ValidateUserID(n.RecipientID); err != nil {
			return err
		}
		if n.ActorID != nil {
			if err := domain.ValidateUserID(*n.ActorID); err != nil {
				return err
			}
		}
		if _, err := domain.NormalizeCommentAuthorName(n.ActorName); err != nil {
			return err
		}
		if err := n.Subject.Validate(); err != nil {
			return err
		}
		if _, err := domain.ParseID(n.PostID.String()); err != nil {
			return err
		}
		if n.ReplyToCommentID != nil {
			if _, err := domain.ParseID(n.ReplyToCommentID.String()); err != nil {
				return err
			}
		}
		if n.PostKind != domain.KindMoment && n.PostKind != domain.KindArticle {
			return errors.New("invalid notification post kind")
		}
		if n.Reasons < 1 || n.Reasons > 15 || n.OccurredAt.UnixMilli() < 1 || n.OccurredAt.Year() > 9999 || !n.OccurredAt.Equal(domain.NormalizeTime(n.OccurredAt)) {
			return errors.New("invalid notification reasons or time")
		}
		switch n.EventType {
		case "post.created", "post.updated", "post.published":
			if n.Subject.Kind != domain.SubjectPost || n.Reasons != domain.ReasonMention {
				return errors.New("invalid post mention event")
			}
		case "comment.created":
			if n.Subject.Kind != domain.SubjectComment || n.Reasons&domain.ReasonLike != 0 {
				return errors.New("invalid comment event")
			}
		case "post.liked", "comment.liked":
			if n.EventType != n.Subject.Kind+".liked" || n.Reasons != domain.ReasonLike {
				return errors.New("invalid like event")
			}
		default:
			return errors.New("unsupported notification event type")
		}
	default:
		return errors.New("unsupported interaction kind")
	}
	return nil
}

func readInteractions(ctx context.Context, files map[string]*zip.File, document manifest, yield func(sqlitestore.ArchiveInteraction) error) (int64, error) {
	file := files[interactionMember]
	if file == nil {
		if document.FormatVersion >= 6 {
			return 0, errors.New("interaction member is required by this format")
		}
		return 0, nil
	}
	if document.FormatVersion < 6 {
		return 0, errors.New("interaction member requires archive format 6")
	}
	return validateJSONLines[sqlitestore.ArchiveInteraction](ctx, file, []string{"kind"}, func(r sqlitestore.ArchiveInteraction, _ map[string]json.RawMessage) error {
		if err := validateInteraction(r); err != nil {
			return err
		}
		return yield(r)
	})
}

func validatePortableInteractions(ctx context.Context, files map[string]*zip.File, document manifest, entries map[string]string, comments map[string]string) error {
	if document.Counts.Interactions < 0 || document.Counts.Interactions > maxPortableInteractions {
		return errors.New("interaction count exceeds portable limits")
	}
	users := make(map[string]bool)
	if _, err := validateJSONLines[userRecord](ctx, files["data/users.jsonl"], []string{"id"}, func(u userRecord, _ map[string]json.RawMessage) error { users[u.ID] = true; return nil }); err != nil {
		return err
	}
	keys := make(map[string]bool)
	sequences := make(map[int64]bool)
	latest := make(map[string]int64)
	seen := make([]sqlitestore.ArchiveInteraction, 0)
	var scanned int64
	count, err := readInteractions(ctx, files, document, func(r sqlitestore.ArchiveInteraction) error {
		scanned++
		if scanned > document.Counts.Interactions {
			return errors.New("interaction records exceed manifest count")
		}
		key := interactionKey(r)
		if keys[key] {
			return errors.New("duplicate interaction record")
		}
		keys[key] = true
		if r.UserID != nil && !users[*r.UserID] {
			return errors.New("interaction refers to a missing account")
		}
		switch r.Kind {
		case "post_like", "post_mention":
			if _, ok := entries[r.SubjectID]; !ok {
				return errors.New("interaction refers to a missing post")
			}
		case "comment_like", "comment_mention", "reply_recipient":
			if _, ok := comments[r.SubjectID]; !ok {
				return errors.New("interaction refers to a missing comment")
			}
		case "notification":
			n := r.Notification
			if !users[n.RecipientID] || n.ActorID != nil && !users[*n.ActorID] {
				return errors.New("notification refers to a missing account")
			}
			if sequences[n.Sequence] {
				return errors.New("duplicate notification sequence")
			}
			sequences[n.Sequence] = true
			if n.Sequence > latest[n.RecipientID] {
				latest[n.RecipientID] = n.Sequence
			}
			if kind, exists := entries[n.PostID.String()]; exists && !n.ForceUnavailable && kind != string(n.PostKind) {
				return errors.New("notification post kind disagrees with its source")
			}
			if n.Subject.Kind == domain.SubjectPost && n.Subject.ID != n.PostID {
				return errors.New("notification post reference disagrees with its target")
			}
			if post, ok := comments[n.Subject.ID.String()]; !n.ForceUnavailable && n.Subject.Kind == domain.SubjectComment && ok && post != n.PostID.String() {
				return errors.New("notification comment belongs to a different post")
			}
		case "seen":
			seen = append(seen, r)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if count != document.Counts.Interactions {
		return errors.New("manifest interaction count does not match portable records")
	}
	for _, r := range seen {
		if r.ThroughSequence > latest[*r.UserID] {
			return errors.New("view position exceeds this account's notification history")
		}
	}
	return nil
}

func exportInteractions(ctx context.Context, protocol *protocolWriter, snapshot *sqlitestore.ArchiveSnapshot) (int64, error) {
	return protocol.jsonLines(interactionMember, func(write func(any) error) error {
		return snapshot.Interactions(ctx, func(r sqlitestore.ArchiveInteraction) error {
			if err := validateInteraction(r); err != nil {
				return err
			}
			return write(r)
		})
	})
}

func personalInteraction(record sqlitestore.ArchiveInteraction, account, instance string, entries, comments map[string]bool) (sqlitestore.ArchiveInteraction, bool) {
	switch record.Kind {
	case "notification":
		if record.Notification == nil || record.Notification.RecipientID != account {
			return record, false
		}
		n := *record.Notification
		if n.ActorID != nil && *n.ActorID != account {
			n.ActorID = nil
		}
		if !entries[n.PostID.String()] || n.Subject.Kind == domain.SubjectComment && !comments[n.Subject.ID.String()] {
			n.ForceUnavailable = true
		}
		record.Notification = &n
	case "seen":
		if record.UserID == nil || *record.UserID != account {
			return record, false
		}
	case "post_like", "post_mention":
		if !entries[record.SubjectID] {
			return record, false
		}
	default:
		if !comments[record.SubjectID] {
			return record, false
		}
	}
	if record.UserID != nil && *record.UserID != account {
		record.Snapshot = &domain.ActivityIdentitySnapshot{OriginKey: instance + ":" + *record.UserID, DisplayName: record.ActorName, Username: record.ActorUsername}
		record.UserID = nil
	}
	record.ActorName = ""
	record.ActorUsername = ""
	return record, true
}
