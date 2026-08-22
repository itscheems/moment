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
	InlineReplyPreviewCount = 3

	commentRootCursorV1  = 1
	commentReplyCursorV1 = 1
)

// CommentPosition is the keyset boundary passed to the repository.
//
// Comments are ordered oldest first, the opposite of the timeline, so the seek
// predicate built from this compares greater-than where the timeline compares
// less-than. Getting that backwards silently returns the wrong page rather
// than failing, which is why the repository tests construct two comments
// sharing a millisecond.
type CommentPosition struct {
	CreatedAtMS int64
	ID          domain.ID
}

// The scope each cursor was issued for is echoed inside it and checked on
// decode, the same guard the timeline applies to its date filters: a cursor
// minted for one thread cannot be replayed against another.
type commentRootCursorPayload struct {
	Version     int    `json:"v"`
	EntryID     string `json:"entry_id"`
	CreatedAtMS int64  `json:"created_at_ms"`
	ID          string `json:"id"`
}

type commentReplyCursorPayload struct {
	Version     int    `json:"v"`
	RootID      string `json:"root_id"`
	CreatedAtMS int64  `json:"created_at_ms"`
	ID          string `json:"id"`
}

func encodeCommentRootCursor(entryID domain.ID, position CommentPosition) string {
	payload, err := json.Marshal(commentRootCursorPayload{
		Version: commentRootCursorV1, EntryID: entryID.String(),
		CreatedAtMS: position.CreatedAtMS, ID: position.ID.String(),
	})
	if err != nil {
		panic("fixed comment cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func encodeCommentReplyCursor(rootID domain.ID, position CommentPosition) string {
	payload, err := json.Marshal(commentReplyCursorPayload{
		Version: commentReplyCursorV1, RootID: rootID.String(),
		CreatedAtMS: position.CreatedAtMS, ID: position.ID.String(),
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
func decodeCommentRootCursor(value string, entryID domain.ID) (*CommentPosition, error) {
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
	if payload.Version != commentRootCursorV1 || payload.EntryID != entryID.String() {
		return nil, invalidCursor()
	}
	return commentPositionFrom(payload.CreatedAtMS, payload.ID)
}

func decodeCommentReplyCursor(value string, rootID domain.ID) (*CommentPosition, error) {
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
	if payload.Version != commentReplyCursorV1 || payload.RootID != rootID.String() {
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
