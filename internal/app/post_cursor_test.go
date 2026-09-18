package app

import (
	"encoding/base64"
	"testing"

	"github.com/moment/moment/internal/domain"
)

const cursorTestID = domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")

func TestTimelineCursorRoundTripAndStrictValidation(t *testing.T) {
	t.Parallel()
	position := TimelinePosition{TimelineAtMS: 1_723_270_150_123, Kind: TimelineItemRepost, ID: cursorTestID}
	fromMS := int64(1_723_000_000_000)
	toMS := int64(1_724_000_000_000)
	for _, direction := range []TimelineDirection{TimelineOlder, TimelineNewer} {
		encoded := encodeTimelineCursor(position, direction, &fromMS, &toMS)
		decoded, decodedDirection, err := decodeTimelineCursor(encoded, &fromMS, &toMS)
		if err != nil {
			t.Fatal(err)
		}
		if *decoded != position || decodedDirection != direction {
			t.Fatalf("decoded = %+v %s, want %+v %s", *decoded, decodedDirection, position, direction)
		}
	}

	// A boundary names an instant and no row.
	boundary := TimelinePosition{TimelineAtMS: 1_723_270_150_123, Boundary: true}
	encoded := encodeTimelineCursor(boundary, TimelineNewer, nil, nil)
	decoded, direction, err := decodeTimelineCursor(encoded, nil, nil)
	if err != nil || *decoded != boundary || direction != TimelineNewer {
		t.Fatalf("boundary decoded = %+v %s, %v", decoded, direction, err)
	}

	// A cursor issued before pages could read newer carries no direction and
	// still continues older, byte for byte as it was issued.
	v4 := base64.RawURLEncoding.EncodeToString([]byte(`{"v":4,"timeline_at_ms":1723270150123,"kind":"repost","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":1723000000000,"to_ms":1724000000000}`))
	decoded, direction, err = decodeTimelineCursor(v4, &fromMS, &toMS)
	if err != nil || *decoded != position || direction != TimelineOlder {
		t.Fatalf("v4 decoded = %+v %s, %v", decoded, direction, err)
	}

	invalidPayloads := []string{
		`{"v":1,"published_at_ms":1,"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		`{"v":4,"timeline_at_ms":1,"kind":"repost","id":"not-an-id","from_ms":null,"to_ms":null}`,
		`{"v":4,"timeline_at_ms":1,"kind":"missing","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		`{"v":4,"timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null,"extra":true}`,
		`{"v":4,"timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null} {}`,
		`{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","timeline_at_ms":1,"kind":"original","v":4,"from_ms":null,"to_ms":null}`,
		`{"v":4,"v":1,"timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		// A direction on a version that never had one, and none on one that must.
		`{"v":4,"dir":"older","timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		`{"v":5,"timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		`{"v":5,"dir":"sideways","timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		// A boundary is not a row, and rows are the only thing v4 could name.
		`{"v":5,"dir":"newer","timeline_at_ms":1,"kind":"boundary","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
		`{"v":4,"timeline_at_ms":1,"kind":"boundary","id":"","from_ms":null,"to_ms":null}`,
		`{"v":6,"dir":"older","timeline_at_ms":1,"kind":"original","id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","from_ms":null,"to_ms":null}`,
	}
	for _, payload := range invalidPayloads {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(payload))
		if _, _, err := decodeTimelineCursor(cursor, nil, nil); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Errorf("payload %q returned %v", payload, err)
		}
	}
	windowed := encodeTimelineCursor(position, TimelineOlder, &fromMS, &toMS)
	for _, cursor := range []string{"%%%", windowed + "=", string(make([]byte, maxCursorBytes+1))} {
		if _, _, err := decodeTimelineCursor(cursor, &fromMS, &toMS); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Errorf("cursor %q returned %v", cursor, err)
		}
	}
	if _, _, err := decodeTimelineCursor(windowed, nil, &toMS); !domain.HasCode(err, domain.CodeInvalidArgument) {
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
