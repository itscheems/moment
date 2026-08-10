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
	timelineCursorV1 = 1
	maxCursorBytes   = 512
)

// TimelinePosition is the private keyset boundary passed to repositories.
type TimelinePosition struct {
	PublishedAtMS int64
	ID            domain.ID
}

type timelineCursorPayload struct {
	Version       int    `json:"v"`
	PublishedAtMS int64  `json:"published_at_ms"`
	ID            string `json:"id"`
}

func encodeTimelineCursor(position TimelinePosition) string {
	payload, err := json.Marshal(timelineCursorPayload{
		Version:       timelineCursorV1,
		PublishedAtMS: position.PublishedAtMS,
		ID:            position.ID.String(),
	})
	if err != nil {
		panic("fixed timeline cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeTimelineCursor(value string) (*TimelinePosition, error) {
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
	if payload.Version != timelineCursorV1 {
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
	return &TimelinePosition{PublishedAtMS: payload.PublishedAtMS, ID: id}, nil
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
