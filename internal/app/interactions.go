package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
)

// NotificationProgress captures committed activity and both viewing positions.
type NotificationProgress struct {
	Latest           int64
	AllSeen          int64
	MentionsSeen     int64
	NewCount         int
	MentionsNewCount int
}

// NotificationQuery is the repository's bounded keyset selection.
type NotificationQuery struct {
	RecipientID  string
	Mask         domain.NotificationReason
	Sequence     int64
	OccurredAtMS int64
	Newer        bool
	Limit        int
}

// InteractionStore supplies interaction authority within one SQLite transaction.
type InteractionStore interface {
	ActivityStore
	GenerationID() string
	ActivitySources(context.Context, []domain.ContentRef) (map[domain.ContentRef]ActivitySource, error)
	SetLike(context.Context, domain.ContentRef, string, bool, time.Time) (bool, error)
	LikeStates(context.Context, string, []domain.ID, string) (map[domain.ID]domain.LikeState, error)
	Notifications(context.Context, NotificationQuery) ([]domain.Notification, error)
	NotificationByID(context.Context, string, domain.ID) (domain.Notification, bool, error)
	NotificationProgress(context.Context, string) (NotificationProgress, error)
	AdvanceNotificationSeen(context.Context, string, string, int64) error
}

// InteractionTransactions keeps reads consistent and mutations atomic.
type InteractionTransactions interface {
	Read(context.Context, func(InteractionStore) error) error
	Write(context.Context, func(InteractionStore) error) error
}

// InteractionService owns likes, reply addressing and account-scoped inbox reads.
type InteractionService struct {
	transactions InteractionTransactions
	posts        *PostService
	comments     *CommentService
	clock        func() time.Time
}

// NewInteractionService reuses existing content readers for authorized full bodies.
func NewInteractionService(transactions InteractionTransactions, posts *PostService, comments *CommentService) (*InteractionService, error) {
	if transactions == nil || posts == nil || comments == nil {
		return nil, errors.New("interaction transactions and content readers are required")
	}
	return &InteractionService{transactions: transactions, posts: posts, comments: comments, clock: time.Now}, nil
}

// Like sets one active account's relation without duplicating repeated requests.
func (service *InteractionService) Like(ctx context.Context, ref domain.ContentRef, active bool) (domain.LikeState, error) {
	if err := ref.Validate(); err != nil {
		return domain.LikeState{}, err
	}
	var state domain.LikeState
	err := service.transactions.Write(ctx, func(store InteractionStore) error {
		actor, err := activeActivityActor(ctx, store)
		if err != nil {
			return err
		}
		source, found, err := store.ActivitySource(ctx, ref)
		if err != nil {
			return err
		}
		if !found || !AccountViewer(actor.UserID).CanReadEntry(source.Entry) {
			return sourceNotFound(ref)
		}
		now := domain.NormalizeTime(service.clock())
		changed, err := store.SetLike(ctx, ref, actor.UserID, active, now)
		if err != nil {
			return err
		}
		if active && changed && source.AuthorID != "" {
			if err := writeActivityNotifications(ctx, store, source, actor, ref.Kind+".liked", now, map[string]domain.NotificationReason{source.AuthorID: domain.ReasonLike}); err != nil {
				return err
			}
		}
		states, err := store.LikeStates(ctx, ref.Kind, []domain.ID{ref.ID}, actor.UserID)
		state = states[ref.ID]
		return err
	})
	return state, err
}

// LikeStatus returns a state only when the viewer may read its source.
func (service *InteractionService) LikeStatus(ctx context.Context, ref domain.ContentRef, viewer Viewer) (domain.LikeState, error) {
	if err := ref.Validate(); err != nil {
		return domain.LikeState{}, err
	}
	var state domain.LikeState
	err := service.transactions.Read(ctx, func(store InteractionStore) error {
		source, found, err := store.ActivitySource(ctx, ref)
		if err != nil {
			return err
		}
		if !found || !viewer.CanReadEntry(source.Entry) {
			return sourceNotFound(ref)
		}
		states, err := store.LikeStates(ctx, ref.Kind, []domain.ID{ref.ID}, viewer.UserID())
		state = states[ref.ID]
		return err
	})
	return state, err
}

