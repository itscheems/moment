package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// InteractionRepository binds interaction use cases to one authoritative generation.
type InteractionRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	generation   string
}

// NewInteractionRepository coordinates ordinary writes through the runtime locker.
func NewInteractionRepository(transactions Transactor, locks *runtimefs.Locker, generation string) (*InteractionRepository, error) {
	if transactions == nil || locks == nil || generation == "" {
		return nil, errors.New("interaction transactions, runtime locks and generation are required")
	}
	return &InteractionRepository{transactions: transactions, locks: locks, generation: generation}, nil
}

func (repository *InteractionRepository) Read(ctx context.Context, operation func(app.InteractionStore) error) error {
	return repository.transactions.ReadTx(ctx, func(q Queries) error {
		return operation(interactionStore{activityStore: activityStore{queries: q}, generation: repository.generation})
	})
}

func (repository *InteractionRepository) Write(ctx context.Context, operation func(app.InteractionStore) error) error {
	lease, err := repository.locks.Acquire(ctx, runtimefs.MaintenanceLock, runtimefs.Shared)
	if err != nil {
		return err
	}
	defer func() { _ = lease.Release() }()
	return repository.transactions.WriteTx(ctx, func(q Queries) error {
		return operation(interactionStore{activityStore: activityStore{queries: q}, generation: repository.generation})
	})
}

type activityStore struct{ queries Queries }
type interactionStore struct {
	activityStore
	generation string
}

func snapshotSQLValues(snapshot *domain.ActivityIdentitySnapshot) (any, any, any) {
	if snapshot == nil {
		return nil, nil, nil
	}
	var username any
	if snapshot.Username != "" {
		username = snapshot.Username
	}
	return snapshot.OriginKey, snapshot.DisplayName, username
}

var _ app.InteractionTransactions = (*InteractionRepository)(nil)
var _ app.InteractionStore = interactionStore{}

func (store interactionStore) GenerationID() string { return store.generation }

func uniqueSQLValues(values []string) (string, []any) {
	args := make([]any, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			args = append(args, value)
		}
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), args
}

