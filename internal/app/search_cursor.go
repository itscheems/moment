package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"

	"github.com/moment/moment/internal/domain"
)

const searchCursorV1 = 1

// SearchPosition is the private relevance keyset passed to repositories.
type SearchPosition struct {
	Rank          float64
	PublishedAtMS int64
	ID            domain.ID
}

type searchCursorPayload struct {
	Version       int    `json:"v"`
	QueryHash     string `json:"query_hash"`
	Revision      int64  `json:"revision"`
	Rank          string `json:"rank"`
	PublishedAtMS int64  `json:"published_at_ms"`
	ID            string `json:"id"`
}

func encodeSearchCursor(position SearchPosition, signature string, revision int64) string {
	payload, err := json.Marshal(searchCursorPayload{
		Version:       searchCursorV1,
		QueryHash:     signature,
		Revision:      revision,
		Rank:          strconv.FormatFloat(position.Rank, 'g', -1, 64),
		PublishedAtMS: position.PublishedAtMS,
		ID:            position.ID.String(),
	})
	if err != nil {
		panic("fixed search cursor payload cannot fail to encode")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeSearchCursor(value, signature string, revision int64) (*SearchPosition, error) {
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
	var payload searchCursorPayload
	if err := decoder.Decode(&payload); err != nil || requireJSONEOF(decoder) != nil {
		return nil, invalidCursor()
	}
	if payload.Version != searchCursorV1 || payload.QueryHash != signature {
		return nil, invalidCursor()
	}
	if payload.Revision != revision {
		return nil, staleSearchCursor()
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) {
		return nil, invalidCursor()
	}
	rank, err := strconv.ParseFloat(payload.Rank, 64)
	if err != nil || math.IsNaN(rank) || math.IsInf(rank, 0) || strconv.FormatFloat(rank, 'g', -1, 64) != payload.Rank {
		return nil, invalidCursor()
	}
	id, err := domain.ParseID(payload.ID)
	if err != nil {
		return nil, invalidCursor()
	}
	return &SearchPosition{Rank: rank, PublishedAtMS: payload.PublishedAtMS, ID: id}, nil
}

func staleSearchCursor() error {
	return domain.NewError(domain.CodeInvalidArgument, "cursor is stale because searchable posts changed", map[string]any{"field": "cursor", "reason": "stale"})
}

func searchSignature(normalizedQuery, timezone string, fromMS, toMS *int64) string {
	payload, err := json.Marshal(struct {
		Version  int    `json:"v"`
		Query    string `json:"q"`
		Timezone string `json:"timezone"`
		FromMS   *int64 `json:"from_ms"`
		ToMS     *int64 `json:"to_ms"`
	}{Version: searchCursorV1, Query: normalizedQuery, Timezone: timezone, FromMS: fromMS, ToMS: toMS})
	if err != nil {
		panic("fixed search signature cannot fail to encode")
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
