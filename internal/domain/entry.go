package domain

import "time"

// EntryKind identifies the stable timeline envelope. v0 intentionally accepts
// only Moment entries.
type EntryKind string

// KindMoment is the only Entry kind accepted by v0 writes and imports.
const KindMoment EntryKind = "moment"

// Entry is the authoritative transport-independent timeline item.
type Entry struct {
	ID          ID
	Kind        EntryKind
	ContentMD   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt time.Time
}

// NewEntry validates and creates one v0 Moment at millisecond precision.
func NewEntry(id ID, content string, now time.Time) (Entry, error) {
	if _, err := ParseID(id.String()); err != nil {
		return Entry{}, err
	}
	if err := ValidateMarkdown(content); err != nil {
		return Entry{}, err
	}
	instant := NormalizeTime(now)
	return Entry{
		ID:          id,
		Kind:        KindMoment,
		ContentMD:   content,
		CreatedAt:   instant,
		UpdatedAt:   instant,
		PublishedAt: instant,
	}, nil
}

// Validate checks an Entry loaded from storage or archive input.
func (entry Entry) Validate() error {
	if entry.Kind != KindMoment {
		return NewError(CodeInvalidArgument, "entry kind is not supported", map[string]any{"field": "kind"})
	}
	if _, err := ParseID(entry.ID.String()); err != nil {
		return err
	}
	if err := ValidateMarkdown(entry.ContentMD); err != nil {
		return err
	}
	if entry.CreatedAt.IsZero() || entry.UpdatedAt.IsZero() || entry.PublishedAt.IsZero() {
		return NewError(CodeInvalidArgument, "entry timestamps are required", map[string]any{"field": "timestamps"})
	}
	if entry.UpdatedAt.Before(entry.CreatedAt) {
		return NewError(CodeInvalidArgument, "updated time cannot precede creation", map[string]any{"field": "updated_at"})
	}
	return nil
}

// UpdateContent changes only editable state. Original creation and publication
// instants remain the timeline authority.
func (entry *Entry) UpdateContent(content string, now time.Time) error {
	if err := ValidateMarkdown(content); err != nil {
		return err
	}
	updated := NormalizeTime(now)
	if updated.Before(entry.CreatedAt) {
		return NewError(CodeInvalidArgument, "updated time cannot precede creation", map[string]any{"field": "updated_at"})
	}
	entry.ContentMD = content
	entry.UpdatedAt = updated
	return nil
}
