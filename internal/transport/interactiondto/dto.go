// Package interactiondto shares the account inbox representation across transports.
package interactiondto

import (
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/commentdto"
	"github.com/moment/moment/internal/transport/postdto"
)

// Actor is either a current account or an unlinked historical display name.
type Actor struct {
	UserID        *string `json:"user_id"`
	Username      *string `json:"username"`
	DisplayName   string  `json:"display_name"`
	AvatarMediaID *string `json:"avatar_media_id"`
	ProfileURL    *string `json:"profile_url"`
}

// Notification exposes immutable event metadata and authorized current content.
type Notification struct {
	ID               domain.ID           `json:"id"`
	EventType        string              `json:"event_type"`
	Subject          domain.ContentRef   `json:"subject"`
	PostID           domain.ID           `json:"post_id"`
	PostKind         string              `json:"post_kind"`
	ReplyToCommentID *domain.ID          `json:"reply_to_comment_id"`
	Reasons          []string            `json:"reasons"`
	OccurredAt       time.Time           `json:"occurred_at"`
	Actor            Actor               `json:"actor"`
	Seen             bool                `json:"seen"`
	Available        bool                `json:"available"`
	Excerpt          string              `json:"excerpt"`
	Post             *postdto.Post       `json:"post"`
	Comment          *commentdto.Comment `json:"comment"`
}

// Page includes separate machine checkpoints and human view boundaries.
type Page struct {
	Items            []Notification `json:"items"`
	NextCursor       string         `json:"next_cursor"`
	ThroughCursor    string         `json:"through_cursor"`
	NewCount         int            `json:"new_count"`
	MentionsNewCount int            `json:"mentions_new_count"`
}

// FromItem never exports protected projections when a source is unavailable.
func FromItem(item app.NotificationItem) Notification {
	n, a := item.Record, item.Actor
	result := Notification{ID: n.ID, EventType: n.EventType, Subject: n.Subject, PostID: n.PostID, PostKind: string(n.PostKind), Reasons: n.Reasons.Names(), OccurredAt: n.OccurredAt, Actor: Actor{UserID: a.UserID, Username: a.Username, DisplayName: a.DisplayName, AvatarMediaID: a.AvatarMediaID, ProfileURL: a.ProfileURL}, Seen: item.Seen, Available: item.Available}
	if item.Available {
		result.Excerpt = item.Excerpt
		result.ReplyToCommentID = n.ReplyToCommentID
		if item.Post != nil {
			v := postdto.FromPost(*item.Post)
			result.Post = &v
		}
		if item.Comment != nil {
			v := commentdto.FromComment(*item.Comment)
			result.Comment = &v
		}
	}
	return result
}

// FromPage maps a stable page without changing viewing positions.
func FromPage(page app.NotificationPage) Page {
	result := Page{Items: make([]Notification, 0, len(page.Items)), NextCursor: page.NextCursor, ThroughCursor: page.ThroughCursor, NewCount: page.NewCount, MentionsNewCount: page.MentionsNewCount}
	for _, item := range page.Items {
		result.Items = append(result.Items, FromItem(item))
	}
	return result
}

// ValidNotification checks source shape and prevents unavailable body projections.
func ValidNotification(n Notification) bool {
	if _, err := domain.ParseID(n.ID.String()); err != nil {
		return false
	}
	if n.Subject.Validate() != nil || n.OccurredAt.IsZero() || len(n.Reasons) == 0 || n.Actor.DisplayName == "" {
		return false
	}
	if _, err := domain.ParseID(n.PostID.String()); err != nil {
		return false
	}
	if n.PostKind != "moment" && n.PostKind != "article" {
		return false
	}
	if !n.Available {
		return n.Post == nil && n.Comment == nil && n.Excerpt == "" && n.ReplyToCommentID == nil
	}
	if n.Post != nil && (postdto.ValidatePost(*n.Post) != nil || n.Post.ID != n.PostID.String()) {
		return false
	}
	if n.Comment != nil && (commentdto.ValidateComment(*n.Comment) != nil || n.Comment.ID != n.Subject.ID.String()) {
		return false
	}
	return true
}
