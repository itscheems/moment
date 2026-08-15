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
	timelineCursorV3 = 3
	maxCursorBytes   = 512
)

// TimelinePosition is the private keyset boundary passed to repositories.
type TimelinePosition struct {
	TimelineAtMS int64
	ID           domain.ID
}

type timelineCursorPayload struct {
	Version      int    `json:"v"`
	TimelineAtMS int64  `json:"timeline_at_ms"`
	ID           string `json:"id"`
	FromMS       *int64 `json:"from_ms"`
	ToMS         *int64 `json:"to_ms"`
}

func encodeTimelineCursor(position TimelinePosition, fromMS, toMS *int64) string {
	payload, err := json.Marshal(timelineCursorPayload{
		Version:      timelineCursorV3,
		TimelineAtMS: position.TimelineAtMS,
		ID:           position.ID.String(),
		FromMS:       fromMS,
		ToMS:         toMS,
	})
	if err != nil {
		panic("fixed timeline cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeTimelineCursor(value string, fromMS, toMS *int64) (*TimelinePosition, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > maxCursorBytes {
		return nil, invalidCursor()
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(payloadBytes) != value {
		return nil, invalidCursor()
	}
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	var payload timelineCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, invalidCursor()
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, invalidCursor()
	}
	if payload.Version != timelineCursorV3 || !equalOptionalMillis(payload.FromMS, fromMS) || !equalOptionalMillis(payload.ToMS, toMS) {
		return nil, invalidCursor()
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) {
		return nil, invalidCursor()
	}
	id, err := domain.ParseID(payload.ID)
	if err != nil {
		return nil, invalidCursor()
	}
	return &TimelinePosition{TimelineAtMS: payload.TimelineAtMS, ID: id}, nil
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
