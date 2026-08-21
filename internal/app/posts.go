// Package app implements Moment's transport-independent use cases and ports.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/moment/moment/internal/domain"
)

const postCreateIdempotencyScope = "posts.create.v1"

// PostStore is the query and command surface available inside one repository
// transaction. Implementations must not retain it after the callback returns.
type PostStore interface {
	EntryMediaReferenceStore
	InsertEntry(context.Context, domain.Entry, string) error
	EntryByID(context.Context, domain.ID) (domain.Entry, bool, error)
	ListEntries(context.Context, TimelineQuery, int) ([]TimelineEntry, error)
	CommentCounts(context.Context, []domain.ID) (map[domain.ID]int, error)
	EntryMediaUsages(context.Context, domain.ID) ([]MediaUsageProjection, error)
	ProjectMediaUsages(context.Context, []domain.EntryMediaUsage) ([]MediaUsageProjection, error)
	ReplaceEntryMediaUsages(context.Context, domain.ID, []domain.EntryMediaUsage) error
	EntryLocation(context.Context, domain.ID) (*domain.Location, error)
	EntryLinkCards(context.Context, domain.ID) ([]domain.LinkCard, error)
	ReplaceEntryLinkCards(context.Context, domain.ID, []domain.LinkCard) error
	EntryLinkCardMediaIDs(context.Context, domain.ID) ([]string, error)
	EntryQuote(context.Context, domain.ID) (EntryQuoteReference, error)
	ReplaceEntryQuote(context.Context, domain.ID, *domain.ID) error
	ReplaceEntryLocation(context.Context, domain.ID, *domain.Location) error
	UpdateEntry(context.Context, domain.Entry, string) (bool, error)
	SetEntryRepostedAt(context.Context, domain.ID, *time.Time, time.Time) (bool, error)
	DeleteEntry(context.Context, domain.ID) (bool, error)
	IdempotencyRecord(context.Context, string, string) (IdempotencyRecord, bool, error)
	InsertIdempotencyRecord(context.Context, IdempotencyRecord) error
}

// PostTransactions supplies consistent read and atomic write transactions to
// PostService without exposing SQL or connection ownership.
type PostTransactions interface {
	Read(context.Context, func(PostStore) error) error
	Write(context.Context, func(PostStore) error) error
}

// IdempotencyRecord is the persisted retry decision for a create request.
type IdempotencyRecord struct {
	Scope              string
	Key                string
	RequestHash        string
	ResponseResourceID domain.ID
	CreatedAt          time.Time
}

// Post is an Entry together with its safe derived representation. Entry stays
// the canonical domain authority; ContentHTML is always rebuilt from ContentMD.
type Post struct {
	Entry        domain.Entry
	TimelineKind TimelineItemKind
	ContentHTML  string
	Media        []MediaUsageProjection
	Location     *domain.Location
	LinkCards    []domain.LinkCard
	CommentCount int
	Quoted       *QuotedPost
}

// TimelineItemKind distinguishes the canonical publication from the owner's
// later repost activity. Both projections point at the same authoritative
// Entry and therefore share media, comments, detail URL, and undo state.
type TimelineItemKind string

const (
	// TimelineItemOriginal is the canonical publication surface.
	TimelineItemOriginal TimelineItemKind = "original"
	// TimelineItemRepost is the linked owner repost activity surface.
	TimelineItemRepost TimelineItemKind = "repost"
)

// TimelineEntry is the repository projection used only by chronological
// lists. Repost does not clone an Entry; it creates a second activity surface.
type TimelineEntry struct {
	Entry domain.Entry
	Kind  TimelineItemKind
}

// TimelineAt returns the ordering instant for this specific list projection.
func (item TimelineEntry) TimelineAt() time.Time {
	if item.Kind == TimelineItemRepost && item.Entry.RepostedAt != nil {
		return *item.Entry.RepostedAt
	}
	return item.Entry.PublishedAt
}

// QuotedPost is the non-recursive public projection embedded by a quote Moment.
type QuotedPost struct {
	Unavailable bool
	Entry       domain.Entry
	ContentHTML string
	Media       []MediaUsageProjection
	Location    *domain.Location
}

// EntryQuoteReference preserves the difference between an entry that never
// quoted another Moment and a quote whose target has since been deleted.
// TargetID is nil only for the latter state.
type EntryQuoteReference struct {
	Present  bool
	TargetID *domain.ID
}

// CreatePostInput contains only application semantics. Authentication,
// headers, flags, and transport DTOs belong to adapters.
type CreatePostInput struct {
	ContentMD      string
	Media          []domain.EntryMediaUsage
	Location       *domain.Location
	LinkCards      []domain.LinkCard
	QuotedPostID   *domain.ID
	IdempotencyKey string
}

