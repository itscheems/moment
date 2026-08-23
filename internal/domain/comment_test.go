package domain

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// The refusal states its ceiling twice: once in the sentence a person reads and
// once in the details a client reads. The sentence used to spell the number out
// by hand, so when MaxCommentAuthorNameCodePoints became the display name bound
// the message went on saying 40 beside a details map saying 80 -- an error that
// contradicts itself in front of whoever tripped it.
func TestTheAuthorNameRefusalAgreesWithItself(t *testing.T) {
	t.Parallel()
	_, err := NormalizeCommentAuthorName(strings.Repeat("名", MaxCommentAuthorNameCodePoints+1))
	var refusal *Error
	if !errors.As(err, &refusal) {
		t.Fatalf("a name past the ceiling = %v, want a domain error", err)
	}
	limit := strconv.Itoa(MaxCommentAuthorNameCodePoints)
	if !strings.Contains(refusal.Message, limit) {
		t.Fatalf("message %q does not name the ceiling %s it enforced", refusal.Message, limit)
	}
	if refusal.Details["limit"] != MaxCommentAuthorNameCodePoints {
		t.Fatalf("details limit = %v, want %d", refusal.Details["limit"], MaxCommentAuthorNameCodePoints)
	}
}
