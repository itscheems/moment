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
	RepostedAt  *time.Time
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
	if entry.RepostedAt != nil {
		if entry.RepostedAt.IsZero() || entry.RepostedAt.Before(entry.PublishedAt) {
			return NewError(CodeInvalidArgument, "repost time cannot precede publication", map[string]any{"field": "reposted_at"})
		}
		if entry.UpdatedAt.Before(*entry.RepostedAt) {
			return NewError(CodeInvalidArgument, "updated time cannot precede repost", map[string]any{"field": "updated_at"})
		}
	}
	return nil
}

// TimelineAt is the activity instant used only for public timeline ordering.
// Original publication and archive dates remain anchored to PublishedAt.
func (entry Entry) TimelineAt() time.Time {
	if entry.RepostedAt != nil {
		return *entry.RepostedAt
	}
	return entry.PublishedAt
}

// SetReposted applies or removes the owner's single repost activity while
// preserving the original publication instant.
func (entry *Entry) SetReposted(active bool, now time.Time) {
	updated := NormalizeTime(now)
	if !updated.After(entry.UpdatedAt) {
		updated = entry.UpdatedAt.Add(time.Millisecond)
	}
	entry.UpdatedAt = updated
	if active {
		reposted := updated
		entry.RepostedAt = &reposted
	} else {
		entry.RepostedAt = nil
	}
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