// CreatePostResult reports whether a retry returned an existing committed
// Entry. Adapters may expose Replayed as response metadata.
type CreatePostResult struct {
	Post     Post
	Replayed bool
}

// UpdatePostInput replaces the authoritative Markdown of one Entry.
type UpdatePostInput struct {
	ID              domain.ID
	ContentMD       string
	PreserveContent bool
	// Media nil preserves existing usages. A non-nil empty slice clears them;
	// every other non-nil value is the complete ordered replacement.
	Media *[]domain.EntryMediaUsage
	// Location nil preserves the current snapshot. A non-nil pointer whose
	// value is nil clears it; every other value replaces it atomically.
	Location **domain.Location
	// LinkCards nil preserves the current cards. A non-nil empty slice clears
	// them; every other non-nil value is the complete ordered replacement. This
	// follows the media-usage list convention rather than the single-value
	// double-pointer convention Location uses.
	LinkCards *[]domain.LinkCard
}

// ListPostsInput requests one reverse-chronological timeline page. A zero
// limit selects DefaultPostPageSize.
type ListPostsInput struct {
	Cursor   string
	Limit    int
	FromDate string
	ToDate   string
	Timezone string
}

// TimelineQuery is the repository-facing keyset and UTC half-open date range.
// Adapters pass calendar strings to PostService and never calculate day
// boundaries themselves.
type TimelineQuery struct {
	Position *TimelinePosition
	FromMS   *int64
	ToMS     *int64
}

// PostPage is one stable keyset page.
type PostPage struct {
	Items      []Post
	NextCursor string
}

// PostClock is injected to keep time deterministic in tests and development
// scenarios.
type PostClock func() time.Time

// PostIDGenerator is injected to keep identity deterministic in tests and
// development scenarios.
type PostIDGenerator func(time.Time) (domain.ID, error)

// PostServiceOption customizes deterministic dependencies without weakening
// the production defaults.
type PostServiceOption func(*PostService) error

// WithPostClock supplies the service clock.
func WithPostClock(clock PostClock) PostServiceOption {
	return func(service *PostService) error {
		if clock == nil {
			return errors.New("post clock is required")
		}
		service.clock = clock
		return nil
	}
}

// WithPostIDGenerator supplies the Entry ID generator.
func WithPostIDGenerator(generator PostIDGenerator) PostServiceOption {
	return func(service *PostService) error {
		if generator == nil {
			return errors.New("post ID generator is required")
		}
		service.ids = generator
		return nil
	}
}

// PostService owns plain-text post behavior shared by future HTTP and CLI
// adapters.
type PostService struct {
	transactions PostTransactions
	clock        PostClock
	ids          PostIDGenerator
}

