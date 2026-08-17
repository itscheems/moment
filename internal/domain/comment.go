package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxCommentAuthorNameCodePoints is the v0 Unicode code-point limit for a
// public display name.
const MaxCommentAuthorNameCodePoints = 40

// CommentAuthorKind is the only persisted comment identity boundary in v0.
type CommentAuthorKind string

const (
	// CommentAuthorOwner marks an authenticated owner action.
	CommentAuthorOwner CommentAuthorKind = "owner"
	// CommentAuthorGuest marks an anonymous public visitor action.
	CommentAuthorGuest CommentAuthorKind = "guest"
)

// Comment is one top-level comment or one reply to a top-level comment.
type Comment struct {
	ID         ID
	EntryID    ID
	ParentID   *ID
	AuthorKind CommentAuthorKind
	AuthorName string
	ContentMD  string
	Location   *Location
	CreatedAt  time.Time
}

// NormalizeCommentAuthorName trims presentation-only surrounding whitespace
// and rejects control characters that could corrupt flattened text or logs.
func NormalizeCommentAuthorName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", NewError(CodeInvalidArgument, "comment author name must be valid UTF-8", map[string]any{"field": "author_name"})
	}
	value = strings.TrimSpace(value)
	count := utf8.RuneCountInString(value)
	if count == 0 || count > MaxCommentAuthorNameCodePoints {
		return "", NewError(CodeInvalidArgument, "comment author name must contain between 1 and 40 code points", map[string]any{"field": "author_name", "limit": MaxCommentAuthorNameCodePoints})
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", NewError(CodeInvalidArgument, "comment author name cannot contain control characters", map[string]any{"field": "author_name"})
		}
	}
	return value, nil
}

// Validate verifies the transport-independent persisted comment contract.
func (comment Comment) Validate() error {
	if _, err := ParseID(comment.ID.String()); err != nil {
		return err
	}
	if _, err := ParseID(comment.EntryID.String()); err != nil {
		return err
	}
	if comment.ParentID != nil {
		if _, err := ParseID(comment.ParentID.String()); err != nil {
			return err
		}
		if *comment.ParentID == comment.ID {
			return NewError(CodeCommentDepthExceeded, "a comment cannot reply to itself", nil)
		}
	}
	if comment.AuthorKind != CommentAuthorOwner && comment.AuthorKind != CommentAuthorGuest {
		return NewError(CodeInvalidArgument, "comment author kind is invalid", map[string]any{"field": "author_kind"})
	}
	name, err := NormalizeCommentAuthorName(comment.AuthorName)
	if err != nil || name != comment.AuthorName {
		if err != nil {
			return err
		}
		return NewError(CodeInvalidArgument, "comment author name is not normalized", map[string]any{"field": "author_name"})
	}
	if comment.ContentMD == "" {
		if comment.Location == nil {
			return ValidateCommentMarkdown(comment.ContentMD)
		}
	} else if err := ValidateCommentMarkdown(comment.ContentMD); err != nil {
		return err
	}
	if comment.Location != nil {
		if err := comment.Location.Validate(); err != nil {
			return err
		}
	}
	if comment.CreatedAt.IsZero() || comment.CreatedAt != NormalizeTime(comment.CreatedAt) {
		return NewError(CodeInvalidArgument, "comment creation time must be a canonical millisecond instant", map[string]any{"field": "created_at"})
	}
	return nil
}
