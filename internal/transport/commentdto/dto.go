// Package commentdto freezes the one-level comment representation shared by
// HTTP, CLI, the Web client and the portable archive protocol.
package commentdto

import (
	"errors"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediatype"
	"github.com/moment/moment/internal/transport/postdto"
)

// Comment is the canonical one-level comment wire representation.
type Comment struct {
	ID          string            `json:"id"`
	PostID      string            `json:"post_id"`
	ParentID    *string           `json:"parent_id"`
	Depth       int               `json:"depth"`
	AuthorKind  string            `json:"author_kind"`
	AuthorName  string            `json:"author_name"`
	ContentMD   string            `json:"content_md"`
	ContentHTML string            `json:"content_html"`
	CreatedAt   time.Time         `json:"created_at"`
	Media       []MediaUsage      `json:"media"`
	Location    *postdto.Location `json:"location"`
}

// MediaUsage is one ordered managed image attached to a comment read model.
type MediaUsage = postdto.MediaProjection

// Thread is a non-null flattened comment array.
type Thread struct {
	Items           []Comment `json:"items"`
	PinnedCommentID *string   `json:"pinned_comment_id"`
}

// Pin is the canonical owner mutation result for one post comment pin.
type Pin struct {
	PostID          string  `json:"post_id"`
	PinnedCommentID *string `json:"pinned_comment_id"`
}

// FromComment maps one application result to the public DTO.
func FromComment(comment app.Comment) Comment {
	var parentID *string
	depth := 0
	if comment.Comment.ParentID != nil {
		value := comment.Comment.ParentID.String()
		parentID = &value
		depth = 1
	}
	media := make([]MediaUsage, len(comment.Media))
	for index, usage := range comment.Media {
		media[index] = MediaUsage{MediaID: usage.MediaID.String(), Position: usage.Position, AltText: usage.AltText, MIMEType: usage.MIMEType, Width: usage.Width, Height: usage.Height}
	}
	var location *postdto.Location
	if comment.Comment.Location != nil {
		location = &postdto.Location{Name: comment.Comment.Location.Name, DisplayName: comment.Comment.Location.DisplayName}
	}
	return Comment{
		ID: comment.Comment.ID.String(), PostID: comment.Comment.EntryID.String(), ParentID: parentID, Depth: depth,
		AuthorKind: string(comment.Comment.AuthorKind), AuthorName: comment.Comment.AuthorName,
		ContentMD: comment.Comment.ContentMD, ContentHTML: comment.ContentHTML, CreatedAt: comment.Comment.CreatedAt.UTC(), Media: media, Location: location,
	}
}

// FromThread maps one canonical flattened application thread.
func FromThread(thread app.CommentThread) Thread {
	items := make([]Comment, 0, len(thread.Items))
	for _, comment := range thread.Items {
		items = append(items, FromComment(comment))
	}
	var pinnedCommentID *string
	if thread.PinnedCommentID != nil {
		value := thread.PinnedCommentID.String()
		pinnedCommentID = &value
	}
	return Thread{Items: items, PinnedCommentID: pinnedCommentID}
}

// FromPin maps one canonical owner pin result.
func FromPin(pin app.CommentPin) Pin {
	var pinnedCommentID *string
	if pin.PinnedCommentID != nil {
		value := pin.PinnedCommentID.String()
		pinnedCommentID = &value
	}
	return Pin{PostID: pin.EntryID.String(), PinnedCommentID: pinnedCommentID}
}

