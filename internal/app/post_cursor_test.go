package app

import (
	"encoding/base64"
	"testing"

	"github.com/moment/moment/internal/domain"
)

const cursorTestID = domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")

func TestTimelineCursorRoundTripAndStrictValidation(t *testing.T) {
	t.Parallel()
	position := TimelinePosition{PublishedAtMS: 1_723_270_150_123, ID: cursorTestID}
	fromMS := int64(1_723_000_000_000)
	toMS := int64(1_724_000_000_000)
	encoded := encodeTimelineCursor(position, &fromMS, &toMS)
	decoded, err := decodeTimelineCursor(encoded, &fromMS, &toMS)
	if err != nil {
		t.Fatal(err)
	}
	if *decoded != position {
		t.Fatalf("decoded position = %+v, want %+v", *decoded, position)
	}

	invalidPayloads := []string{
		`{"v":1,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		`{"v":2,"published_at_ms":1,"id":"not-an-id","from_ms":null,"to_ms":null}`,
		`{"v":2,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null,"extra":true}`,
		`{"v":2,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null} {}`,
		`{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","published_at_ms":1,"v":2,"from_ms":null,"to_ms":null}`,
		`{"v":2,"v":1,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
	}
	for _, payload := range invalidPayloads {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(payload))
		if _, err := decodeTimelineCursor(cursor, &fromMS, &toMS); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Errorf("payload %q returned %v", payload, err)
		}
	}
	for _, cursor := range []string{"%%%", encoded + "=", string(make([]byte, maxCursorBytes+1))} {
		if _, err := decodeTimelineCursor(cursor, &fromMS, &toMS); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Errorf("cursor %q returned %v", cursor, err)
		}
	}
	if _, err := decodeTimelineCursor(encoded, nil, &toMS); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("cursor reused with different filters returned %v", err)
	}
}

func TestPostLimitBounds(t *testing.T) {
	t.Parallel()
	if limit, err := normalizePostLimit(0); err != nil || limit != DefaultPostPageSize {
		t.Fatalf("default limit = %d, %v", limit, err)
	}
	for _, limit := range []int{1, MaxPostPageSize} {
		if got, err := normalizePostLimit(limit); err != nil || got != limit {
			t.Fatalf("limit %d = %d, %v", limit, got, err)
		}
	}
	for _, limit := range []int{-1, MaxPostPageSize + 1} {
		if _, err := normalizePostLimit(limit); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("limit %d returned %v", limit, err)
		}
	}
}