func (store activityStore) ActivityAccounts(ctx context.Context, ids []string) (map[string]app.ActivityAccount, error) {
	return store.activityAccounts(ctx, "id", ids)
}
func (store activityStore) MentionAccounts(ctx context.Context, handles []string) (map[string]app.ActivityAccount, error) {
	return store.activityAccounts(ctx, "username", handles)
}
func (store activityStore) activityAccounts(ctx context.Context, column string, values []string) (map[string]app.ActivityAccount, error) {
	result := make(map[string]app.ActivityAccount)
	for start := 0; start < len(values); start += 500 {
		end := min(start+500, len(values))
		marks, args := uniqueSQLValues(values[start:end])
		if len(args) == 0 {
			continue
		}
		rows, err := store.queries.QueryContext(ctx, "SELECT id,username,display_name,avatar_media_id,status FROM users WHERE "+column+" IN ("+marks+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a app.ActivityAccount
			var avatar sql.NullString
			var status string
			if err := rows.Scan(&a.UserID, &a.Username, &a.DisplayName, &avatar, &status); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if avatar.Valid {
				value := avatar.String
				a.AvatarMediaID = &value
			}
			a.Active = status == string(domain.AccountActive)
			key := a.UserID
			if column == "username" {
				key = a.Username
				if !a.Active {
					continue
				}
			}
			result[key] = a
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
	}
	return result, nil
}

func (store activityStore) ActivitySource(ctx context.Context, ref domain.ContentRef) (app.ActivitySource, bool, error) {
	sources, err := store.ActivitySources(ctx, []domain.ContentRef{ref})
	if err != nil {
		return app.ActivitySource{}, false, err
	}
	source, ok := sources[ref]
	return source, ok, nil
}

func (store activityStore) ActivitySources(ctx context.Context, refs []domain.ContentRef) (map[domain.ContentRef]app.ActivitySource, error) {
	result := make(map[domain.ContentRef]app.ActivitySource)
	for _, kind := range []string{domain.SubjectPost, domain.SubjectComment} {
		ids := make([]string, 0, len(refs))
		for _, ref := range refs {
			if ref.Kind == kind {
				ids = append(ids, ref.ID.String())
			}
		}
		for start := 0; start < len(ids); start += 500 {
			marks, args := uniqueSQLValues(ids[start:min(start+500, len(ids))])
			if len(args) == 0 {
				continue
			}
			query := `SELECT e.id,e.id,e.author_user_id,e.kind,e.publication_state,e.visibility,e.author_user_id,CASE WHEN e.kind='article' THEN trim(a.title||' '||a.excerpt) ELSE coalesce(e.content_md,'') END,NULL FROM entries e LEFT JOIN entry_article a ON a.entry_id=e.id WHERE e.id IN (` + marks + ")"
			if kind == domain.SubjectComment {
				query = `SELECT c.id,e.id,e.author_user_id,e.kind,e.publication_state,e.visibility,c.author_user_id,c.content_md,c.reply_to_comment_id FROM comments c JOIN entries e ON e.id=c.entry_id WHERE c.id IN (` + marks + ")"
			}
			rows, err := store.queries.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id, post, entryAuthor, entryKind, publication, visibility, body string
				var author, reply sql.NullString
				if err := rows.Scan(&id, &post, &entryAuthor, &entryKind, &publication, &visibility, &author, &body, &reply); err != nil {
					_ = rows.Close()
					return nil, err
				}
				ref := domain.ContentRef{Kind: kind, ID: domain.ID(id)}
				source := app.ActivitySource{Ref: ref, Entry: domain.Entry{ID: domain.ID(post), AuthorUserID: entryAuthor, Kind: domain.EntryKind(entryKind), PublicationState: publication, Visibility: domain.Visibility(visibility)}, AuthorID: author.String, BodyPreview: body}
				source.PreviewIsPlain = kind == domain.SubjectPost && source.Entry.Kind == domain.KindArticle
				if reply.Valid {
					value := domain.ID(reply.String)
					source.ReplyToCommentID = &value
				}
				result[ref] = source
			}
			err = rows.Err()
			closeErr := rows.Close()
			if err != nil || closeErr != nil {
				return nil, errors.Join(err, closeErr)
			}
		}
	}
	return result, nil
}

func mentionTable(kind string) (string, string, error) {
	switch kind {
	case domain.SubjectPost:
		return "entry_mentions", "entry_id", nil
	case domain.SubjectComment:
		return "comment_mentions", "comment_id", nil
	default:
		return "", "", domain.NewError(domain.CodeInvalidArgument, "invalid mention source", nil)
	}
}