// ValidateComment rejects a superficially successful non-canonical API DTO.
func ValidateComment(comment Comment) error {
	id, err := domain.ParseID(comment.ID)
	if err != nil {
		return err
	}
	postID, err := domain.ParseID(comment.PostID)
	if err != nil {
		return err
	}
	var parentID *domain.ID
	if comment.ParentID != nil {
		parsed, parseErr := domain.ParseID(*comment.ParentID)
		if parseErr != nil {
			return parseErr
		}
		parentID = &parsed
	}
	if (parentID == nil && comment.Depth != 0) || (parentID != nil && comment.Depth != 1) {
		return errors.New("comment depth does not match parent_id")
	}
	var location *domain.Location
	if comment.Location != nil {
		location = &domain.Location{Name: comment.Location.Name, DisplayName: comment.Location.DisplayName, Provider: "manual"}
	}
	record := domain.Comment{ID: id, EntryID: postID, ParentID: parentID, AuthorKind: domain.CommentAuthorKind(comment.AuthorKind), AuthorName: comment.AuthorName, ContentMD: comment.ContentMD, Location: location, CreatedAt: comment.CreatedAt}
	if err := record.Validate(); err != nil {
		return err
	}
	usages := make([]domain.EntryMediaUsage, len(comment.Media))
	for index, usage := range comment.Media {
		if usage.Width == 0 || usage.Height == 0 || !validProjectionMIMEType(usage.MIMEType) {
			return errors.New("comment media projection is invalid")
		}
		mediaID, parseErr := domain.ParseID(usage.MediaID)
		if parseErr != nil {
			return parseErr
		}
		usages[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
	}
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return err
	}
	html := ""
	if record.ContentMD != "" {
		html, err = domain.RenderCommentMarkdown(record.ContentMD)
	}
	if err != nil || html != comment.ContentHTML {
		return errors.New("comment content_html does not match content_md")
	}
	return nil
}

func validProjectionMIMEType(value string) bool {
	return mediatype.IsSupported(value)
}

// ValidateThread verifies adjacency, identity uniqueness and stable ordering.
func ValidateThread(thread Thread) error {
	if thread.Items == nil {
		return errors.New("comment thread items must be an array")
	}
	seen := make(map[string]struct{})
	postID := ""
	currentRoot := ""
	var lastRoot *Comment
	var lastReply *Comment
	for _, comment := range thread.Items {
		if err := ValidateComment(comment); err != nil {
			return err
		}
		if postID == "" {
			postID = comment.PostID
		}
		if comment.PostID != postID {
			return errors.New("comment thread contains multiple posts")
		}
		if _, duplicate := seen[comment.ID]; duplicate {
			return errors.New("comment thread contains a duplicate comment")
		}
		seen[comment.ID] = struct{}{}
		if comment.ParentID == nil {
			if lastRoot != nil && !commentAfter(*lastRoot, comment) {
				return errors.New("comment roots are not in stable order")
			}
			rootCopy := comment
			lastRoot = &rootCopy
			lastReply = nil
			currentRoot = comment.ID
			continue
		}
		if *comment.ParentID != currentRoot {
			return errors.New("comment reply is not adjacent to its root")
		}
		if lastReply != nil && !commentAfter(*lastReply, comment) {
			return errors.New("comment replies are not in stable order")
		}
		replyCopy := comment
		lastReply = &replyCopy
	}
	if thread.PinnedCommentID != nil {
		_, found := seen[*thread.PinnedCommentID]
		if !found {
			return errors.New("pinned comment is missing from the thread")
		}
		for _, comment := range thread.Items {
			if comment.ID == *thread.PinnedCommentID && comment.ParentID != nil {
				return errors.New("pinned comment must be a root")
			}
		}
	}
	return nil
}

// ValidatePin rejects a non-canonical owner mutation result.
func ValidatePin(pin Pin) error {
	if _, err := domain.ParseID(pin.PostID); err != nil {
		return err
	}
	if pin.PinnedCommentID != nil {
		if _, err := domain.ParseID(*pin.PinnedCommentID); err != nil {
			return err
		}
	}
	return nil
}

func commentAfter(left, right Comment) bool {
	if right.CreatedAt.After(left.CreatedAt) {
		return true
	}
	return right.CreatedAt.Equal(left.CreatedAt) && right.ID > left.ID
}