// ReplyContext derives optional and required participants for an active writer.
func (service *InteractionService) ReplyContext(ctx context.Context, ref domain.ContentRef) (ReplyContext, error) {
	var result ReplyContext
	err := service.transactions.Read(ctx, func(store InteractionStore) error {
		actor, err := activeActivityActor(ctx, store)
		if err != nil {
			return err
		}
		result, err = replyContext(ctx, store, ref, AccountViewer(actor.UserID))
		return err
	})
	return result, err
}

// NotificationActor is a current linked profile or an unlinked historical name.
type NotificationActor struct {
	UserID        *string
	Username      *string
	DisplayName   string
	AvatarMediaID *string
	ProfileURL    *string
}

// NotificationItem combines an immutable event with authorized current projections.
type NotificationItem struct {
	Record    domain.Notification
	Actor     NotificationActor
	Seen      bool
	Available bool
	Excerpt   string
	Post      *Post
	Comment   *Comment
}

// NotificationListInput selects a view and either older or incremental history.
type NotificationListInput struct {
	Scope  string
	Cursor string
	After  string
	Reason string
	Limit  int
}

// NotificationPage carries history and the snapshot boundary eligible for viewing.
type NotificationPage struct {
	Items            []NotificationItem
	NextCursor       string
	ThroughCursor    string
	NewCount         int
	MentionsNewCount int
}

// NotificationState is the lightweight progress projection used by live observers.
type NotificationState struct {
	NewCount         int    `json:"new_count"`
	MentionsNewCount int    `json:"mentions_new_count"`
	ThroughCursor    string `json:"through_cursor"`
	AllSeen          int64  `json:"-"`
	MentionsSeen     int64  `json:"-"`
}

type notificationCursor struct {
	Version      int                       `json:"v"`
	Generation   string                    `json:"g"`
	Account      string                    `json:"a"`
	Scope        string                    `json:"s"`
	Mask         domain.NotificationReason `json:"m"`
	Sequence     int64                     `json:"q"`
	OccurredAtMS int64                     `json:"t,omitempty"`
}

func encodeNotificationCursor(store InteractionStore, account, scope string, mask domain.NotificationReason, sequence, occurredAtMS int64) string {
	data, _ := json.Marshal(notificationCursor{Version: 1, Generation: store.GenerationID(), Account: account, Scope: scope, Mask: mask, Sequence: sequence, OccurredAtMS: occurredAtMS})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeNotificationCursor(value string, store InteractionStore, account, scope string, mask domain.NotificationReason) (notificationCursor, error) {
	invalid := func() error {
		return domain.NewError(domain.CodeInvalidArgument, "notification cursor is invalid for this account, view, or data generation", map[string]any{"field": "cursor"})
	}
	if len(value) > MaxCursorBytes {
		return notificationCursor{}, invalid()
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return notificationCursor{}, invalid()
	}
	var cursor notificationCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return notificationCursor{}, invalid()
	}
	if cursor.Version != 1 || cursor.Generation != store.GenerationID() || cursor.Account != account || cursor.Scope != scope || cursor.Mask != mask || cursor.Sequence < 0 {
		return notificationCursor{}, invalid()
	}
	return cursor, nil
}

func notificationMask(scope, reason string) (domain.NotificationReason, error) {
	mask, err := domain.NotificationScopeMask(scope)
	if err != nil {
		return 0, err
	}
	if reason == "" {
		return mask, nil
	}
	var selected domain.NotificationReason
	switch reason {
	case "like":
		selected = domain.ReasonLike
	case "reply":
		selected = domain.ReasonReply
	case "mention":
		selected = domain.ReasonMention
	case "conversation":
		selected = domain.ReasonConversation
	default:
		return 0, domain.NewError(domain.CodeInvalidArgument, "unknown notification reason", map[string]any{"field": "reason"})
	}
	if selected&mask == 0 {
		return 0, domain.NewError(domain.CodeInvalidArgument, "notification reason is outside this view", map[string]any{"field": "reason"})
	}
	return selected, nil
}

