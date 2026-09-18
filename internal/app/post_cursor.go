package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/moment/moment/internal/domain"
)

const (
	// DefaultPostPageSize is used when a caller does not specify a limit.
	DefaultPostPageSize = 20
	// MaxPostPageSize bounds repository work for one timeline request.
	MaxPostPageSize  = 100
	timelineCursorV4 = 4
	timelineCursorV5 = 5
	// MaxCursorBytes bounds opaque cursors in client storage.
	MaxCursorBytes = 512
	maxCursorBytes = MaxCursorBytes
)

// TimelineDirection is which way a page reads from its position.
type TimelineDirection string

const (
	// TimelineOlder reads what appeared before the position, newest first: the
	// timeline's own direction, and the only one a cursor knew before v5.
	TimelineOlder TimelineDirection = "older"
	// TimelineNewer reads what appeared at or after the position. The page
	// still comes back newest first -- the store reads ascending and the
	// service turns it around -- so a client prepends a newer page exactly as
	// it appends an older one.
	TimelineNewer TimelineDirection = "newer"
)

// TimelinePosition is the private keyset boundary passed to repositories:
// one row, named by its ordering triple, or -- when Boundary is set -- a bare
// instant with no row behind it, which is how an anchored day names its own
// end before any row has been read. A boundary reads strictly before itself
// in the older direction and at or after itself in the newer one.
type TimelinePosition struct {
	TimelineAtMS int64
	Kind         TimelineItemKind
	ID           domain.ID
	Boundary     bool
}

const timelineCursorBoundaryKind = "boundary"

// The field order is the canonical byte order a decoded cursor must re-encode
// to. `dir` is omitted when empty so a v4 cursor, issued before pages could
// read newer, still re-encodes to the bytes it was issued as.
type timelineCursorPayload struct {
	Version      int    `json:"v"`
	Direction    string `json:"dir,omitempty"`
	TimelineAtMS int64  `json:"timeline_at_ms"`
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	FromMS       *int64 `json:"from_ms"`
	ToMS         *int64 `json:"to_ms"`
}

func encodeTimelineCursor(position TimelinePosition, direction TimelineDirection, fromMS, toMS *int64) string {
	kind := string(position.Kind)
	id := position.ID.String()
	if position.Boundary {
		kind = timelineCursorBoundaryKind
		id = ""
	}
	payload, err := json.Marshal(timelineCursorPayload{
		Version:      timelineCursorV5,
		Direction:    string(direction),
		TimelineAtMS: position.TimelineAtMS,
		Kind:         kind,
		ID:           id,
		FromMS:       fromMS,
		ToMS:         toMS,
	})
	if err != nil {
		panic("fixed timeline cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// decodeTimelineCursor reads a cursor back into the position and direction it
// was issued for. An empty cursor is the head of the timeline, read older.
func decodeTimelineCursor(value string, fromMS, toMS *int64) (*TimelinePosition, TimelineDirection, error) {
	if value == "" {
		return nil, TimelineOlder, nil
	}
	if len(value) > maxCursorBytes {
		return nil, "", invalidCursor()
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(payloadBytes) != value {
		return nil, "", invalidCursor()
	}
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	var payload timelineCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, "", invalidCursor()
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, "", invalidCursor()
	}
	direction := TimelineOlder
	switch payload.Version {
	case timelineCursorV4:
		// Issued before a cursor carried a direction: it has none, and reads
		// the only way a page could then be read.
		if payload.Direction != "" {
			return nil, "", invalidCursor()
		}
	case timelineCursorV5:
		direction = TimelineDirection(payload.Direction)
		if direction != TimelineOlder && direction != TimelineNewer {
			return nil, "", invalidCursor()
		}
	default:
		return nil, "", invalidCursor()
	}
	if !equalOptionalMillis(payload.FromMS, fromMS) || !equalOptionalMillis(payload.ToMS, toMS) {
		return nil, "", invalidCursor()
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) {
		return nil, "", invalidCursor()
	}
	position := TimelinePosition{TimelineAtMS: payload.TimelineAtMS}
	switch payload.Kind {
	case timelineCursorBoundaryKind:
		if payload.Version != timelineCursorV5 || payload.ID != "" {
			return nil, "", invalidCursor()
		}
		position.Boundary = true
	case string(TimelineItemOriginal), string(TimelineItemRepost):
		id, err := domain.ParseID(payload.ID)
		if err != nil {
			return nil, "", invalidCursor()
		}
		position.Kind = TimelineItemKind(payload.Kind)
		position.ID = id
	default:
		return nil, "", invalidCursor()
	}
	return &position, direction, nil
}

func equalOptionalMillis(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}

func normalizePostLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultPostPageSize, nil
	}
	if limit < 1 || limit > MaxPostPageSize {
		return 0, domain.NewError(domain.CodeInvalidArgument, "limit must be between 1 and 100", map[string]any{
			"field": "limit",
			"min":   1,
			"max":   MaxPostPageSize,
		})
	}
	return limit, nil
}

func invalidCursor() error {
	return domain.NewError(domain.CodeInvalidArgument, "cursor is invalid or unsupported", map[string]any{"field": "cursor"})
}