// NewPostService assembles the post use cases with secure production identity
// entropy and an injectable clock.
func NewPostService(transactions PostTransactions, options ...PostServiceOption) (*PostService, error) {
	if transactions == nil {
		return nil, errors.New("post transactions are required")
	}
	service := &PostService{
		transactions: transactions,
		clock:        time.Now,
		ids: func(now time.Time) (domain.ID, error) {
			return domain.NewID(now, rand.Reader)
		},
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("post service option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// Create validates and atomically persists an Entry and its retry record.
func (service *PostService) Create(ctx context.Context, input CreatePostInput) (CreatePostResult, error) {
	if err := contextError(ctx); err != nil {
		return CreatePostResult{}, err
	}
	requestHash := hashCreatePost(input.ContentMD, input.Media, input.Location, input.LinkCards, input.QuotedPostID)
	if input.IdempotencyKey != "" {
		replayed, found, err := service.loadCreateReplay(ctx, input, requestHash)
		if err != nil {
			return CreatePostResult{}, err
		}
		if found {
			return replayed, nil
		}
	}
	if err := domain.ValidateEntryMediaUsages(input.Media); err != nil {
		return CreatePostResult{}, err
	}
	if input.Location != nil {
		if err := input.Location.Validate(); err != nil {
			return CreatePostResult{}, err
		}
	}
	if err := domain.ValidateLinkCards(input.LinkCards); err != nil {
		return CreatePostResult{}, err
	}
	if err := validatePostAggregate(input.ContentMD, len(input.Media)); err != nil {
		return CreatePostResult{}, err
	}
	if _, err := domain.RenderMarkdown(input.ContentMD); err != nil {
		return CreatePostResult{}, err
	}
	contentText, err := domain.NormalizeContentV2(input.ContentMD)
	if err != nil {
		return CreatePostResult{}, err
	}

	result := CreatePostResult{}

	err = service.transactions.Write(ctx, func(store PostStore) error {
		if input.QuotedPostID != nil {
			if _, found, quoteErr := store.EntryByID(ctx, *input.QuotedPostID); quoteErr != nil {
				return quoteErr
			} else if !found {
				return postNotFound(*input.QuotedPostID)
			}
		}
		if input.IdempotencyKey != "" {
			record, found, storeErr := store.IdempotencyRecord(ctx, postCreateIdempotencyScope, input.IdempotencyKey)
			if storeErr != nil {
				return storeErr
			}
			if found {
				post, replayErr := reconstructCreateReplay(input, requestHash, record)
				if replayErr != nil {
					return replayErr
				}
				counts, replayErr := store.CommentCounts(ctx, []domain.ID{record.ResponseResourceID})
				if replayErr != nil {
					return replayErr
				}
				post.CommentCount = counts[record.ResponseResourceID]
				post.Media, replayErr = store.ProjectMediaUsages(ctx, input.Media)
				if replayErr != nil {
					return replayErr
				}
				result.Post = post
				result.Replayed = true
				return nil
			}
		}

		now := domain.NormalizeTime(service.clock())
		id, generateErr := service.ids(now)
		if generateErr != nil {
			return generateErr
		}
		entry, createErr := domain.NewEntry(id, input.ContentMD, now)
		if createErr != nil {
			return createErr
		}
		if insertErr := store.InsertEntry(ctx, entry, contentText); insertErr != nil {
			return insertErr
		}
		if err := store.ReplaceEntryMediaUsages(ctx, entry.ID, input.Media); err != nil {
			return err
		}
		if err := store.ReplaceEntryLocation(ctx, entry.ID, input.Location); err != nil {
			return err
		}
		if err := store.ReplaceEntryLinkCards(ctx, entry.ID, input.LinkCards); err != nil {
			return err
		}
		if err := store.ReplaceEntryQuote(ctx, entry.ID, input.QuotedPostID); err != nil {
			return err
		}
		affected := append(mediaUsageIDs(input.Media), linkCardMediaIDs(input.LinkCards)...)
		if err := store.ReconcileMediaReferences(ctx, affected, now); err != nil {
			return err
		}
		if input.IdempotencyKey != "" {
			if insertErr := store.InsertIdempotencyRecord(ctx, IdempotencyRecord{
				Scope:              postCreateIdempotencyScope,
				Key:                input.IdempotencyKey,
				RequestHash:        requestHash,
				ResponseResourceID: entry.ID,
				CreatedAt:          now,
			}); insertErr != nil {
				return insertErr
			}
		}
		result.Post.Entry = entry
		projectedMedia, projectErr := store.ProjectMediaUsages(ctx, input.Media)
		if projectErr != nil {
			return projectErr
		}
		result.Post.Media = projectedMedia
		result.Post.Location = domain.CloneLocation(input.Location)
		result.Post.LinkCards = domain.CloneLinkCards(input.LinkCards)
		if input.QuotedPostID != nil {
			quoted, quoteErr := loadQuotedPost(ctx, store, *input.QuotedPostID)
			if quoteErr != nil {
				return quoteErr
			}
			result.Post.Quoted = quoted
		}
		return nil
	})
	if err != nil {
		return CreatePostResult{}, normalizePostRepositoryError(err)
	}
	result.Post.ContentHTML, err = domain.RenderMarkdown(result.Post.Entry.ContentMD)
	if err != nil {
		return CreatePostResult{}, storedEntryError(result.Post.Entry.ID)
	}
	return result, nil
}

func (service *PostService) loadCreateReplay(ctx context.Context, input CreatePostInput, requestHash string) (CreatePostResult, bool, error) {
	var record IdempotencyRecord
	var found bool
	var commentCount int
	var media []MediaUsageProjection
	var post Post
	err := service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		record, found, storeErr = store.IdempotencyRecord(ctx, postCreateIdempotencyScope, input.IdempotencyKey)
		if storeErr != nil || !found {
			return storeErr
		}
		post, storeErr = reconstructCreateReplay(input, requestHash, record)
		if storeErr != nil {
			return storeErr
		}
		counts, storeErr := store.CommentCounts(ctx, []domain.ID{record.ResponseResourceID})
		commentCount = counts[record.ResponseResourceID]
		if storeErr != nil {
			return storeErr
		}
		media, storeErr = store.ProjectMediaUsages(ctx, input.Media)
		if storeErr != nil {
			return storeErr
		}
		if input.QuotedPostID != nil {
			post.Quoted, storeErr = loadQuotedPost(ctx, store, *input.QuotedPostID)
		}
		return storeErr
	})
	if err != nil {
		return CreatePostResult{}, false, normalizePostRepositoryError(err)
	}
	if !found {
		return CreatePostResult{}, false, nil
	}
	quoted := post.Quoted
	post, err = renderPost(post.Entry, media, post.Location, post.LinkCards, commentCount)
	if err != nil {
		return CreatePostResult{}, false, idempotencySnapshotError()
	}
	post.Quoted = quoted
	return CreatePostResult{Post: post, Replayed: true}, true, nil
}

func reconstructCreateReplay(input CreatePostInput, requestHash string, record IdempotencyRecord) (Post, error) {
	if record.Scope != postCreateIdempotencyScope || record.Key != input.IdempotencyKey || !validRequestHash(record.RequestHash) {
		return Post{}, idempotencySnapshotError()
	}
	if record.RequestHash != requestHash {
		return Post{}, domain.NewError(domain.CodeIdempotencyKeyReused, "idempotency key was already used with a different post payload", map[string]any{"field": "idempotency_key"})
	}
	if _, err := domain.ParseID(record.ResponseResourceID.String()); err != nil ||
		domain.TimeToMillis(record.CreatedAt) <= 0 || record.CreatedAt != domain.NormalizeTime(record.CreatedAt) {
		return Post{}, idempotencySnapshotError()
	}
	if err := domain.ValidateEntryMediaUsages(input.Media); err != nil {
		return Post{}, idempotencySnapshotError()
	}
	if input.Location != nil {
		if err := input.Location.Validate(); err != nil {
			return Post{}, idempotencySnapshotError()
		}
	}
	if err := validatePostAggregate(input.ContentMD, len(input.Media)); err != nil {
		return Post{}, idempotencySnapshotError()
	}
	entry, err := domain.NewEntry(record.ResponseResourceID, input.ContentMD, record.CreatedAt)
	if err != nil {
		return Post{}, idempotencySnapshotError()
	}
	if err := entry.Validate(); err != nil {
		return Post{}, idempotencySnapshotError()
	}
	return Post{Entry: entry, Location: domain.CloneLocation(input.Location)}, nil
}

// Get returns one post or a stable not-found error.
func (service *PostService) Get(ctx context.Context, id domain.ID) (Post, error) {
	if err := contextError(ctx); err != nil {
		return Post{}, err
	}
	if err := validatePostID(id); err != nil {
		return Post{}, err
	}
	var entry domain.Entry
	var media []MediaUsageProjection
	var location *domain.Location
	var linkCards []domain.LinkCard
	var commentCount int
	var quoted *QuotedPost
	var found bool
	err := service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		entry, found, storeErr = store.EntryByID(ctx, id)
		if storeErr != nil || !found {
			return storeErr
		}
		media, storeErr = store.EntryMediaUsages(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		location, storeErr = store.EntryLocation(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		linkCards, storeErr = store.EntryLinkCards(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		counts, storeErr := store.CommentCounts(ctx, []domain.ID{id})
		commentCount = counts[id]
		if storeErr != nil {
			return storeErr
		}
		quoted, storeErr = loadEntryQuote(ctx, store, id)
		return storeErr
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	if !found {
		return Post{}, postNotFound(id)
	}
	post, err := renderPost(entry, media, location, linkCards, commentCount)
	post.Quoted = quoted
	return post, err
}

// SetReposted toggles the owner's repost activity. Repeating the requested
// state is idempotent and does not move the timeline again.
func (service *PostService) SetReposted(ctx context.Context, id domain.ID, active bool) (Post, error) {
	if err := contextError(ctx); err != nil {
		return Post{}, err
	}
	if err := validatePostID(id); err != nil {
		return Post{}, err
	}
	var result Post
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(id)
		}
		alreadyActive := entry.RepostedAt != nil
		if alreadyActive != active {
			entry.SetReposted(active, service.clock())
			changed, updateErr := store.SetEntryRepostedAt(ctx, id, entry.RepostedAt, entry.UpdatedAt)
			if updateErr != nil {
				return updateErr
			}
			if !changed {
				return postNotFound(id)
			}
		}
		media, storeErr := store.EntryMediaUsages(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		location, storeErr := store.EntryLocation(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		linkCards, storeErr := store.EntryLinkCards(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		counts, storeErr := store.CommentCounts(ctx, []domain.ID{id})
		if storeErr != nil {
			return storeErr
		}
		quoted, quoteErr := loadEntryQuote(ctx, store, id)
		if quoteErr != nil {
			return quoteErr
		}
		result = Post{Entry: entry, Media: media, Location: location, LinkCards: linkCards, CommentCount: counts[id], Quoted: quoted}
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	quoted := result.Quoted
	result, err = renderPost(result.Entry, result.Media, result.Location, result.LinkCards, result.CommentCount)
	result.Quoted = quoted
	return result, err
}

// List returns one deterministic reverse-chronological keyset page.
func (service *PostService) List(ctx context.Context, input ListPostsInput) (PostPage, error) {
	if err := contextError(ctx); err != nil {
		return PostPage{}, err
	}
	limit, err := normalizePostLimit(input.Limit)
	if err != nil {
		return PostPage{}, err
	}
	fromMS, toMS, err := postDateRange(input.FromDate, input.ToDate, input.Timezone)
	if err != nil {
		return PostPage{}, err
	}
	position, err := decodeTimelineCursor(input.Cursor, fromMS, toMS)
	if err != nil {
		return PostPage{}, err
	}

	var entries []TimelineEntry
	mediaByEntry := make(map[domain.ID][]MediaUsageProjection)
	locationByEntry := make(map[domain.ID]*domain.Location)
	linkCardsByEntry := make(map[domain.ID][]domain.LinkCard)
	commentCounts := make(map[domain.ID]int)
	quotedByEntry := make(map[domain.ID]*QuotedPost)
	err = service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		entries, storeErr = store.ListEntries(ctx, TimelineQuery{Position: position, FromMS: fromMS, ToMS: toMS}, limit+1)
		if storeErr != nil {
			return storeErr
		}
		ids := make([]domain.ID, 0, len(entries))
		loadedEntries := make(map[domain.ID]struct{}, len(entries))
		for _, item := range entries {
			entry := item.Entry
			if _, loaded := loadedEntries[entry.ID]; loaded {
				continue
			}
			loadedEntries[entry.ID] = struct{}{}
			ids = append(ids, entry.ID)
			mediaByEntry[entry.ID], storeErr = store.EntryMediaUsages(ctx, entry.ID)
			if storeErr != nil {
				return storeErr
			}
			locationByEntry[entry.ID], storeErr = store.EntryLocation(ctx, entry.ID)
			if storeErr != nil {
				return storeErr
			}
			linkCardsByEntry[entry.ID], storeErr = store.EntryLinkCards(ctx, entry.ID)
			if storeErr != nil {
				return storeErr
			}
			quotedByEntry[entry.ID], storeErr = loadEntryQuote(ctx, store, entry.ID)
			if storeErr != nil {
				return storeErr
			}
		}
		commentCounts, storeErr = store.CommentCounts(ctx, ids)
		return storeErr
	})
	if err != nil {
		return PostPage{}, normalizePostRepositoryError(err)
	}

	hasNext := len(entries) > limit
	if hasNext {
		entries = entries[:limit]
	}
	page := PostPage{Items: make([]Post, 0, len(entries))}
	for _, item := range entries {
		entry := item.Entry
		post, renderErr := renderPost(entry, mediaByEntry[entry.ID], locationByEntry[entry.ID], linkCardsByEntry[entry.ID], commentCounts[entry.ID])
		if renderErr != nil {
			return PostPage{}, renderErr
		}
		post.Quoted = quotedByEntry[entry.ID]
		post.TimelineKind = item.Kind
		page.Items = append(page.Items, post)
	}
	if hasNext {
		last := entries[len(entries)-1]
		page.NextCursor = encodeTimelineCursor(TimelinePosition{
			TimelineAtMS: domain.TimeToMillis(last.TimelineAt()),
			Kind:         last.Kind,
			ID:           last.Entry.ID,
		}, fromMS, toMS)
	}
	return page, nil
}

func postDateRange(fromDate, toDate, timezone string) (*int64, *int64, error) {
	if fromDate == "" && toDate == "" {
		return nil, nil, nil
	}
	if err := validateTimezone(timezone); err != nil {
		return nil, nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is invalid", map[string]any{"field": "timezone"})
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is unavailable", map[string]any{"field": "timezone"})
	}
	parse := func(field, value string) (*int64, error) {
		if value == "" {
			return nil, nil
		}
		instant, parseErr := time.ParseInLocation("2006-01-02", value, location)
		if parseErr != nil || instant.Format("2006-01-02") != value {
			return nil, domain.NewError(domain.CodeInvalidArgument, field+" must use YYYY-MM-DD", map[string]any{"field": field})
		}
		milliseconds := domain.TimeToMillis(instant)
		return &milliseconds, nil
	}
	fromMS, err := parse("from", fromDate)
	if err != nil {
		return nil, nil, err
	}
	toMS, err := parse("to", toDate)
	if err != nil {
		return nil, nil, err
	}
	if fromMS != nil && toMS != nil && *fromMS >= *toMS {
		return nil, nil, domain.NewError(domain.CodeInvalidArgument, "from must be earlier than to", map[string]any{"fields": []string{"from", "to"}})
	}
	return fromMS, toMS, nil
}

// Update replaces content while preserving creation and publication instants.
// UpdatedAt advances by at least one millisecond even if the clock stalls or
// moves backwards.
func (service *PostService) Update(ctx context.Context, input UpdatePostInput) (Post, error) {
	if err := contextError(ctx); err != nil {
		return Post{}, err
	}
	if err := validatePostID(input.ID); err != nil {
		return Post{}, err
	}
	if input.PreserveContent && input.Media == nil && input.Location == nil && input.LinkCards == nil {
		return Post{}, domain.NewError(domain.CodeInvalidArgument, "post update must change content_md, media, location, or link cards", map[string]any{"field": "body"})
	}
	if input.Media != nil {
		if err := domain.ValidateEntryMediaUsages(*input.Media); err != nil {
			return Post{}, err
		}
	}
	if input.Location != nil && *input.Location != nil {
		if err := (*input.Location).Validate(); err != nil {
			return Post{}, err
		}
	}
	var updated domain.Entry
	var updatedMedia []MediaUsageProjection
	var updatedLocation *domain.Location
	var updatedLinkCards []domain.LinkCard
	var updatedQuoted *QuotedPost
	var commentCount int
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, input.ID)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(input.ID)
		}
		currentMedia, mediaErr := store.EntryMediaUsages(ctx, input.ID)
		if mediaErr != nil {
			return mediaErr
		}
		currentLocation, mediaErr := store.EntryLocation(ctx, input.ID)
		if mediaErr != nil {
			return mediaErr
		}
		currentLinkCards, mediaErr := store.EntryLinkCards(ctx, input.ID)
		if mediaErr != nil {
			return mediaErr
		}
		quoteReference, mediaErr := store.EntryQuote(ctx, input.ID)
		if mediaErr != nil {
			return mediaErr
		}
		counts, mediaErr := store.CommentCounts(ctx, []domain.ID{input.ID})
		if mediaErr != nil {
			return mediaErr
		}
		commentCount = counts[input.ID]
		content := input.ContentMD
		if input.PreserveContent {
			content = entry.ContentMD
		}
		updatedMedia = currentMedia
		updatedLocation = currentLocation
		updatedLinkCards = currentLinkCards
		if input.Media != nil {
			updatedMedia, mediaErr = store.ProjectMediaUsages(ctx, *input.Media)
			if mediaErr != nil {
				return mediaErr
			}
		}
		if input.Location != nil {
			updatedLocation = domain.CloneLocation(*input.Location)
		}
		if input.LinkCards != nil {
			if mediaErr := domain.ValidateLinkCards(*input.LinkCards); mediaErr != nil {
				return mediaErr
			}
			updatedLinkCards = domain.CloneLinkCards(*input.LinkCards)
		}
		if mediaErr := validatePostAggregate(content, len(updatedMedia)); mediaErr != nil {
			return mediaErr
		}
		if _, mediaErr := domain.RenderMarkdown(content); mediaErr != nil {
			return mediaErr
		}
		contentText, mediaErr := domain.NormalizeContentV2(content)
		if mediaErr != nil {
			return mediaErr
		}
		now := domain.NormalizeTime(service.clock())
		if !now.After(entry.UpdatedAt) {
			now = entry.UpdatedAt.Add(time.Millisecond)
		}
		if updateErr := entry.UpdateContent(content, now); updateErr != nil {
			return updateErr
		}
		changed, updateErr := store.UpdateEntry(ctx, entry, contentText)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			return postNotFound(input.ID)
		}
		if input.Media != nil {
			if mediaErr := store.ReplaceEntryMediaUsages(ctx, input.ID, *input.Media); mediaErr != nil {
				return mediaErr
			}
			affected := append(mediaProjectionIDs(currentMedia), mediaUsageIDs(*input.Media)...)
			if mediaErr := store.ReconcileMediaReferences(ctx, affected, now); mediaErr != nil {
				return mediaErr
			}
		}
		if input.Location != nil {
			if updateErr := store.ReplaceEntryLocation(ctx, input.ID, *input.Location); updateErr != nil {
				return updateErr
			}
		}
		if input.LinkCards != nil {
			if updateErr := store.ReplaceEntryLinkCards(ctx, input.ID, *input.LinkCards); updateErr != nil {
				return updateErr
			}
			// Old and new thumbnails are reconciled together in this same
			// transaction so a replaced card never leaves a stale clock.
			affectedCards := append(linkCardMediaIDs(currentLinkCards), linkCardMediaIDs(*input.LinkCards)...)
			if updateErr := store.ReconcileMediaReferences(ctx, affectedCards, now); updateErr != nil {
				return updateErr
			}
		}
		updated = entry
		if quoteReference.Present {
			updatedQuoted, mediaErr = loadEntryQuote(ctx, store, input.ID)
			if mediaErr != nil {
				return mediaErr
			}
		}
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	result, err := renderPost(updated, updatedMedia, updatedLocation, updatedLinkCards, commentCount)
	result.Quoted = updatedQuoted
	return result, err
}

// Delete removes one Entry in a write transaction. Database foreign keys own
// the required comment and media-usage cascades.
func (service *PostService) Delete(ctx context.Context, id domain.ID) (Post, error) {
	if err := contextError(ctx); err != nil {
		return Post{}, err
	}
	if err := validatePostID(id); err != nil {
		return Post{}, err
	}
	var deleted Post
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(id)
		}
		mediaUsages, mediaErr := store.EntryMediaUsages(ctx, id)
		if mediaErr != nil {
			return mediaErr
		}
		location, mediaErr := store.EntryLocation(ctx, id)
		if mediaErr != nil {
			return mediaErr
		}
		linkCards, mediaErr := store.EntryLinkCards(ctx, id)
		if mediaErr != nil {
			return mediaErr
		}
		quoted, mediaErr := loadEntryQuote(ctx, store, id)
		if mediaErr != nil {
			return mediaErr
		}
		counts, mediaErr := store.CommentCounts(ctx, []domain.ID{id})
		if mediaErr != nil {
			return mediaErr
		}
		affectedMedia, mediaErr := store.EntryMediaIDs(ctx, id)
		if mediaErr != nil {
			return mediaErr
		}
		// Cascade removes the card rows, so its thumbnails must be reconciled
		// alongside the entry's own media once the delete has committed.
		affectedMedia = append(affectedMedia, linkCardMediaIDs(linkCards)...)
		changed, deleteErr := store.DeleteEntry(ctx, id)
		if deleteErr != nil {
			return deleteErr
		}
		if !changed {
			return postNotFound(id)
		}
		if err := store.ReconcileMediaReferences(ctx, affectedMedia, domain.NormalizeTime(service.clock())); err != nil {
			return err
		}
		deleted = Post{Entry: entry, Media: cloneMediaUsageProjections(mediaUsages), Location: domain.CloneLocation(location), LinkCards: domain.CloneLinkCards(linkCards), CommentCount: counts[id], Quoted: quoted}
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	quoted := deleted.Quoted
	deleted, err = renderPost(deleted.Entry, deleted.Media, deleted.Location, deleted.LinkCards, deleted.CommentCount)
	deleted.Quoted = quoted
	return deleted, err
}

