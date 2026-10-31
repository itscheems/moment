package app

import (
	"context"
	"crypto/rand"
	"sort"
	"time"

	"github.com/moment/moment/internal/domain"
)

// ActivityAccount is the bounded identity needed to address an interaction.
type ActivityAccount struct {
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"display_name"`
	AvatarMediaID *string `json:"avatar_media_id"`
	Active        bool    `json:"-"`
}

// ActivitySource applies the enclosing post's reading authority to either kind
// of target. BodyPreview is read from current content, never from an event.
type ActivitySource struct {
	Ref              domain.ContentRef
	Entry            domain.Entry
	AuthorID         string
	ReplyToCommentID *domain.ID
	BodyPreview      string
	PreviewIsPlain   bool
}

// ActivityStore is used inside the content writer's existing transaction.
type ActivityStore interface {
	ActivitySource(context.Context, domain.ContentRef) (ActivitySource, bool, error)
	ActivityAccounts(context.Context, []string) (map[string]ActivityAccount, error)
	MentionAccounts(context.Context, []string) (map[string]ActivityAccount, error)
	MentionBindings(context.Context, domain.ContentRef) ([]domain.MentionBinding, error)
	ReplaceMentionBindings(context.Context, domain.ContentRef, []domain.MentionBinding) error
	ReplyRecipientIDs(context.Context, domain.ID) ([]string, error)
	ReplaceReplyRecipientIDs(context.Context, domain.ID, []string) error
	InsertNotification(context.Context, domain.Notification) error
}

// ReplyParticipant is one server-derived candidate, optionally removable by the writer.
type ReplyParticipant struct {
	ActivityAccount
	Required bool `json:"required"`
}

// ReplyContext describes the accounts included when replying to one content target.
type ReplyContext struct {
	Subject      domain.ContentRef  `json:"subject"`
	Participants []ReplyParticipant `json:"participants"`
}

func sourceNotFound(ref domain.ContentRef) error {
	if ref.Kind == domain.SubjectComment {
		return commentNotFound(ref.ID)
	}
	return postNotFound(ref.ID)
}

func activeActivityActor(ctx context.Context, store ActivityStore) (ActivityAccount, error) {
	id, err := contentActorID(ctx, store)
	if err != nil {
		return ActivityAccount{}, err
	}
	accounts, err := store.ActivityAccounts(ctx, []string{id})
	if err != nil {
		return ActivityAccount{}, err
	}
	actor, ok := accounts[id]
	if !ok || !actor.Active {
		return ActivityAccount{}, domain.NewError(domain.CodeUnauthorized, "an active account identity is required", nil)
	}
	return actor, nil
}

func replyContext(ctx context.Context, store ActivityStore, ref domain.ContentRef, viewer Viewer) (ReplyContext, error) {
	if err := ref.Validate(); err != nil {
		return ReplyContext{}, err
	}
	source, found, err := store.ActivitySource(ctx, ref)
	if err != nil {
		return ReplyContext{}, err
	}
	if !found || !viewer.CanReadEntry(source.Entry) {
		return ReplyContext{}, sourceNotFound(ref)
	}
	ids := []string{source.AuthorID}
	mentions, err := store.MentionBindings(ctx, ref)
	if err != nil {
		return ReplyContext{}, err
	}
	for _, mention := range mentions {
		if mention.UserID != nil {
			ids = append(ids, *mention.UserID)
		}
	}
	if ref.Kind == domain.SubjectComment {
		inherited, err := store.ReplyRecipientIDs(ctx, ref.ID)
		if err != nil {
			return ReplyContext{}, err
		}
		ids = append(ids, inherited...)
	}
	accounts, err := store.ActivityAccounts(ctx, ids)
	if err != nil {
		return ReplyContext{}, err
	}
	result := ReplyContext{Subject: ref, Participants: make([]ReplyParticipant, 0)}
	seen := make(map[string]bool)
	for _, id := range ids {
		if id == "" || seen[id] || id == viewer.UserID() && id != source.AuthorID {
			continue
		}
		seen[id] = true
		if account, ok := accounts[id]; ok {
			result.Participants = append(result.Participants, ReplyParticipant{ActivityAccount: account, Required: id == source.AuthorID})
		}
	}
	return result, nil
}

func normalizeReplyExclusions(ids []string) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		if err := domain.ValidateUserID(id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result, nil
}

// bindMentions preserves existing bindings, including unresolved text. Historical
// handles supplied in previous prevent an unrelated edit from backfilling events.
func bindMentions(ctx context.Context, store ActivityStore, source ActivitySource, handles, previous []string) ([]domain.MentionBinding, map[string]domain.NotificationReason, error) {
	old, err := store.MentionBindings(ctx, source.Ref)
	if err != nil {
		return nil, nil, err
	}
	byHandle := make(map[string]domain.MentionBinding)
	for _, binding := range old {
		byHandle[binding.Handle] = binding
	}
	for _, handle := range previous {
		if _, ok := byHandle[handle]; !ok {
			byHandle[handle] = domain.MentionBinding{Handle: handle}
		}
	}
	fresh := make([]string, 0)
	for _, handle := range handles {
		if _, ok := byHandle[handle]; !ok {
			fresh = append(fresh, handle)
		}
	}
	resolved, err := store.MentionAccounts(ctx, fresh)
	if err != nil {
		return nil, nil, err
	}
	bindings := make([]domain.MentionBinding, 0, len(handles))
	ids := make([]string, 0, len(handles))
	already := make(map[string]bool)
	for _, handle := range handles {
		binding, exists := byHandle[handle]
		if !exists {
			binding.Handle = handle
			if account, ok := resolved[handle]; ok {
				id := account.UserID
				binding.UserID = &id
			}
		}
		if binding.UserID != nil {
			ids = append(ids, *binding.UserID)
			if binding.Notified {
				already[*binding.UserID] = true
			}
		}
		bindings = append(bindings, binding)
	}
	accounts, err := store.ActivityAccounts(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	recipients := make(map[string]domain.NotificationReason)
	for i, binding := range bindings {
		if binding.UserID == nil || binding.Notified {
			continue
		}
		id := *binding.UserID
		if !AccountViewer(id).CanReadEntry(source.Entry) {
			continue
		}
		// A public mention is handled once, including ineligible recipients.
		bindings[i].Notified = true
		account, exists := accounts[id]
		if !exists || !account.Active {
			continue
		}
		if !already[id] {
			recipients[id] |= domain.ReasonMention
		}
	}
	return bindings, recipients, nil
}

func writeActivityNotifications(ctx context.Context, store ActivityStore, source ActivitySource, actor ActivityAccount, eventType string, at time.Time, recipients map[string]domain.NotificationReason) error {
	ids := make([]string, 0, len(recipients))
	for id := range recipients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	accounts, err := store.ActivityAccounts(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		account, exists := accounts[id]
		if id == actor.UserID || !exists || !account.Active || !AccountViewer(id).CanReadEntry(source.Entry) {
			continue
		}
		actorID := actor.UserID
		notificationID, err := domain.NewID(at, rand.Reader)
		if err != nil {
			return err
		}
		if err := store.InsertNotification(ctx, domain.Notification{ID: notificationID, RecipientID: id, ActorID: &actorID, ActorName: actor.DisplayName, EventType: eventType, Subject: source.Ref, PostID: source.Entry.ID, PostKind: source.Entry.Kind, ReplyToCommentID: source.ReplyToCommentID, Reasons: recipients[id], OccurredAt: at}); err != nil {
			return err
		}
	}
	return nil
}

func syncPostActivity(ctx context.Context, store ActivityStore, entry domain.Entry, handles, previous []string, eventType string, at time.Time) error {
	actor, err := activeActivityActor(ctx, store)
	if err != nil {
		return err
	}
	source := ActivitySource{Ref: domain.ContentRef{Kind: domain.SubjectPost, ID: entry.ID}, Entry: entry, AuthorID: entry.AuthorUserID}
	bindings, recipients, err := bindMentions(ctx, store, source, handles, previous)
	if err != nil {
		return err
	}
	if err := store.ReplaceMentionBindings(ctx, source.Ref, bindings); err != nil {
		return err
	}
	return writeActivityNotifications(ctx, store, source, actor, eventType, at, recipients)
}

func createCommentActivity(ctx context.Context, store ActivityStore, comment domain.Comment, excluded []string) error {
	actor, err := activeActivityActor(ctx, store)
	if err != nil {
		return err
	}
	ref := domain.ContentRef{Kind: domain.SubjectPost, ID: comment.EntryID}
	if comment.ParentID != nil {
		ref = domain.ContentRef{Kind: domain.SubjectComment, ID: *comment.ParentID}
		if comment.ReplyToCommentID != nil {
			ref.ID = *comment.ReplyToCommentID
		}
	}
	context, err := replyContext(ctx, store, ref, AccountViewer(actor.UserID))
	if err != nil {
		return err
	}
	candidates := make(map[string]ReplyParticipant)
	for _, person := range context.Participants {
		candidates[person.UserID] = person
	}
	removed := make(map[string]bool)
	for _, id := range excluded {
		person, exists := candidates[id]
		if !exists || person.Required {
			return domain.NewError(domain.CodeInvalidArgument, "only optional reply participants may be excluded", map[string]any{"field": "exclude_reply_user_ids"})
		}
		removed[id] = true
	}
	source, found, err := store.ActivitySource(ctx, domain.ContentRef{Kind: domain.SubjectComment, ID: comment.ID})
	if err != nil {
		return err
	}
	if !found {
		return commentNotFound(comment.ID)
	}
	if ref.Kind == domain.SubjectComment {
		target := ref.ID
		source.ReplyToCommentID = &target
	}
	bindings, recipients, err := bindMentions(ctx, store, source, domain.MarkdownMentions(comment.ContentMD), nil)
	if err != nil {
		return err
	}
	selected := make(map[string]bool)
	for id, person := range candidates {
		if removed[id] {
			continue
		}
		selected[id] = true
		reason := domain.ReasonConversation
		if person.Required {
			reason = domain.ReasonReply
		}
		recipients[id] |= reason
	}
	for _, binding := range bindings {
		if binding.UserID != nil {
			selected[*binding.UserID] = true
		}
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := store.ReplaceMentionBindings(ctx, source.Ref, bindings); err != nil {
		return err
	}
	if err := store.ReplaceReplyRecipientIDs(ctx, comment.ID, ids); err != nil {
		return err
	}
	return writeActivityNotifications(ctx, store, source, actor, "comment.created", comment.CreatedAt, recipients)
}
