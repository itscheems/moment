// Package commentdto freezes the one-level comment representation shared by
// HTTP, CLI, the Web client and the portable archive protocol.
package commentdto

import (
	"errors"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

// Comment is the canonical one-level comment wire representation.
type Comment struct {
	ID          string       `json:"id"`
	PostID      string       `json:"post_id"`
	ParentID    *string      `json:"parent_id"`
	Depth       int          `json:"depth"`
	AuthorKind  string       `json:"author_kind"`
	AuthorName  string       `json:"author_name"`
	ContentMD   string       `json:"content_md"`
	ContentHTML string       `json:"content_html"`
	CreatedAt   time.Time    `json:"created_at"`
	Media       []MediaUsage `json:"media"`
}

// MediaUsage is one ordered managed image attached to a comment.
type MediaUsage = postdto.MediaUsage

// Thread is a non-null flattened comment array.
type Thread struct {
	Items []Comment `json:"items"`
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
		media[index] = MediaUsage{MediaID: usage.MediaID.String(), Position: usage.Position, AltText: usage.AltText}
	}
	return Comment{
		ID: comment.Comment.ID.String(), PostID: comment.Comment.EntryID.String(), ParentID: parentID, Depth: depth,
		AuthorKind: string(comment.Comment.AuthorKind), AuthorName: comment.Comment.AuthorName,
		ContentMD: comment.Comment.ContentMD, ContentHTML: comment.ContentHTML, CreatedAt: comment.Comment.CreatedAt.UTC(), Media: media,
	}
}

// FromThread maps one canonical flattened application thread.
func FromThread(thread app.CommentThread) Thread {
	items := make([]Comment, 0, len(thread.Items))
	for _, comment := range thread.Items {
		items = append(items, FromComment(comment))
	}
	return Thread{Items: items}
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
	record := domain.Comment{ID: id, EntryID: postID, ParentID: parentID, AuthorKind: domain.CommentAuthorKind(comment.AuthorKind), AuthorName: comment.AuthorName, ContentMD: comment.ContentMD, CreatedAt: comment.CreatedAt}
	if err := record.Validate(); err != nil {
		return err
	}
	usages := make([]domain.EntryMediaUsage, len(comment.Media))
	for index, usage := range comment.Media {
		mediaID, parseErr := domain.ParseID(usage.MediaID)
		if parseErr != nil {
			return parseErr
		}
		usages[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
	}
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return err
	}
	html, err := domain.RenderCommentMarkdown(record.ContentMD)
	if err != nil || html != comment.ContentHTML {
		return errors.New("comment content_html does not match content_md")
	}
	return nil
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
	return nil
}

func commentAfter(left, right Comment) bool {
	if right.CreatedAt.After(left.CreatedAt) {
		return true
	}
	return right.CreatedAt.Equal(left.CreatedAt) && right.ID > left.ID
}
