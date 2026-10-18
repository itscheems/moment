package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxCommentAuthorNameCodePoints bounds stored historical signatures to the profile name limit.
const MaxCommentAuthorNameCodePoints = MaxDisplayNameLength

// Comment stores a reply relationship, content and optional real account identity.
type Comment struct {
	ID           ID
	EntryID      ID
	ParentID     *ID
	AuthorName   string
	AuthorUserID *string
	// ReplyToCommentID names the comment being answered. It is an annotation,
	// not structure: ParentID alone decides where a reply sits and how deletion
	// cascades, so this field takes no part in ordering, depth or cascade.
	//
	// Only a reply can set it, and the target must belong to the same
	// conversation. Verifying that needs the sibling rows, so the check lives
	// in the store and in the service rather than here.
	ReplyToCommentID *ID
	ContentMD        string
	Location         *Location
	CreatedAt        time.Time
}

// ValidateAuthorUserID accepts the 32 lowercase hex digits an account
// identifier is written with, matching users.id.
func ValidateAuthorUserID(value string) error {
	if len(value) != 32 {
		return NewError(CodeInvalidArgument, "comment author user id must be 32 hex digits", map[string]any{"field": "author_user_id"})
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return NewError(CodeInvalidArgument, "comment author user id must be lowercase hexadecimal", map[string]any{"field": "author_user_id"})
		}
	}
	return nil
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
		// Both halves of the refusal read the constant. Spelling the ceiling
		// out here once said 40 while the details map said 80, which is a
		// message that argues with itself in front of whoever tripped it.
		return "", NewError(CodeInvalidArgument, fmt.Sprintf("comment author name must contain between 1 and %d code points", MaxCommentAuthorNameCodePoints), map[string]any{"field": "author_name", "limit": MaxCommentAuthorNameCodePoints})
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
	name, err := NormalizeCommentAuthorName(comment.AuthorName)
	if err != nil || name != comment.AuthorName {
		if err != nil {
			return err
		}
		return NewError(CodeInvalidArgument, "comment author name is not normalized", map[string]any{"field": "author_name"})
	}
	if comment.AuthorUserID != nil {
		if err := ValidateAuthorUserID(*comment.AuthorUserID); err != nil {
			return err
		}
	}
	if comment.ReplyToCommentID != nil {
		if _, err := ParseID(comment.ReplyToCommentID.String()); err != nil {
			return err
		}
		if comment.ParentID == nil {
			return NewError(CodeInvalidArgument, "a top-level comment cannot answer another comment", map[string]any{"field": "reply_to_comment_id"})
		}
		if *comment.ReplyToCommentID == comment.ID {
			return NewError(CodeInvalidArgument, "a comment cannot answer itself", map[string]any{"field": "reply_to_comment_id"})
		}
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