func hashCreatePost(content string, media []domain.EntryMediaUsage, location *domain.Location, cards []domain.LinkCard, quotedPostID *domain.ID) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("moment-post-create-v1\x00"))
	writeHashLength(digest, len(content))
	_, _ = digest.Write([]byte(content))
	// Keep the historical text-only hash stable while binding every ordered
	// usage into media-bearing retries.
	if len(media) > 0 {
		_, _ = digest.Write([]byte("\x00media-v1\x00"))
		writeHashLength(digest, len(media))
		for _, usage := range media {
			writeHashLength(digest, usage.Position)
			writeHashLength(digest, len(usage.MediaID.String()))
			_, _ = digest.Write([]byte(usage.MediaID.String()))
			switch {
			case usage.AltText == nil:
				_, _ = digest.Write([]byte{0})
			case *usage.AltText == "":
				_, _ = digest.Write([]byte{1})
			default:
				_, _ = digest.Write([]byte{2})
				writeHashLength(digest, len(*usage.AltText))
				_, _ = digest.Write([]byte(*usage.AltText))
			}
		}
	}
	if location != nil {
		_, _ = digest.Write([]byte("\x00location-v1\x00"))
		for _, value := range []string{
			location.Name, location.DisplayName, location.CountryCode, location.Country,
			location.Region, location.City, location.District, location.Category,
			location.Provider, location.ProviderPlaceID,
		} {
			writeHashLength(digest, len(value))
			_, _ = digest.Write([]byte(value))
		}
		if location.Point == nil {
			_, _ = digest.Write([]byte{0})
		} else {
			_, _ = digest.Write([]byte{1})
			writeHashInt64(digest, location.Point.LatitudeE7)
			writeHashInt64(digest, location.Point.LongitudeE7)
			_, _ = digest.Write([]byte(location.Point.System))
		}
	}
	// Keep the historical hash stable for card-free retries while binding a
	// card-bearing retry to the exact snapshot it published.
	if len(cards) > 0 {
		_, _ = digest.Write([]byte("\x00link-card-v1\x00"))
		writeHashLength(digest, len(cards))
		for _, card := range cards {
			writeHashLength(digest, card.Position)
			for _, value := range []string{
				card.SourceURL, card.CanonicalURL, string(card.Provider), string(card.Kind),
				card.Title, card.Description, card.SiteName, card.AuthorName,
				card.ThumbnailMediaID, card.EmbedURL,
			} {
				writeHashLength(digest, len(value))
				_, _ = digest.Write([]byte(value))
			}
			if card.DurationSeconds == nil {
				_, _ = digest.Write([]byte{0})
			} else {
				_, _ = digest.Write([]byte{1})
				writeHashInt64(digest, int64(*card.DurationSeconds))
			}
		}
	}
	if quotedPostID != nil {
		_, _ = digest.Write([]byte("\x00quote-v1\x00" + quotedPostID.String()))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func validatePostAggregate(content string, mediaCount int) error {
	return domain.ValidatePostMarkdown(content, mediaCount)
}

func writeHashInt64(writer interface{ Write([]byte) (int, error) }, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = writer.Write(encoded[:])
}

func writeHashLength(writer interface{ Write([]byte) (int, error) }, value int) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(value))
	_, _ = writer.Write(length[:])
}

func validRequestHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func loadEntryQuote(ctx context.Context, store PostStore, entryID domain.ID) (*QuotedPost, error) {
	reference, err := store.EntryQuote(ctx, entryID)
	if err != nil || !reference.Present {
		return nil, err
	}
	if reference.TargetID == nil {
		return &QuotedPost{Unavailable: true}, nil
	}
	quoted, err := loadQuotedPost(ctx, store, *reference.TargetID)
	if err != nil {
		return nil, err
	}
	if quoted == nil {
		return &QuotedPost{Unavailable: true}, nil
	}
	return quoted, nil
}

func loadQuotedPost(ctx context.Context, store PostStore, id domain.ID) (*QuotedPost, error) {
	entry, found, err := store.EntryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	media, err := store.EntryMediaUsages(ctx, id)
	if err != nil {
		return nil, err
	}
	location, err := store.EntryLocation(ctx, id)
	if err != nil {
		return nil, err
	}
	html, err := domain.RenderMarkdown(entry.ContentMD)
	if err != nil {
		return nil, storedEntryError(id)
	}
	return &QuotedPost{Entry: entry, ContentHTML: html, Media: media, Location: location}, nil
}

func renderPost(entry domain.Entry, media []MediaUsageProjection, location *domain.Location, cards []domain.LinkCard, commentCount int) (Post, error) {
	if err := validatePostMediaProjections(media); err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	if err := domain.ValidatePostMarkdown(entry.ContentMD, len(media)); err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	html, err := domain.RenderMarkdown(entry.ContentMD)
	if err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	if commentCount < 0 {
		return Post{}, storedEntryError(entry.ID)
	}
	if location != nil {
		if err := location.Validate(); err != nil {
			return Post{}, storedEntryError(entry.ID)
		}
	}
	if err := domain.ValidateLinkCards(cards); err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	return Post{
		Entry:        entry,
		ContentHTML:  html,
		Media:        cloneMediaUsageProjections(media),
		Location:     domain.CloneLocation(location),
		LinkCards:    domain.CloneLinkCards(cards),
		CommentCount: commentCount,
	}, nil
}

