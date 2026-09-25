package domain

import "time"

// EntryKind identifies the stable timeline envelope.
type EntryKind string

const (
	// KindMoment is a short post whose body is Markdown.
	KindMoment EntryKind = "moment"
	// KindArticle is a long post with a title, a cover and a structured body.
	// Its body is not Markdown and does not live on the Entry at all.
	KindArticle EntryKind = "article"
)

// EntryKinds lists every accepted value in wire order.
var EntryKinds = []EntryKind{KindMoment, KindArticle}

// ParseEntryKind accepts exactly the wire spellings.
func ParseEntryKind(value string) (EntryKind, error) {
	for _, candidate := range EntryKinds {
		if string(candidate) == value {
			return candidate, nil
		}
	}
	return "", NewError(CodeInvalidArgument, "kind must be moment or article", map[string]any{"field": "kind"})
}

// Valid reports whether the value is one of the accepted kinds.
func (kind EntryKind) Valid() bool {
	_, err := ParseEntryKind(string(kind))
	return err == nil
}

// Visibility names who may read an Entry. The author always reads their own
// entries; the value only ever narrows everybody else.
type Visibility string

const (
	// VisibilityEveryone is the default: any visitor may read the entry.
	VisibilityEveryone Visibility = "everyone"
	// VisibilityOnlyMe hides the entry from everyone but its author.
	VisibilityOnlyMe Visibility = "only_me"
)

// Visibilities lists every accepted value in wire order.
var Visibilities = []Visibility{VisibilityEveryone, VisibilityOnlyMe}

// ParseVisibility accepts exactly the wire spellings. An empty value is not a
// default here; callers that mean "unchanged" or "default" decide that
// themselves so the two never get confused.
func ParseVisibility(value string) (Visibility, error) {
	for _, candidate := range Visibilities {
		if string(candidate) == value {
			return candidate, nil
		}
	}
	return "", NewError(CodeInvalidArgument, "visibility must be everyone or only_me", map[string]any{"field": "visibility"})
}

// Valid reports whether the value is one of the accepted visibilities.
func (visibility Visibility) Valid() bool {
	_, err := ParseVisibility(string(visibility))
	return err == nil
}

// Entry is the authoritative transport-independent timeline item.
type Entry struct {
	ID   ID
	Kind EntryKind
	// ContentMD is the Markdown body of a moment. It is nil exactly when Kind
	// is KindArticle, whose body is structured and lives in its own aggregate.
	// The pointer is deliberate: a reader that forgets which kind it holds
	// fails loudly here instead of rendering an article as a blank post.
	ContentMD   *string
	Visibility  Visibility
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt time.Time
	RepostedAt  *time.Time
	EditedAt    *time.Time
}

// NewEntry validates and creates one Moment at millisecond precision.
func NewEntry(id ID, content string, now time.Time) (Entry, error) {
	if _, err := ParseID(id.String()); err != nil {
		return Entry{}, err
	}
	if err := ValidateMarkdown(content); err != nil {
		return Entry{}, err
	}
	instant := NormalizeTime(now)
	body := content
	return Entry{
		ID:          id,
		Kind:        KindMoment,
		ContentMD:   &body,
		Visibility:  VisibilityEveryone,
		CreatedAt:   instant,
		UpdatedAt:   instant,
		PublishedAt: instant,
	}, nil
}

// MarkdownBody is the body of a moment, ready to be assigned to an Entry.
// Go has no literal for "a pointer to this string", and an Entry body is
// optional precisely so that forgetting which kind you hold is an error rather
// than a blank page.
func MarkdownBody(content string) *string {
	return &content
}

// NewArticleEntry creates the timeline envelope of an article. The title, cover
// and body are validated by the Article aggregate and stored beside this row.
func NewArticleEntry(id ID, now time.Time) (Entry, error) {
	if _, err := ParseID(id.String()); err != nil {
		return Entry{}, err
	}
	instant := NormalizeTime(now)
	return Entry{
		ID:          id,
		Kind:        KindArticle,
		ContentMD:   nil,
		Visibility:  VisibilityEveryone,
		CreatedAt:   instant,
		UpdatedAt:   instant,
		PublishedAt: instant,
	}, nil
}

