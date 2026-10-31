package domain

import "time"

// ContentRef identifies the content on which an interaction occurred.
type ContentRef struct {
	Kind string `json:"type"`
	ID   ID     `json:"id"`
}

// Supported interaction source kinds.
const (
	SubjectPost    = "post"
	SubjectComment = "comment"
)

// Validate accepts only canonical post and comment references.
func (ref ContentRef) Validate() error {
	if ref.Kind != SubjectPost && ref.Kind != SubjectComment {
		return NewError(CodeInvalidArgument, "subject type must be post or comment", map[string]any{"field": "subject.type"})
	}
	_, err := ParseID(ref.ID.String())
	return err
}

// NotificationReason records why one account receives an activity.
type NotificationReason uint8

// Reasons can be combined when one action addresses an account in several ways.
const (
	ReasonReply NotificationReason = 1 << iota
	ReasonMention
	ReasonConversation
	ReasonLike
)

// Names returns reasons in stable presentation order.
func (reason NotificationReason) Names() []string {
	names := make([]string, 0, 4)
	for _, item := range []struct {
		value NotificationReason
		name  string
	}{
		{ReasonReply, "reply"}, {ReasonMention, "mention"}, {ReasonConversation, "conversation"}, {ReasonLike, "like"},
	} {
		if reason&item.value != 0 {
			names = append(names, item.name)
		}
	}
	return names
}

// Notification view scopes have independent acknowledgement positions.
const (
	NotificationAll      = "all"
	NotificationMentions = "mentions"
)

// NotificationScopeMask selects the reasons included in one inbox view.
func NotificationScopeMask(scope string) (NotificationReason, error) {
	switch scope {
	case NotificationAll:
		return ReasonReply | ReasonMention | ReasonConversation | ReasonLike, nil
	case NotificationMentions:
		return ReasonReply | ReasonMention | ReasonConversation, nil
	default:
		return 0, NewError(CodeInvalidArgument, "notification scope must be all or mentions", map[string]any{"field": "scope"})
	}
}

// MentionBinding preserves a handle's resolved account until the author removes it.
// An unresolved handle remains unresolved on unrelated edits.
type MentionBinding struct {
	Handle   string
	UserID   *string
	Notified bool
}

// Notification is an immutable activity addressed to one account. Source IDs
// remain historical references after content deletion; bodies are never stored here.
type Notification struct {
	ID               ID
	Sequence         int64
	RecipientID      string
	ActorID          *string
	ActorName        string
	EventType        string
	Subject          ContentRef
	PostID           ID
	PostKind         EntryKind
	ReplyToCommentID *ID
	Reasons          NotificationReason
	OccurredAt       time.Time
}

// LikeState combines the current relation count and the verified viewer's state.
type LikeState struct {
	Count     int  `json:"like_count"`
	LikedByMe bool `json:"liked_by_me"`
}
