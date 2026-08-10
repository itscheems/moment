package domain

import (
	"io"
	"time"

	"github.com/oklog/ulid/v2"
)

// ID is a canonical 26-character ULID exposed as an opaque string outside the
// domain.
type ID string

// ParseID validates and canonicalizes a Moment identifier.
func ParseID(value string) (ID, error) {
	id, err := ulid.ParseStrict(value)
	if err != nil || id.String() != value {
		return "", NewError(CodeInvalidArgument, "id must be a canonical ULID", map[string]any{"field": "id"})
	}
	return ID(value), nil
}

// NewID creates an identifier using caller-owned time and entropy. Keeping both
// inputs explicit makes tests and development fixtures deterministic.
func NewID(now time.Time, entropy io.Reader) (ID, error) {
	if entropy == nil {
		return "", NewError(CodeInternal, "id entropy source is required", nil)
	}
	id, err := ulid.New(ulid.Timestamp(now.UTC()), entropy)
	if err != nil {
		return "", NewError(CodeInternal, "could not generate id", nil)
	}
	return ID(id.String()), nil
}

// String returns the opaque public representation.
func (id ID) String() string { return string(id) }
