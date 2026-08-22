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
//
// AuthorName is the value stored on the row and exists so a client can round
// trip a comment without losing what was persisted. Author is the identity to
// render: it is resolved server side, so every adapter shows the same name and
// no client has to know that owner comments take theirs from the site.
type Comment struct {
	ID          string            `json:"id"`
	PostID      string            `json:"post_id"`
	ParentID    *string           `json:"parent_id"`
	Depth       int               `json:"depth"`
	AuthorKind  string            `json:"author_kind"`
	AuthorName  string            `json:"author_name"`
	Author      Author            `json:"author"`
	ReplyTo     *ReplyTarget      `json:"reply_to"`
	ContentMD   string            `json:"content_md"`
	ContentHTML string            `json:"content_html"`
	CreatedAt   time.Time         `json:"created_at"`
	Media       []MediaUsage      `json:"media"`
	Location    *postdto.Location `json:"location"`
	// ReplyCount is the true number of replies under a root, independent of how
	// many of them this response carried. RepliesNextCursor is set when more
	// remain. Both are zero on a reply.
	ReplyCount        int    `json:"reply_count"`
	RepliesNextCursor string `json:"replies_next_cursor,omitempty"`
}

// ReplyTarget names the comment a reply answers.
//
// The client shows it only when the target is not the root, because answering
// the root is what the reply's position already says; repeating it there would
// be noise. Judging that by target comment rather than by target author is
// deliberate: when the root's author has written several replies, the name is
// exactly the thing that stops identifying which one is meant.
type ReplyTarget struct {
	CommentID string `json:"comment_id"`
	Author    Author `json:"author"`
}

// Author is the resolved public identity behind one comment.
//
// A comment written before commenting required an account has no identity to
// resolve, so every field but DisplayName is null and the client renders it as
// unlinked plain text.
type Author struct {
	UserID        *string `json:"user_id"`
	Username      *string `json:"username"`
	DisplayName   string  `json:"display_name"`
	AvatarMediaID *string `json:"avatar_media_id"`
	ProfileURL    *string `json:"profile_url"`
}

// MediaUsage is one ordered managed image attached to a comment read model.
type MediaUsage = postdto.MediaProjection

// Thread is a non-null flattened comment array.
//
// Items is one page in creation order. A pinned root is not hoisted into it:
// reordering the stream would make the keyset cursor mean something different
// the moment the pin changed. It travels separately in Pinned instead, and the
// client renders that above the stream and skips the duplicate by ID.
//
// Excluding it from Items was the tempting alternative and is wrong: re-pin
// mid-scroll and the previously pinned root would be missing from pages the
// reader had already fetched, with nothing to put it back.
type Thread struct {
	Items           []Comment `json:"items"`
	PinnedCommentID *string   `json:"pinned_comment_id"`
	Pinned          []Comment `json:"pinned"`
}

// ReplyPage is one page of replies under a single root.
type ReplyPage struct {
	Items []Comment `json:"items"`
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
		AuthorKind: string(comment.Comment.AuthorKind), AuthorName: comment.Comment.AuthorName, Author: fromAuthor(comment.Author), ReplyTo: fromReplyTarget(comment.ReplyTo),
		ReplyCount: comment.ReplyCount, RepliesNextCursor: comment.RepliesNextCursor,
		ContentMD: comment.Comment.ContentMD, ContentHTML: comment.ContentHTML, CreatedAt: comment.Comment.CreatedAt.UTC(), Media: media, Location: location,
	}
}

func fromReplyTarget(target *app.CommentReplyTarget) *ReplyTarget {
	if target == nil {
		return nil
	}
	return &ReplyTarget{CommentID: target.CommentID.String(), Author: fromAuthor(target.Author)}
}

func fromAuthor(author app.CommentAuthor) Author {
	value := Author{DisplayName: author.DisplayName, AvatarMediaID: author.AvatarMediaID}
	if author.UserID != "" {
		userID := author.UserID
		value.UserID = &userID
	}
	if author.Username != "" {
		username := author.Username
		value.Username = &username
	}
	if author.ProfileURL != "" {
		profileURL := author.ProfileURL
		value.ProfileURL = &profileURL
	}
	return value
}

// FromThread maps one canonical flattened application thread.
func FromThread(thread app.CommentThread) Thread {
	var pinnedCommentID *string
	if thread.PinnedCommentID != nil {
		value := thread.PinnedCommentID.String()
		pinnedCommentID = &value
	}
	return Thread{Items: fromComments(thread.Items), PinnedCommentID: pinnedCommentID, Pinned: fromComments(thread.Pinned)}
}

// FromReplyPage maps one page of replies under a single root.
func FromReplyPage(page app.CommentReplyPage) ReplyPage {
	return ReplyPage{Items: fromComments(page.Items)}
}

func fromComments(comments []app.Comment) []Comment {
	if comments == nil {
		return nil
	}
	items := make([]Comment, 0, len(comments))
	for _, comment := range comments {
		items = append(items, FromComment(comment))
	}
	return items
}