// Markdown reports the moment body and whether this entry has one at all. The
// comma-ok shape is the point: an article has no Markdown, and a caller that
// wants text has to say what it does in that case.
func (entry Entry) Markdown() (string, bool) {
	if entry.ContentMD == nil {
		return "", false
	}
	return *entry.ContentMD, true
}

// Validate checks an Entry loaded from storage or archive input.
func (entry Entry) Validate() error {
	if !entry.Kind.Valid() {
		return NewError(CodeInvalidArgument, "entry kind is not supported", map[string]any{"field": "kind"})
	}
	if _, err := ParseID(entry.ID.String()); err != nil {
		return err
	}
	// The body belongs to exactly one of the two shapes, and the database
	// carries the same rule as a check constraint.
	switch entry.Kind {
	case KindMoment:
		if entry.ContentMD == nil {
			return NewError(CodeInvalidArgument, "a moment must carry Markdown", map[string]any{"field": "content_md"})
		}
		if err := ValidateMarkdown(*entry.ContentMD); err != nil {
			return err
		}
	case KindArticle:
		if entry.ContentMD != nil {
			return NewError(CodeInvalidArgument, "an article keeps its body outside the entry", map[string]any{"field": "content_md"})
		}
	}
	if !entry.Visibility.Valid() {
		return NewError(CodeInvalidArgument, "entry visibility is not supported", map[string]any{"field": "visibility"})
	}
	if entry.CreatedAt.IsZero() || entry.UpdatedAt.IsZero() || entry.PublishedAt.IsZero() {
		return NewError(CodeInvalidArgument, "entry timestamps are required", map[string]any{"field": "timestamps"})
	}
	if entry.UpdatedAt.Before(entry.CreatedAt) {
		return NewError(CodeInvalidArgument, "updated time cannot precede creation", map[string]any{"field": "updated_at"})
	}
	if entry.EditedAt != nil && (entry.EditedAt.IsZero() || entry.EditedAt.Before(entry.CreatedAt) || entry.EditedAt.After(entry.UpdatedAt)) {
		return NewError(CodeInvalidArgument, "edited time must be between creation and last update", map[string]any{"field": "edited_at"})
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

// SetVisibility changes who may read the entry. It is not a content edit, so
// EditedAt stays where it was: the "edited" mark tells readers the words
// changed, and nothing a reader can see changed here.
func (entry *Entry) SetVisibility(visibility Visibility, now time.Time) error {
	if !visibility.Valid() {
		return NewError(CodeInvalidArgument, "visibility must be everyone or only_me", map[string]any{"field": "visibility"})
	}
	if entry.Visibility == visibility {
		return nil
	}
	updated := NormalizeTime(now)
	if !updated.After(entry.UpdatedAt) {
		updated = entry.UpdatedAt.Add(time.Millisecond)
	}
	entry.Visibility = visibility
	entry.UpdatedAt = updated
	return nil
}

// UpdateContent changes only editable state. Original creation and publication
// instants remain the timeline authority.
func (entry *Entry) UpdateContent(content string, now time.Time) error {
	if entry.Kind != KindMoment {
		return NewError(CodeInvalidArgument, "only a moment has a Markdown body to replace", map[string]any{"field": "kind"})
	}
	if err := ValidateMarkdown(content); err != nil {
		return err
	}
	updated := NormalizeTime(now)
	if updated.Before(entry.CreatedAt) {
		return NewError(CodeInvalidArgument, "updated time cannot precede creation", map[string]any{"field": "updated_at"})
	}
	if previous, ok := entry.Markdown(); !ok || previous != content {
		edited := updated
		entry.EditedAt = &edited
	}
	body := content
	entry.ContentMD = &body
	entry.UpdatedAt = updated
	return nil
}
