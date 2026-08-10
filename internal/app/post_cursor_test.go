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
	encoded := encodeTimelineCursor(position)
	decoded, err := decodeTimelineCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if *decoded != position {
		t.Fatalf("decoded position = %+v, want %+v", *decoded, position)
	}

	invalidPayloads := []string{
		`{"v":2,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
		`{"v":1,"published_at_ms":1,"id":"not-an-id"}`,
		`{"v":1,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","extra":true}`,
		`{"v":1,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"} {}`,
		`{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","published_at_ms":1,"v":1}`,
		`{"v":2,"v":1,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
	}
	for _, payload := range invalidPayloads {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(payload))
		if _, err := decodeTimelineCursor(cursor); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Errorf("payload %q returned %v", payload, err)
		}
	}
	for _, cursor := range []string{"%%%", encoded + "=", string(make([]byte, maxCursorBytes+1))} {
		if _, err := decodeTimelineCursor(cursor); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Errorf("cursor %q returned %v", cursor, err)
		}
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