func (store activityStore) MentionBindings(ctx context.Context, ref domain.ContentRef) ([]domain.MentionBinding, error) {
	table, column, err := mentionTable(ref.Kind)
	if err != nil {
		return nil, err
	}
	rows, err := store.queries.QueryContext(ctx, "SELECT handle,user_id,notified,snapshot_key,snapshot_name,snapshot_username FROM "+table+" WHERE "+column+"=? ORDER BY handle", ref.ID.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]domain.MentionBinding, 0)
	for rows.Next() {
		var b domain.MentionBinding
		var id sql.NullString
		var key, name, username sql.NullString
		if err := rows.Scan(&b.Handle, &id, &b.Notified, &key, &name, &username); err != nil {
			return nil, err
		}
		if id.Valid {
			value := id.String
			b.UserID = &value
		}
		if key.Valid {
			b.Snapshot = &domain.ActivityIdentitySnapshot{OriginKey: key.String, DisplayName: name.String, Username: username.String}
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func (store activityStore) ReplaceMentionBindings(ctx context.Context, ref domain.ContentRef, bindings []domain.MentionBinding) error {
	table, column, err := mentionTable(ref.Kind)
	if err != nil {
		return err
	}
	if _, err := store.queries.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+"=?", ref.ID.String()); err != nil {
		return err
	}
	for _, b := range bindings {
		key, name, username := snapshotSQLValues(b.Snapshot)
		if _, err := store.queries.ExecContext(ctx, "INSERT INTO "+table+" ("+column+",handle,user_id,notified,snapshot_key,snapshot_name,snapshot_username) VALUES (?,?,?,?,?,?,?)", ref.ID.String(), b.Handle, nullableString(b.UserID), b.Notified, key, name, username); err != nil {
			return err
		}
	}
	return nil
}

func (store activityStore) ReplyRecipientIDs(ctx context.Context, id domain.ID) ([]string, error) {
	rows, err := store.queries.QueryContext(ctx, "SELECT user_id FROM comment_reply_recipients WHERE comment_id=? AND user_id IS NOT NULL ORDER BY user_id", id.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (store activityStore) ReplaceReplyRecipientIDs(ctx context.Context, id domain.ID, ids []string) error {
	if _, err := store.queries.ExecContext(ctx, "DELETE FROM comment_reply_recipients WHERE comment_id=?", id.String()); err != nil {
		return err
	}
	for _, userID := range ids {
		if _, err := store.queries.ExecContext(ctx, "INSERT INTO comment_reply_recipients(comment_id,user_id) VALUES (?,?)", id.String(), userID); err != nil {
			return err
		}
	}
	return nil
}

func (store activityStore) InsertNotification(ctx context.Context, n domain.Notification) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO notifications(id,recipient_id,actor_id,actor_name,event_type,subject_kind,subject_id,post_id,post_kind,reply_to_comment_id,reasons,occurred_at_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, n.ID.String(), n.RecipientID, nullableString(n.ActorID), n.ActorName, n.EventType, n.Subject.Kind, n.Subject.ID.String(), n.PostID.String(), string(n.PostKind), nullableCommentID(n.ReplyToCommentID), int(n.Reasons), domain.TimeToMillis(n.OccurredAt))
	return err
}

func likeTable(kind string) (string, string, error) {
	switch kind {
	case domain.SubjectPost:
		return "post_likes", "post_id", nil
	case domain.SubjectComment:
		return "comment_likes", "comment_id", nil
	default:
		return "", "", domain.NewError(domain.CodeInvalidArgument, "invalid like target", nil)
	}
}

func (store interactionStore) SetLike(ctx context.Context, ref domain.ContentRef, user string, active bool, at time.Time) (bool, error) {
	table, column, err := likeTable(ref.Kind)
	if err != nil {
		return false, err
	}
	query := "DELETE FROM " + table + " WHERE " + column + "=? AND user_id=?"
	args := []any{ref.ID.String(), user}
	if active {
		query = "INSERT INTO " + table + " (" + column + ",user_id,created_at_ms) VALUES (?,?,?) ON CONFLICT DO NOTHING"
		args = append(args, domain.TimeToMillis(at))
	}
	result, err := store.queries.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (store activityStore) LikeStates(ctx context.Context, kind string, ids []domain.ID, viewer string) (map[domain.ID]domain.LikeState, error) {
	table, column, err := likeTable(kind)
	if err != nil {
		return nil, err
	}
	result := make(map[domain.ID]domain.LikeState)
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, id.String())
	}
	for start := 0; start < len(values); start += 500 {
		marks, args := uniqueSQLValues(values[start:min(start+500, len(values))])
		if len(args) == 0 {
			continue
		}
		args = append([]any{viewer}, args...)
		rows, err := store.queries.QueryContext(ctx, "SELECT "+column+",count(*),max(CASE WHEN user_id=? THEN 1 ELSE 0 END) FROM "+table+" WHERE "+column+" IN ("+marks+") GROUP BY "+column, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var state domain.LikeState
			if err := rows.Scan(&id, &state.Count, &state.LikedByMe); err != nil {
				_ = rows.Close()
				return nil, err
			}
			result[domain.ID(id)] = state
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
	}
	return result, nil
}

const notificationColumns = `sequence,id,recipient_id,actor_id,actor_name,event_type,subject_kind,subject_id,post_id,post_kind,reply_to_comment_id,reasons,occurred_at_ms,historical,force_unavailable`

func scanNotification(row interface{ Scan(...any) error }) (domain.Notification, error) {
	var n domain.Notification
	var actor, reply sql.NullString
	var identifier, subject, post, kind string
	var reason int
	var occurred int64
	if err := row.Scan(&n.Sequence, &identifier, &n.RecipientID, &actor, &n.ActorName, &n.EventType, &n.Subject.Kind, &subject, &post, &kind, &reply, &reason, &occurred, &n.Historical, &n.ForceUnavailable); err != nil {
		return n, err
	}
	if actor.Valid {
		value := actor.String
		n.ActorID = &value
	}
	if reply.Valid {
		value := domain.ID(reply.String)
		n.ReplyToCommentID = &value
	}
	n.ID = domain.ID(identifier)
	n.Subject.ID = domain.ID(subject)
	n.PostID = domain.ID(post)
	n.PostKind = domain.EntryKind(kind)
	n.Reasons = domain.NotificationReason(reason)
	n.OccurredAt = domain.TimeFromMillis(occurred)
	return n, nil
}

func (store interactionStore) Notifications(ctx context.Context, q app.NotificationQuery) ([]domain.Notification, error) {
	query := "SELECT " + notificationColumns + " FROM notifications WHERE recipient_id=? AND (reasons & ?) != 0"
	args := []any{q.RecipientID, int(q.Mask)}
	if q.Newer {
		query += " AND historical=0 AND sequence>?"
		args = append(args, q.Sequence)
	} else if q.Sequence > 0 {
		query += " AND (occurred_at_ms<? OR (occurred_at_ms=? AND sequence<?))"
		args = append(args, q.OccurredAtMS, q.OccurredAtMS, q.Sequence)
	}
	if q.Newer {
		query += " ORDER BY sequence ASC"
	} else {
		query += " ORDER BY occurred_at_ms DESC,sequence DESC"
	}
	query += " LIMIT ?"
	args = append(args, q.Limit)
	rows, err := store.queries.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]domain.Notification, 0)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (store interactionStore) NotificationByID(ctx context.Context, recipient string, id domain.ID) (domain.Notification, bool, error) {
	n, err := scanNotification(store.queries.QueryRowContext(ctx, "SELECT "+notificationColumns+" FROM notifications WHERE recipient_id=? AND id=?", recipient, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Notification{}, false, nil
	}
	return n, err == nil, err
}

func (store interactionStore) NotificationProgress(ctx context.Context, recipient string) (app.NotificationProgress, error) {
	var progress app.NotificationProgress
	if err := store.queries.QueryRowContext(ctx, `SELECT coalesce((SELECT max(sequence) FROM notifications WHERE recipient_id=?),0),coalesce((SELECT through_sequence FROM notification_seen WHERE user_id=? AND scope='all'),0),coalesce((SELECT through_sequence FROM notification_seen WHERE user_id=? AND scope='mentions'),0)`, recipient, recipient, recipient).Scan(&progress.Latest, &progress.AllSeen, &progress.MentionsSeen); err != nil {
		return progress, err
	}
	err := store.queries.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN (n.reasons & 7)!=0 THEN 1 ELSE 0 END),0)
FROM notifications n JOIN entries e ON e.id=n.post_id LEFT JOIN comments c ON n.subject_kind='comment' AND c.id=n.subject_id AND c.entry_id=e.id
WHERE n.recipient_id=? AND n.historical=0 AND n.force_unavailable=0 AND e.publication_state='published' AND (e.visibility='everyone' OR (e.visibility='only_me' AND e.author_user_id=?))
AND ((n.subject_kind='post' AND n.subject_id=e.id) OR (n.subject_kind='comment' AND c.id IS NOT NULL))
AND n.sequence>? AND ((n.reasons & 7)=0 OR n.sequence>?)`, recipient, recipient, progress.AllSeen, progress.MentionsSeen).Scan(&progress.NewCount, &progress.MentionsNewCount)
	return progress, err
}

func (store interactionStore) AdvanceNotificationSeen(ctx context.Context, user, scope string, sequence int64) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO notification_seen(user_id,scope,through_sequence) VALUES (?,?,?) ON CONFLICT(user_id,scope) DO UPDATE SET through_sequence=max(through_sequence,excluded.through_sequence)`, user, scope, sequence)
	if err != nil {
		return fmt.Errorf("advance notification view: %w", err)
	}
	return nil
}