// linkCardMediaIDs collects the managed thumbnails a card set references so
// reference reconciliation covers this source like any other.
func linkCardMediaIDs(cards []domain.LinkCard) []domain.ID {
	ids := make([]domain.ID, 0, len(cards))
	for _, card := range cards {
		if card.ThumbnailMediaID == "" {
			continue
		}
		if id, err := domain.ParseID(card.ThumbnailMediaID); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func mediaUsageIDs(usages []domain.EntryMediaUsage) []domain.ID {
	ids := make([]domain.ID, 0, len(usages))
	for _, usage := range usages {
		ids = append(ids, usage.MediaID)
	}
	return ids
}

func mediaProjectionIDs(usages []MediaUsageProjection) []domain.ID {
	ids := make([]domain.ID, 0, len(usages))
	for _, usage := range usages {
		ids = append(ids, usage.MediaID)
	}
	return ids
}

func validatePostID(id domain.ID) error {
	_, err := domain.ParseID(id.String())
	return err
}

func postNotFound(id domain.ID) error {
	return domain.NewError(domain.CodePostNotFound, fmt.Sprintf("post %s does not exist", id), map[string]any{"post_id": id.String()})
}

func storedEntryError(id domain.ID) error {
	return domain.NewError(domain.CodeDataIntegrityFailed, "stored post violates the content contract", map[string]any{"post_id": id.String()})
}

func idempotencySnapshotError() error {
	return domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the post contract", nil)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return domain.NewError(domain.CodeInternal, "context is required", nil)
	}
	return ctx.Err()
}

func normalizePostRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return domain.NewError(domain.CodeInternal, "post storage operation failed", nil)
}