// NextCursor exposes a page boundary as a nullable wire value.
func NextCursor(cursor string) *string {
	if cursor == "" {
		return nil
	}
	return &cursor
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
	if err := validateAuthor(comment.Author); err != nil {
		return err
	}
	if comment.ReplyTo != nil {
		if parentID == nil {
			return errors.New("a top-level comment cannot answer another comment")
		}
		targetID, parseErr := domain.ParseID(comment.ReplyTo.CommentID)
		if parseErr != nil {
			return parseErr
		}
		if targetID == id {
			return errors.New("a comment cannot answer itself")
		}
		if err := validateAuthor(comment.ReplyTo.Author); err != nil {
			return err
		}
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

// validateAuthor rejects a half-resolved identity. An account either has all
// of its reference fields or none of them; anything in between means a caller
// would render a link that goes nowhere.
func validateAuthor(author Author) error {
	if author.DisplayName == "" {
		return errors.New("comment author display name is empty")
	}
	linked := 0
	for _, value := range []*string{author.UserID, author.Username, author.ProfileURL} {
		if value != nil {
			if *value == "" {
				return errors.New("comment author reference is empty")
			}
			linked++
		}
	}
	if linked != 0 && linked != 3 {
		return errors.New("comment author identity is partially resolved")
	}
	if author.Username != nil && author.ProfileURL != nil && *author.ProfileURL != "/@"+*author.Username {
		return errors.New("comment author profile URL does not match username")
	}
	return nil
}

func validProjectionMIMEType(value string) bool {
	return mediatype.IsSupported(value)
}

// ValidateThread verifies adjacency, identity uniqueness and stable ordering.
//
// It deliberately does not require completeness. A page carries a subset of a
// thread, and a subset still satisfies every property that matters here: roots
// stay in order, and each reply still sits directly behind the root it belongs
// to. The one rule that had to go is "the pinned comment appears in items" —
// under pagination it legitimately may not.
//
// A pinned selection with no conversation beside it is likewise accepted, and
// deliberately: ListPage emits exactly that when the pinned row cannot be
// loaded, and the reader still needs to know which comment is pinned in order
// to mark it if a later page carries it. What is not accepted is a pinned
// conversation belonging to some other post, because the whole response
// describes one post's thread.
func ValidateThread(thread Thread) error {
	if thread.Items == nil {
		return errors.New("comment thread items must be an array")
	}
	if thread.PinnedCommentID != nil {
		if _, err := domain.ParseID(*thread.PinnedCommentID); err != nil {
			return err
		}
	}
	if err := validateCommentRun(thread.Pinned); err != nil {
		return err
	}
	if len(thread.Pinned) > 0 {
		if thread.PinnedCommentID == nil || thread.Pinned[0].ID != *thread.PinnedCommentID || thread.Pinned[0].ParentID != nil {
			return errors.New("pinned conversation does not lead with the pinned root")
		}
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
	// The pinned conversation travels beside the page rather than inside it,
	// but it is still part of this post's thread and has to be checked against
	// it: walking items alone left the two halves of one response free to
	// describe two different posts. Items may be empty while a pinned
	// conversation is present, so either half can be the one that names the
	// post.
	for _, comment := range thread.Pinned {
		if postID == "" {
			postID = comment.PostID
		}
		if comment.PostID != postID {
			return errors.New("comment thread contains multiple posts")
		}
	}
	if thread.PinnedCommentID != nil {
		for _, comment := range thread.Items {
			if comment.ID == *thread.PinnedCommentID && comment.ParentID != nil {
				return errors.New("pinned comment must be a root")
			}
		}
	}
	return nil
}

// ValidateReplyPage checks one page of replies under a single root.
//
// It carries the same whole-page checks as ValidateThread. Sharing one
// parent_id does not imply sharing one post, and stable ordering does not
// imply distinct identities once a repeated reply arrives with a different
// timestamp — which is what concatenating two overlapping pages produces.
func ValidateReplyPage(page ReplyPage) error {
	if page.Items == nil {
		return errors.New("comment reply page items must be an array")
	}
	seen := make(map[string]struct{})
	postID := ""
	parent := ""
	var last *Comment
	for _, comment := range page.Items {
		if err := ValidateComment(comment); err != nil {
			return err
		}
		if comment.ParentID == nil {
			return errors.New("comment reply page contains a root comment")
		}
		if postID == "" {
			postID = comment.PostID
		}
		if comment.PostID != postID {
			return errors.New("comment reply page contains multiple posts")
		}
		if _, duplicate := seen[comment.ID]; duplicate {
			return errors.New("comment reply page contains a duplicate comment")
		}
		seen[comment.ID] = struct{}{}
		if parent == "" {
			parent = *comment.ParentID
		}
		if *comment.ParentID != parent {
			return errors.New("comment reply page mixes conversations")
		}
		if last != nil && !commentAfter(*last, comment) {
			return errors.New("comment replies are not in stable order")
		}
		current := comment
		last = &current
	}
	return nil
}

// validateCommentRun checks a root followed by its own ordered replies.
func validateCommentRun(comments []Comment) error {
	root := ""
	var last *Comment
	for index, comment := range comments {
		if err := ValidateComment(comment); err != nil {
			return err
		}
		if index == 0 {
			if comment.ParentID != nil {
				return errors.New("comment run must lead with a root")
			}
			root = comment.ID
			continue
		}
		if comment.ParentID == nil || *comment.ParentID != root {
			return errors.New("comment reply is not adjacent to its root")
		}
		if last != nil && !commentAfter(*last, comment) {
			return errors.New("comment replies are not in stable order")
		}
		current := comment
		last = &current
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
