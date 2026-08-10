package domain

import (
	"strings"
	"unicode/utf8"
)

const (
	// MaxEntryMediaUsages is the product limit for one Moment. Positions are
	// therefore the continuous range 0..8.
	MaxEntryMediaUsages = 9
	// MaxMediaAltCodePoints bounds one usage description without conflating
	// bytes with Unicode code points.
	MaxMediaAltCodePoints = 1_000
	// MaxEntryMediaBytes is the aggregate logical usage budget for one post.
	MaxEntryMediaBytes int64 = 50 << 20
)

// EntryMediaUsage is one ordered use of a managed media object. MediaID is
// intentionally not unique: one object may appear more than once with
// independent alt text.
type EntryMediaUsage struct {
	MediaID  ID
	Position int
	AltText  *string
}

// ValidateEntryMediaUsages enforces the canonical ordered usage contract.
func ValidateEntryMediaUsages(usages []EntryMediaUsage) error {
	if len(usages) > MaxEntryMediaUsages {
		return NewError(CodeInvalidArgument, "a post may contain at most 9 media usages", map[string]any{"field": "media", "max": MaxEntryMediaUsages})
	}
	for index, usage := range usages {
		if usage.Position != index {
			return NewError(CodeInvalidArgument, "media positions must be continuous and match array order", map[string]any{"field": "media", "index": index})
		}
		if _, err := ParseID(usage.MediaID.String()); err != nil {
			return NewError(CodeInvalidArgument, "media_id must be a canonical ULID", map[string]any{"field": "media", "index": index})
		}
		if usage.AltText == nil || *usage.AltText == "" {
			continue
		}
		if !utf8.ValidString(*usage.AltText) || strings.TrimSpace(*usage.AltText) == "" {
			return NewError(CodeInvalidArgument, "alt_text must be null, empty, or contain visible text", map[string]any{"field": "media", "index": index})
		}
		if utf8.RuneCountInString(*usage.AltText) > MaxMediaAltCodePoints {
			return NewError(CodeInvalidArgument, "alt_text exceeds 1000 Unicode code points", map[string]any{"field": "media", "index": index, "max": MaxMediaAltCodePoints})
		}
	}
	return nil
}

// CloneEntryMediaUsages returns a deep copy so idempotency snapshots and DTOs
// cannot be changed through shared alt pointers.
func CloneEntryMediaUsages(usages []EntryMediaUsage) []EntryMediaUsage {
	cloned := make([]EntryMediaUsage, len(usages))
	for index, usage := range usages {
		cloned[index] = usage
		if usage.AltText != nil {
			alt := *usage.AltText
			cloned[index].AltText = &alt
		}
	}
	return cloned
}
