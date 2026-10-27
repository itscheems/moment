package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"

	"github.com/moment/moment/internal/domain"
)

const (
	// DefaultCommentPageSize bounds one page of root comments.
	DefaultCommentPageSize = 20
	// DefaultReplyPageSize bounds one page of replies under a single root. It
	// is smaller than a root page because replies are read as a conversation:
	// a reader opening a long thread wants the next few, not the next twenty.
	DefaultReplyPageSize = 10
	// InlineReplyPreviewCount is how many replies travel with their root before
	// the reader asks for more. Enough to show a conversation is happening,
	// few enough that a popular root cannot dominate the page it sits on.
	InlineReplyPreviewCount = 1

	commentRootCursorV1        = 1
	commentReplyCursorV1       = 1
	commentDirectReplyCursorV1 = 2
)

// CommentSort selects both the keyset seek direction and the ID tie-breaker.
type CommentSort string

const (
	CommentOldest CommentSort = "oldest"
	CommentNewest CommentSort = "newest"
)

func NormalizeCommentSort(value CommentSort) (CommentSort, error) {
	if value == "" {
		return CommentOldest, nil
	}
	if value != CommentOldest && value != CommentNewest {
		return "", domain.NewError(domain.CodeInvalidArgument, "sort must be oldest or newest", map[string]any{"field": "sort"})
	}
	return value, nil
}

// Oldest cursors retain their existing representation; newest is explicit.
func commentCursorSort(value CommentSort) CommentSort {
	if value == CommentNewest {
		return value
	}
	return ""
}

// CommentPosition is the keyset boundary passed to the repository.
type CommentPosition struct {
	CreatedAtMS int64
	ID          domain.ID
}

// The scope each cursor was issued for is echoed inside it and checked on
// decode, the same guard the timeline applies to its date filters: a cursor
// minted for one thread cannot be replayed against another.
type commentRootCursorPayload struct {
	Version     int         `json:"v"`
	EntryID     string      `json:"entry_id"`
	CreatedAtMS int64       `json:"created_at_ms"`
	ID          string      `json:"id"`
	Sort        CommentSort `json:"sort,omitempty"`
}

type commentReplyCursorPayload struct {
	Version     int         `json:"v"`
	RootID      string      `json:"root_id"`
	CreatedAtMS int64       `json:"created_at_ms"`
	ID          string      `json:"id"`
	Sort        CommentSort `json:"sort,omitempty"`
}

func encodeCommentRootCursor(entryID domain.ID, position CommentPosition, sort CommentSort) string {
	payload, err := json.Marshal(commentRootCursorPayload{
		Version: commentRootCursorV1, EntryID: entryID.String(),
		CreatedAtMS: position.CreatedAtMS, ID: position.ID.String(), Sort: commentCursorSort(sort),
	})
	if err != nil {
		panic("fixed comment cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func encodeCommentReplyCursor(rootID domain.ID, position CommentPosition, sort CommentSort) string {
	return encodeScopedCommentReplyCursor(rootID, position, CommentRepliesThread, sort)
}

func encodeScopedCommentReplyCursor(rootID domain.ID, position CommentPosition, scope CommentReplyScope, sort CommentSort) string {
	version := commentReplyCursorV1
	if scope == CommentRepliesDirect {
		version = commentDirectReplyCursorV1
	}
	payload, err := json.Marshal(commentReplyCursorPayload{
		Version: version, RootID: rootID.String(),
		CreatedAtMS: position.CreatedAtMS, ID: position.ID.String(), Sort: commentCursorSort(sort),
	})
	if err != nil {
		panic("fixed comment cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// decodeCommentRootCursor rejects anything this process did not mint.
//
// Integrity comes from strict canonicalisation rather than a signature: a
// cursor grants no authority, so the worst a forged one can do is address a
// different page of already-public data. Requiring the bytes to round trip
// exactly is enough to keep the format ours, and it leaves no key to manage.
func decodeCommentRootCursor(value string, entryID domain.ID, sort CommentSort) (*CommentPosition, error) {
	payloadBytes, ok := decodeCursorBytes(value)
	if !ok {
		if value == "" {
			return nil, nil
		}
		return nil, invalidCursor()
	}
	var payload commentRootCursorPayload
	if err := decodeCanonicalCursor(payloadBytes, &payload); err != nil {
		return nil, err
	}
	if payload.Version != commentRootCursorV1 || payload.EntryID != entryID.String() || payload.Sort != commentCursorSort(sort) {
		return nil, invalidCursor()
	}
	return commentPositionFrom(payload.CreatedAtMS, payload.ID)
}

func decodeScopedCommentReplyCursor(value string, rootID domain.ID, scope CommentReplyScope, sort CommentSort) (*CommentPosition, error) {
	payloadBytes, ok := decodeCursorBytes(value)
	if !ok {
		if value == "" {
			return nil, nil
		}
		return nil, invalidCursor()
	}
	var payload commentReplyCursorPayload
	if err := decodeCanonicalCursor(payloadBytes, &payload); err != nil {
		return nil, err
	}
	version := commentReplyCursorV1
	if scope == CommentRepliesDirect {
		version = commentDirectReplyCursorV1
	}
	if payload.Version != version || payload.RootID != rootID.String() || payload.Sort != commentCursorSort(sort) {
		return nil, invalidCursor()
	}
	return commentPositionFrom(payload.CreatedAtMS, payload.ID)
}

func decodeCursorBytes(value string) ([]byte, bool) {
	if value == "" || len(value) > maxCursorBytes {
		return nil, false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(payloadBytes) != value {
		return nil, false
	}
	return payloadBytes, true
}

// decodeCanonicalCursor refuses payloads that decode successfully but are not
// byte-identical to what encoding the same value would produce. That rules out
// reordered keys, duplicates and trailing bytes, none of which this process
// can emit.
func decodeCanonicalCursor(payloadBytes []byte, payload any) error {
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(payload); err != nil {
		return invalidCursor()
	}
	if err := requireJSONEOF(decoder); err != nil {
		return invalidCursor()
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) {
		return invalidCursor()
	}
	return nil
}

func commentPositionFrom(createdAtMS int64, rawID string) (*CommentPosition, error) {
	id, err := domain.ParseID(rawID)
	if err != nil {
		return nil, invalidCursor()
	}
	return &CommentPosition{CreatedAtMS: createdAtMS, ID: id}, nil
}

func normalizeCommentLimit(limit, fallback int) (int, error) {
	if limit == 0 {
		return fallback, nil
	}
	if limit < 1 || limit > MaxPostPageSize {
		return 0, domain.NewError(domain.CodeInvalidArgument, "limit must be between 1 and 100", map[string]any{
			"field": "limit", "min": 1, "max": MaxPostPageSize,
		})
	}
	return limit, nil
}