// Notifications returns metadata and safe previews without acknowledging a view.
func (service *InteractionService) Notifications(ctx context.Context, input NotificationListInput) (NotificationPage, error) {
	if input.Scope == "" {
		input.Scope = domain.NotificationAll
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 100 || input.After != "" && input.Cursor != "" {
		return NotificationPage{}, domain.NewError(domain.CodeInvalidArgument, "invalid notification pagination", nil)
	}
	mask, err := notificationMask(input.Scope, input.Reason)
	if err != nil {
		return NotificationPage{}, err
	}
	page := NotificationPage{Items: make([]NotificationItem, 0)}
	err = service.transactions.Read(ctx, func(store InteractionStore) error {
		actor, err := activeActivityActor(ctx, store)
		if err != nil {
			return err
		}
		var position notificationCursor
		cursor := input.Cursor
		if input.After != "" {
			cursor = input.After
		}
		if cursor != "" {
			position, err = decodeNotificationCursor(cursor, store, actor.UserID, input.Scope, mask)
			if err != nil {
				return err
			}
		}
		progress, err := store.NotificationProgress(ctx, actor.UserID)
		if err != nil {
			return err
		}
		if position.Sequence > progress.Latest {
			return domain.NewError(domain.CodeInvalidArgument, "notification cursor is beyond this account's history", nil)
		}
		if input.Cursor != "" && position.OccurredAtMS <= 0 {
			return domain.NewError(domain.CodeInvalidArgument, "history pagination requires the returned next cursor", nil)
		}
		records, err := store.Notifications(ctx, NotificationQuery{RecipientID: actor.UserID, Mask: mask, Sequence: position.Sequence, OccurredAtMS: position.OccurredAtMS, Newer: input.After != "", Limit: input.Limit + 1})
		if err != nil {
			return err
		}
		more := len(records) > input.Limit
		if more {
			records = records[:input.Limit]
		}
		refs := make([]domain.ContentRef, 0, len(records))
		ids := make([]string, 0, len(records))
		for _, record := range records {
			refs = append(refs, record.Subject)
			if record.ActorID != nil {
				ids = append(ids, *record.ActorID)
			}
		}
		sources, err := store.ActivitySources(ctx, refs)
		if err != nil {
			return err
		}
		accounts, err := store.ActivityAccounts(ctx, ids)
		if err != nil {
			return err
		}
		for _, record := range records {
			item := projectNotification(record, progress, accounts)
			if source, ok := sources[record.Subject]; ok && !record.ForceUnavailable && source.Entry.ID == record.PostID && AccountViewer(actor.UserID).CanReadEntry(source.Entry) {
				item.Available = true
				preview := source.BodyPreview
				if !source.PreviewIsPlain {
					preview, err = domain.VisibleContent(preview)
					if err != nil {
						return err
					}
				}
				runes := []rune(strings.TrimSpace(preview))
				if len(runes) > 200 {
					preview = string(runes[:200]) + "…"
				}
				item.Excerpt = preview
			}
			page.Items = append(page.Items, item)
		}
		if more {
			last := records[len(records)-1]
			var at int64
			if input.After == "" {
				at = domain.TimeToMillis(last.OccurredAt)
			}
			page.NextCursor = encodeNotificationCursor(store, actor.UserID, input.Scope, mask, last.Sequence, at)
		}
		page.ThroughCursor = encodeNotificationCursor(store, actor.UserID, input.Scope, mask, progress.Latest, 0)
		page.NewCount = progress.NewCount
		page.MentionsNewCount = progress.MentionsNewCount
		return nil
	})
	return page, err
}

// Notification reads one addressed event and the latest body under current authority.
func (service *InteractionService) Notification(ctx context.Context, id string) (NotificationItem, error) {
	identifier, err := domain.ParseID(id)
	if err != nil {
		return NotificationItem{}, domain.NewError(domain.CodeInvalidArgument, "invalid notification identifier", nil)
	}
	page, err := service.notificationItem(ctx, identifier)
	if err != nil {
		return NotificationItem{}, err
	}
	if page.Record.ForceUnavailable {
		return page, nil
	}
	viewer := ViewerFromContext(ctx)
	if page.Record.Subject.Kind == domain.SubjectPost {
		post, err := service.posts.Get(ctx, page.Record.Subject.ID, viewer)
		if err == nil {
			page.Post = &post
			page.Available = true
			return page, nil
		}
		if !domain.HasCode(err, domain.CodePostNotFound) {
			return NotificationItem{}, err
		}
	} else {
		context, err := service.comments.GetContext(ctx, page.Record.PostID, page.Record.Subject.ID, viewer)
		if err == nil {
			page.Comment = &context.Comment
			page.Available = true
			return page, nil
		}
		if !domain.HasCode(err, domain.CodePostNotFound) && !domain.HasCode(err, domain.CodeCommentNotFound) {
			return NotificationItem{}, err
		}
	}
	page.Available = false
	page.Excerpt = ""
	return page, nil
}

func (service *InteractionService) notificationItem(ctx context.Context, id domain.ID) (NotificationItem, error) {
	var item NotificationItem
	err := service.transactions.Read(ctx, func(store InteractionStore) error {
		actor, err := activeActivityActor(ctx, store)
		if err != nil {
			return err
		}
		var found bool
		record, found, err := store.NotificationByID(ctx, actor.UserID, id)
		if err != nil {
			return err
		}
		if !found {
			return domain.NewError(domain.CodeNotificationNotFound, "notification was not found", nil)
		}
		progress, err := store.NotificationProgress(ctx, record.RecipientID)
		if err != nil {
			return err
		}
		accounts := make(map[string]ActivityAccount)
		if record.ActorID != nil {
			accounts, err = store.ActivityAccounts(ctx, []string{*record.ActorID})
			if err != nil {
				return err
			}
		}
		item = projectNotification(record, progress, accounts)
		return nil
	})
	return item, err
}

func projectNotification(record domain.Notification, progress NotificationProgress, accounts map[string]ActivityAccount) NotificationItem {
	seen := progress.AllSeen
	if record.Reasons&(domain.ReasonReply|domain.ReasonMention|domain.ReasonConversation) != 0 && progress.MentionsSeen > seen {
		seen = progress.MentionsSeen
	}
	item := NotificationItem{Record: record, Seen: record.Historical || record.Sequence <= seen, Actor: NotificationActor{DisplayName: record.ActorName}}
	if record.ActorID != nil {
		if account, ok := accounts[*record.ActorID]; ok && account.Active {
			id, name, url := account.UserID, account.Username, domain.ProfileURL(account.Username)
			item.Actor = NotificationActor{UserID: &id, Username: &name, DisplayName: account.DisplayName, AvatarMediaID: account.AvatarMediaID, ProfileURL: &url}
		}
	}
	return item
}

// NotificationState reads progress without changing either acknowledgement position.
func (service *InteractionService) NotificationState(ctx context.Context) (NotificationState, error) {
	var result NotificationState
	err := service.transactions.Read(ctx, func(store InteractionStore) error {
		actor, err := activeActivityActor(ctx, store)
		if err != nil {
			return err
		}
		progress, err := store.NotificationProgress(ctx, actor.UserID)
		if err != nil {
			return err
		}
		mask, _ := domain.NotificationScopeMask(domain.NotificationAll)
		result = NotificationState{NewCount: progress.NewCount, MentionsNewCount: progress.MentionsNewCount, ThroughCursor: encodeNotificationCursor(store, actor.UserID, domain.NotificationAll, mask, progress.Latest, 0), AllSeen: progress.AllSeen, MentionsSeen: progress.MentionsSeen}
		return nil
	})
	return result, err
}

// SeeNotifications monotonically acknowledges the supplied view's snapshot only.
func (service *InteractionService) SeeNotifications(ctx context.Context, scope, cursor string) (NotificationState, error) {
	mask, err := domain.NotificationScopeMask(scope)
	if err != nil {
		return NotificationState{}, err
	}
	err = service.transactions.Write(ctx, func(store InteractionStore) error {
		actor, err := activeActivityActor(ctx, store)
		if err != nil {
			return err
		}
		position, err := decodeNotificationCursor(cursor, store, actor.UserID, scope, mask)
		if err != nil {
			return err
		}
		progress, err := store.NotificationProgress(ctx, actor.UserID)
		if err != nil {
			return err
		}
		if position.Sequence > progress.Latest {
			return domain.NewError(domain.CodeInvalidArgument, "cannot confirm unseen future notifications", nil)
		}
		return store.AdvanceNotificationSeen(ctx, actor.UserID, scope, position.Sequence)
	})
	if err != nil {
		return NotificationState{}, err
	}
	return service.NotificationState(ctx)
}
