package domain

import (
	"testing"
	"time"
)

func TestVisibilityAcceptsExactlyTheWireSpellings(t *testing.T) {
	for _, value := range []string{"everyone", "only_me"} {
		if parsed, err := ParseVisibility(value); err != nil || string(parsed) != value {
			t.Fatalf("ParseVisibility(%q) = %q, %v", value, parsed, err)
		}
	}
	for _, value := range []string{"", "Everyone", "only-me", "onlyme", "public", "friends"} {
		if _, err := ParseVisibility(value); !HasCode(err, CodeInvalidArgument) {
			t.Fatalf("ParseVisibility(%q) accepted: %v", value, err)
		}
	}
	if Visibility("").Valid() || !VisibilityOnlyMe.Valid() {
		t.Fatal("Valid disagrees with ParseVisibility")
	}
}

func TestNewEntryIsPublicAndValidateRefusesAnUnknownAudience(t *testing.T) {
	id, err := NewID(time.UnixMilli(1_700_000_000_000), zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewEntry(id, "hello", time.UnixMilli(1_700_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Visibility != VisibilityEveryone {
		t.Fatalf("new entry visibility = %q", entry.Visibility)
	}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	entry.Visibility = ""
	if err := entry.Validate(); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("empty visibility validated: %v", err)
	}
	entry.Visibility = "friends"
	if err := entry.Validate(); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("unknown visibility validated: %v", err)
	}
}

// Changing who may read is not an edit of what they read: updated_at moves,
// edited_at does not, and repeating the same audience changes nothing.
func TestSetVisibilityMovesUpdatedAtButNotEditedAt(t *testing.T) {
	id, err := NewID(time.UnixMilli(1_700_000_000_000), zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	created := time.UnixMilli(1_700_000_000_000)
	entry, err := NewEntry(id, "hello", created)
	if err != nil {
		t.Fatal(err)
	}
	if err := entry.SetVisibility(VisibilityOnlyMe, created.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if entry.Visibility != VisibilityOnlyMe || !entry.UpdatedAt.Equal(created.Add(time.Second)) || entry.EditedAt != nil {
		t.Fatalf("after hide: %+v", entry)
	}
	updated := entry.UpdatedAt
	if err := entry.SetVisibility(VisibilityOnlyMe, created.Add(time.Hour)); err != nil || !entry.UpdatedAt.Equal(updated) {
		t.Fatalf("repeated audience moved updated_at: %+v %v", entry, err)
	}
	// A clock that stalls still moves the row forward by one millisecond, as
	// every other update does.
	if err := entry.SetVisibility(VisibilityEveryone, created); err != nil || !entry.UpdatedAt.Equal(updated.Add(time.Millisecond)) {
		t.Fatalf("stalled clock: %+v %v", entry, err)
	}
	if err := entry.SetVisibility("friends", created.Add(time.Hour)); !HasCode(err, CodeInvalidArgument) || entry.Visibility != VisibilityEveryone {
		t.Fatalf("unknown audience applied: %+v %v", entry, err)
	}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}
