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
	"reflect"
	"slices"
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
	EntryLinkCards(context.Context, domain.ID) ([]LinkCardProjection, error)
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
	// Publishing an article writes an entry and a body together, so the two
	// belong to one store rather than to two that could drift apart mid-write.
	InsertArticle(context.Context, domain.ID, domain.Article) error
	ReplaceArticle(context.Context, domain.ID, domain.Article) error
	UpdateArticleEntry(context.Context, domain.Entry, string) (bool, error)
	ArticleByEntryID(context.Context, domain.ID) (domain.Article, bool, error)
	ArticleCards(context.Context, []domain.ID) (map[domain.ID]ArticleCard, error)
	ArticleDerivations(context.Context) ([]ArticleDerivation, error)
	// Drafts live outside entries on purpose: eighteen read paths select from
	// entries, and a status column there would need every one of them to
	// filter correctly for an unfinished article to stay unpublished.
	InsertArticleDraft(context.Context, ArticleDraftRecord) error
	UpdateArticleDraft(context.Context, ArticleDraftRecord, time.Time) (bool, error)
	ArticleDraftByID(context.Context, domain.ID) (ArticleDraftRecord, bool, error)
	ArticleDraftForEntry(context.Context, domain.ID) (ArticleDraftRecord, bool, error)
	ListArticleDrafts(context.Context, int) ([]ArticleDraftRecord, error)
	CountArticleDrafts(context.Context) (int, error)
	DeleteArticleDraft(context.Context, domain.ID) (bool, error)
}

// PostTransactions supplies consistent read and atomic write transactions to
// PostService without exposing SQL or connection ownership.
type PostTransactions interface {
	Read(context.Context, func(PostStore) error) error
	Write(context.Context, func(PostStore) error) error
}

// IdempotencyRecord is the persisted retry decision for a create request.
type IdempotencyRecord struct {
	AuthorUserID       string
	Scope              string
	Key                string
	RequestHash        string
	ResponseResourceID domain.ID
	CreatedAt          time.Time
}

// Post is an Entry together with its safe derived representation. Entry stays
// the canonical domain authority; ContentHTML is always rebuilt from ContentMD.
type Post struct {
	Author       *CommentAuthor
	Entry        domain.Entry
	TimelineKind TimelineItemKind
	ContentHTML  string
	// Article is the body of a KindArticle entry, present on a detail read and
	// nil for a moment. The Entry's ContentMD is nil in exactly the same cases.
	Article *domain.Article
	// ArticleCard is what a list read carries instead: enough to draw the card
	// without the body. A detail read fills both.
	ArticleCard  *ArticleCard
	Media        []MediaUsageProjection
	Location     *domain.Location
	LinkCards    []LinkCardProjection
	CommentCount int
	Quoted       *QuotedPost
}

// LinkCardProjection pairs a stored card with its thumbnail's authoritative
// geometry. Dimensions cross this boundary so the browser can reserve layout
// space before the image loads, the same reason media projections carry width
// and height while media writes do not.
type LinkCardProjection struct {
	Card              domain.LinkCard
	ThumbnailMIMEType string
	ThumbnailWidth    uint32
	ThumbnailHeight   uint32
	FaviconMIMEType   string
}

// LinkCardsOf extracts the stored cards from their projections.
func LinkCardsOf(projections []LinkCardProjection) []domain.LinkCard {
	if projections == nil {
		return nil
	}
	cards := make([]domain.LinkCard, 0, len(projections))
	for _, projection := range projections {
		cards = append(cards, projection.Card)
	}
	return cards
}

func cloneLinkCardProjections(projections []LinkCardProjection) []LinkCardProjection {
	if projections == nil {
		return nil
	}
	clones := make([]LinkCardProjection, len(projections))
	copy(clones, projections)
	cards := domain.CloneLinkCards(LinkCardsOf(projections))
	for index := range clones {
		clones[index].Card = cards[index]
	}
	return clones
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
	// ArticleCard stands in for the body when the target is an article. A
	// quote shows a card, not a whole article; embedding one would put a
	// complete long post inside somebody else's short one.
	ArticleCard *ArticleCard
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
	ContentMD string
	// Visibility empty means the default, everyone.
	Visibility     domain.Visibility
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
	// Visibility nil preserves the current audience. Changing it alone is a
	// complete update: nothing else about the post has to move.
	Visibility *domain.Visibility
}

// ListPostsInput requests one reverse-chronological timeline page. A zero
// limit selects DefaultPostPageSize.
type ListPostsInput struct {
	AuthorUserID  string
	OriginalsOnly bool
	Cursor        string
	Limit         int
	FromDate      string
	ToDate        string
	// Anchor is a site-calendar day the page reads back from: what appeared
	// on or before it, newest first, with PrevCursor continuing into what
	// appeared after it. It is a position on the timeline's own axis -- the
	// instant a row appeared, a repost's included -- not a publication window,
	// and cannot be combined with one or with a Cursor.
	Anchor   string
	Timezone string
	// Kind narrows the page to one kind of post. Empty means both, because a
	// timeline with a gap in it is not a timeline.
	Kind domain.EntryKind
	// Viewer decides which visibilities the page may contain. The zero value
	// is a visitor.
	Viewer Viewer
}

// TimelineQuery is the repository-facing keyset and UTC half-open date range.
// Adapters pass calendar strings to PostService and never calculate day
// boundaries themselves.
type TimelineQuery struct {
	AuthorUserID  string
	OriginalsOnly bool
	Position      *TimelinePosition
	// Direction is which way to read from Position; the zero value reads
	// older. A newer read returns rows ascending, oldest first.
	Direction TimelineDirection
	FromMS    *int64
	ToMS      *int64
	// Kind narrows the read to one kind of post. Empty reads both, because a
	// timeline with a gap in it is not a timeline.
	Kind   domain.EntryKind
	Viewer Viewer
}

// PostPage is one stable keyset page, newest first whichever way it was read.
// NextCursor continues older from its last item and PrevCursor newer from its
// first; each is empty once the read that produced the page proved that
// direction exhausted, and a page proves only the direction it read.
type PostPage struct {
	Items      []Post
	NextCursor string
	PrevCursor string
	// RefreshCursor remains usable after newer paging is exhausted.
	RefreshCursor string
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
	visibility, err := createVisibility(input.Visibility)
	if err != nil {
		return CreatePostResult{}, err
	}
	requestHash := hashCreatePost(input.ContentMD, input.Media, input.Location, input.LinkCards, input.QuotedPostID, visibility)
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
	contentText, err := DerivedSearchText(input.ContentMD, input.LinkCards)
	if err != nil {
		return CreatePostResult{}, err
	}

	result := CreatePostResult{}

	err = service.transactions.Write(ctx, func(store PostStore) error {
		if input.QuotedPostID != nil {
			// The owner is the only writer, so the quoted target is theirs to
			// see whatever its visibility; readers meet the quote through
			// loadQuotedPost, which asks on their behalf.
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
				if err := attachContentAuthor(ctx, store, &post); err != nil {
					return err
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
		entry.Visibility = visibility
		entry.AuthorUserID, createErr = contentActorID(ctx, store)
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
				AuthorUserID:       entry.AuthorUserID,
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
		result.Post.LinkCards, err = store.EntryLinkCards(ctx, entry.ID)
		if err != nil {
			return err
		}
		if input.QuotedPostID != nil {
			quoted, quoteErr := loadQuotedPost(ctx, store, OwnerViewer(), *input.QuotedPostID)
			if quoteErr != nil {
				return quoteErr
			}
			result.Post.Quoted = quoted
		}
		return attachContentAuthor(ctx, store, &result.Post)
	})
	if err != nil {
		return CreatePostResult{}, normalizePostRepositoryError(err)
	}
	result.Post.ContentHTML, err = renderEntryHTML(result.Post.Entry, result.Post.Article, result.Post.Media, LinkCardsOf(result.Post.LinkCards))
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
			post.Quoted, storeErr = loadQuotedPost(ctx, store, OwnerViewer(), *input.QuotedPostID)
		}
		if storeErr != nil {
			return storeErr
		}
		return attachContentAuthor(ctx, store, &post)
	})
	if err != nil {
		return CreatePostResult{}, false, normalizePostRepositoryError(err)
	}
	if !found {
		return CreatePostResult{}, false, nil
	}
	quoted, author := post.Quoted, post.Author
	post, err = renderPost(post.Entry, articleView{}, media, post.Location, post.LinkCards, commentCount)
	if err != nil {
		return CreatePostResult{}, false, idempotencySnapshotError()
	}
	post.Quoted, post.Author = quoted, author
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
	visibility, err := createVisibility(input.Visibility)
	if err != nil {
		return Post{}, idempotencySnapshotError()
	}
	entry.Visibility = visibility
	entry.AuthorUserID = record.AuthorUserID
	if err := entry.Validate(); err != nil {
		return Post{}, idempotencySnapshotError()
	}
	return Post{Entry: entry, Location: domain.CloneLocation(input.Location)}, nil
}

// Get returns one post the viewer may read, or a stable not-found error. A
// post the viewer may not read is reported exactly like one that does not
// exist.
func (service *PostService) Get(ctx context.Context, id domain.ID, viewer Viewer) (Post, error) {
	if err := contextError(ctx); err != nil {
		return Post{}, err
	}
	if err := validatePostID(id); err != nil {
		return Post{}, err
	}
	var entry domain.Entry
	var media []MediaUsageProjection
	var location *domain.Location
	var linkCards []LinkCardProjection
	var commentCount int
	var quoted *QuotedPost
	var found bool
	var view articleView
	var authors map[string]CommentAuthorIdentity
	err := service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		viewer, storeErr = resolvedViewer(ctx, store, viewer)
		if storeErr != nil {
			return storeErr
		}
		entry, found, storeErr = store.EntryByID(ctx, id)
		if storeErr != nil || !found {
			return storeErr
		}
		authors, storeErr = readContentAuthors(ctx, store, []domain.Entry{entry})
		if storeErr != nil {
			return storeErr
		}
		if !viewer.CanReadEntry(entry) {
			found = false
			return nil
		}
		if view, storeErr = loadArticleView(ctx, store, entry); storeErr != nil {
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
		quoted, storeErr = loadEntryQuote(ctx, store, viewer, id)
		return storeErr
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	if !found {
		return Post{}, postHiddenFrom(id)
	}
	post, err := renderPost(entry, view, media, location, linkCards, commentCount)
	post.Quoted = quoted
	post.Author = contentAuthor(authors, entry.AuthorUserID)
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
	var view articleView
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(id)
		}
		if entry.Kind == domain.KindArticle {
			if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
				return err
			}
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
		quoted, quoteErr := loadEntryQuote(ctx, store, OwnerViewer(), id)
		if quoteErr != nil {
			return quoteErr
		}
		if view, storeErr = loadArticleView(ctx, store, entry); storeErr != nil {
			return storeErr
		}
		result = Post{Entry: entry, Media: media, Location: location, LinkCards: linkCards, CommentCount: counts[id], Quoted: quoted}
		return attachContentAuthor(ctx, store, &result)
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	quoted, author := result.Quoted, result.Author
	result, err = renderPost(result.Entry, view, result.Media, result.Location, result.LinkCards, result.CommentCount)
	result.Quoted, result.Author = quoted, author
	return result, err
}

// List returns one deterministic keyset page, newest first: the head of the
// timeline, the continuation a cursor names in the direction it names, or the
// end of an anchored day read back from.
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
	position, direction, err := decodeTimelineCursor(input.Cursor, fromMS, toMS)
	if err != nil {
		return PostPage{}, err
	}
	if input.Anchor != "" {
		if input.Cursor != "" || fromMS != nil || toMS != nil {
			return PostPage{}, domain.NewError(domain.CodeInvalidArgument, "anchor cannot be combined with cursor, from, or to", map[string]any{"field": "anchor"})
		}
		anchorEndMS, anchorErr := postAnchorEnd(input.Anchor, input.Timezone)
		if anchorErr != nil {
			return PostPage{}, anchorErr
		}
		position = &TimelinePosition{TimelineAtMS: anchorEndMS, Boundary: true}
		direction = TimelineOlder
	}

	// Capture before establishing the read snapshot, including an empty head.
	var headSnapshotMS int64
	if position == nil {
		headSnapshotMS = domain.TimeToMillis(service.clock())
	}
	var entries []TimelineEntry
	mediaByEntry := make(map[domain.ID][]MediaUsageProjection)
	locationByEntry := make(map[domain.ID]*domain.Location)
	linkCardsByEntry := make(map[domain.ID][]LinkCardProjection)
	commentCounts := make(map[domain.ID]int)
	articleCards := make(map[domain.ID]ArticleCard)
	authors := make(map[string]CommentAuthorIdentity)
	quotedByEntry := make(map[domain.ID]*QuotedPost)
	err = service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		entries, storeErr = store.ListEntries(ctx, TimelineQuery{AuthorUserID: input.AuthorUserID, OriginalsOnly: input.OriginalsOnly, Position: position, Direction: direction, FromMS: fromMS, ToMS: toMS, Kind: input.Kind, Viewer: input.Viewer}, limit+1)
		if storeErr != nil {
			return storeErr
		}
		authorEntries := make([]domain.Entry, 0, len(entries))
		for _, item := range entries {
			authorEntries = append(authorEntries, item.Entry)
		}
		authors, storeErr = readContentAuthors(ctx, store, authorEntries)
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
			quotedByEntry[entry.ID], storeErr = loadEntryQuote(ctx, store, input.Viewer, entry.ID)
			if storeErr != nil {
				return storeErr
			}
		}
		// A list carries article cards, never bodies: twenty full bodies on one
		// page would be megabytes nobody on that page reads.
		if articleCards, storeErr = store.ArticleCards(ctx, ids); storeErr != nil {
			return storeErr
		}
		commentCounts, storeErr = store.CommentCounts(ctx, ids)
		return storeErr
	})
	if err != nil {
		return PostPage{}, normalizePostRepositoryError(err)
	}

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	if direction == TimelineNewer {
		// Read ascending so the limit takes the rows nearest the position;
		// served newest first like every other page.
		slices.Reverse(entries)
	}
	page := PostPage{Items: make([]Post, 0, len(entries))}
	for _, item := range entries {
		entry := item.Entry
		post, renderErr := renderPost(entry, articleView{Card: articleCardFor(articleCards, entry)}, mediaByEntry[entry.ID], locationByEntry[entry.ID], linkCardsByEntry[entry.ID], commentCounts[entry.ID])
		if renderErr != nil {
			return PostPage{}, renderErr
		}
		post.Quoted = quotedByEntry[entry.ID]
		post.TimelineKind = item.Kind
		post.Author = contentAuthor(authors, entry.AuthorUserID)
		page.Items = append(page.Items, post)
	}
	cursorFor := func(item TimelineEntry, toward TimelineDirection) string {
		return encodeTimelineCursor(TimelinePosition{
			TimelineAtMS: domain.TimeToMillis(item.TimelineAt()),
			Kind:         item.Kind,
			ID:           item.Entry.ID,
		}, toward, fromMS, toMS)
	}
	switch {
	case len(entries) > 0 && direction == TimelineOlder:
		if hasMore {
			page.NextCursor = cursorFor(entries[len(entries)-1], TimelineOlder)
		}
		page.PrevCursor = cursorFor(entries[0], TimelineNewer)
	case len(entries) > 0:
		page.NextCursor = cursorFor(entries[len(entries)-1], TimelineOlder)
		if hasMore {
			page.PrevCursor = cursorFor(entries[0], TimelineNewer)
		}
	case position != nil && position.Boundary && direction == TimelineOlder:
		// Nothing on or before the anchored day. What appeared after it is
		// still there, and the boundary is the only row-free way to name it.
		page.PrevCursor = encodeTimelineCursor(*position, TimelineNewer, fromMS, toMS)
	}
	// Refresh from the entire newest millisecond, rather than one random ULID:
	// a publication committed later in that millisecond may sort before it.
	if len(entries) > 0 {
		page.RefreshCursor = encodeTimelineCursor(TimelinePosition{
			TimelineAtMS: domain.TimeToMillis(entries[0].TimelineAt()), Boundary: true,
		}, TimelineNewer, fromMS, toMS)
	} else if direction == TimelineNewer {
		page.RefreshCursor = input.Cursor
		if position != nil && !position.Boundary {
			// An exhausted continuation may name a row that has disappeared.
			// Keep its whole millisecond refreshable, including later commits.
			page.RefreshCursor = encodeTimelineCursor(TimelinePosition{
				TimelineAtMS: position.TimelineAtMS, Boundary: true,
			}, TimelineNewer, fromMS, toMS)
		}
	} else if position == nil {
		page.RefreshCursor = encodeTimelineCursor(TimelinePosition{
			TimelineAtMS: headSnapshotMS, Boundary: true,
		}, TimelineNewer, fromMS, toMS)
	}
	return page, nil
}

// postAnchorEnd is the first instant after a site-calendar day, in
// milliseconds: the boundary an anchored page reads back from.
func postAnchorEnd(anchor, timezone string) (int64, error) {
	location, err := postLocation(timezone)
	if err != nil {
		return 0, err
	}
	day, err := time.ParseInLocation("2006-01-02", anchor, location)
	if err != nil || day.Format("2006-01-02") != anchor {
		return 0, domain.NewError(domain.CodeInvalidArgument, "anchor must use YYYY-MM-DD", map[string]any{"field": "anchor"})
	}
	return domain.TimeToMillis(day.AddDate(0, 0, 1)), nil
}

func postLocation(timezone string) (*time.Location, error) {
	if err := validateTimezone(timezone); err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is invalid", map[string]any{"field": "timezone"})
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is unavailable", map[string]any{"field": "timezone"})
	}
	return location, nil
}

func postDateRange(fromDate, toDate, timezone string) (*int64, *int64, error) {
	if fromDate == "" && toDate == "" {
		return nil, nil, nil
	}
	location, err := postLocation(timezone)
	if err != nil {
		return nil, nil, err
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
	if input.PreserveContent && input.Media == nil && input.Location == nil && input.LinkCards == nil && input.Visibility == nil {
		return Post{}, domain.NewError(domain.CodeInvalidArgument, "post update must change content_md, media, location, link cards, or visibility", map[string]any{"field": "body"})
	}
	if input.Visibility != nil && !input.Visibility.Valid() {
		return Post{}, domain.NewError(domain.CodeInvalidArgument, "visibility must be everyone or only_me", map[string]any{"field": "visibility"})
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
	var resultAuthor *CommentAuthor
	var updated domain.Entry
	var updatedMedia []MediaUsageProjection
	var updatedLocation *domain.Location
	var updatedLinkCards []LinkCardProjection
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
		if entry.Kind == domain.KindArticle {
			if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
				return err
			}
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
		// This path replaces a Markdown body. An article is edited through
		// its own path, which rewrites the whole aggregate at once.
		if entry.Kind != domain.KindMoment {
			return domain.NewError(domain.CodeInvalidArgument,
				"an article is not edited through the post body", map[string]any{"field": "kind"})
		}
		content := input.ContentMD
		if input.PreserveContent {
			previous, present := entry.Markdown()
			if !present {
				return storedEntryError(entry.ID)
			}
			content = previous
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
		// The replacement set is authoritative for validation and for the
		// derived search text. Projections are reloaded after the write so the
		// response carries each thumbnail's stored geometry.
		nextLinkCards := LinkCardsOf(currentLinkCards)
		if input.LinkCards != nil {
			if mediaErr := domain.ValidateLinkCards(*input.LinkCards); mediaErr != nil {
				return mediaErr
			}
			nextLinkCards = *input.LinkCards
		}
		if mediaErr := validatePostAggregate(content, len(updatedMedia)); mediaErr != nil {
			return mediaErr
		}
		if _, mediaErr := domain.RenderMarkdown(content); mediaErr != nil {
			return mediaErr
		}
		contentText, mediaErr := DerivedSearchText(content, nextLinkCards)
		if mediaErr != nil {
			return mediaErr
		}
		now := domain.NormalizeTime(service.clock())
		if !now.After(entry.UpdatedAt) {
			now = entry.UpdatedAt.Add(time.Millisecond)
		}
		mediaChanged := (len(currentMedia) != 0 || len(updatedMedia) != 0) && !reflect.DeepEqual(currentMedia, updatedMedia)
		cardsChanged := (len(currentLinkCards) != 0 || len(nextLinkCards) != 0) && !reflect.DeepEqual(LinkCardsOf(currentLinkCards), nextLinkCards)
		previousContent, _ := entry.Markdown()
		contentChanged := content != previousContent || mediaChanged || cardsChanged || !reflect.DeepEqual(currentLocation, updatedLocation)
		if updateErr := entry.UpdateContent(content, now); updateErr != nil {
			return updateErr
		}
		if contentChanged {
			edited := now
			entry.EditedAt = &edited
		}
		if input.Visibility != nil {
			if updateErr := entry.SetVisibility(*input.Visibility, now); updateErr != nil {
				return updateErr
			}
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
			affectedCards := append(linkCardMediaIDs(LinkCardsOf(currentLinkCards)), linkCardMediaIDs(*input.LinkCards)...)
			if updateErr := store.ReconcileMediaReferences(ctx, affectedCards, now); updateErr != nil {
				return updateErr
			}
			updatedLinkCards, mediaErr = store.EntryLinkCards(ctx, input.ID)
			if mediaErr != nil {
				return mediaErr
			}
		}
		authors, authorErr := readContentAuthors(ctx, store, []domain.Entry{entry})
		if authorErr != nil {
			return authorErr
		}
		resultAuthor = contentAuthor(authors, entry.AuthorUserID)
		updated = entry
		if quoteReference.Present {
			updatedQuoted, mediaErr = loadEntryQuote(ctx, store, OwnerViewer(), input.ID)
			if mediaErr != nil {
				return mediaErr
			}
		}
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	result, err := renderPost(updated, articleView{}, updatedMedia, updatedLocation, updatedLinkCards, commentCount)
	result.Quoted = updatedQuoted
	result.Author = resultAuthor
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
	var view articleView
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(id)
		}
		if entry.Kind == domain.KindArticle {
			if err := requireContentAuthor(ctx, store, entry.AuthorUserID); err != nil {
				return err
			}
		}
		if view, storeErr = loadArticleView(ctx, store, entry); storeErr != nil {
			return storeErr
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
		quoted, mediaErr := loadEntryQuote(ctx, store, OwnerViewer(), id)
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
		affectedMedia = append(affectedMedia, linkCardMediaIDs(LinkCardsOf(linkCards))...)
		if entry.Kind == domain.KindArticle {
			workingCopy, found, err := store.ArticleDraftForEntry(ctx, id)
			if err != nil {
				return err
			}
			if found {
				affectedMedia = append(affectedMedia, workingCopy.Draft.CoverAndInlineMedia()...)
				if _, err := store.DeleteArticleDraft(ctx, workingCopy.ID); err != nil {
					return err
				}
			}
		}
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
		deleted = Post{Entry: entry, Media: cloneMediaUsageProjections(mediaUsages), Location: domain.CloneLocation(location), LinkCards: cloneLinkCardProjections(linkCards), CommentCount: counts[id], Quoted: quoted}
		return attachContentAuthor(ctx, store, &deleted)
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	quoted, author := deleted.Quoted, deleted.Author
	deleted, err = renderPost(deleted.Entry, view, deleted.Media, deleted.Location, deleted.LinkCards, deleted.CommentCount)
	deleted.Quoted, deleted.Author = quoted, author
	return deleted, err
}

func hashCreatePost(content string, media []domain.EntryMediaUsage, location *domain.Location, cards []domain.LinkCard, quotedPostID *domain.ID, visibility domain.Visibility) string {
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
				card.ThumbnailMediaID, card.FaviconMediaID, card.EmbedURL,
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
			for _, count := range []*int64{card.ViewCount, card.CommentCount, card.LikeCount} {
				if count == nil {
					_, _ = digest.Write([]byte{0})
					continue
				}
				_, _ = digest.Write([]byte{1})
				writeHashInt64(digest, *count)
			}
		}
	}
	if quotedPostID != nil {
		_, _ = digest.Write([]byte("\x00quote-v1\x00" + quotedPostID.String()))
	}
	// Only a non-default audience joins the hash, so every retry key minted
	// before visibility existed still replays.
	if visibility != domain.VisibilityEveryone {
		_, _ = digest.Write([]byte("\x00visibility-v1\x00" + string(visibility)))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// createVisibility resolves the create input's audience: absent means the
// default, anything else has to be a known value.
func createVisibility(requested domain.Visibility) (domain.Visibility, error) {
	if requested == "" {
		return domain.VisibilityEveryone, nil
	}
	return domain.ParseVisibility(string(requested))
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

func loadEntryQuote(ctx context.Context, store PostStore, viewer Viewer, entryID domain.ID) (*QuotedPost, error) {
	reference, err := store.EntryQuote(ctx, entryID)
	if err != nil || !reference.Present {
		return nil, err
	}
	if reference.TargetID == nil {
		return &QuotedPost{Unavailable: true}, nil
	}
	quoted, err := loadQuotedPost(ctx, store, viewer, *reference.TargetID)
	if err != nil {
		return nil, err
	}
	if quoted == nil {
		return &QuotedPost{Unavailable: true}, nil
	}
	return quoted, nil
}

// loadQuotedPost embeds the quoted target when the viewer may read it. A
// target the viewer may not see comes back nil and renders as the same
// tombstone a deleted target does: the quote stays, its words do not travel.
func loadQuotedPost(ctx context.Context, store PostStore, viewer Viewer, id domain.ID) (*QuotedPost, error) {
	entry, found, err := store.EntryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	viewer, err = resolvedViewer(ctx, store, viewer)
	if err != nil {
		return nil, err
	}
	if !found || !viewer.CanReadEntry(entry) {
		return nil, nil
	}
	location, err := store.EntryLocation(ctx, id)
	if err != nil {
		return nil, err
	}
	// A quoted article shows its card. Rendering the body here would embed a
	// whole long post inside a short one, and the pictures it places are not
	// attachments of the quoting post either.
	if entry.Kind == domain.KindArticle {
		cards, cardErr := store.ArticleCards(ctx, []domain.ID{id})
		if cardErr != nil {
			return nil, cardErr
		}
		card, present := cards[id]
		if !present {
			return nil, storedEntryError(id)
		}
		return &QuotedPost{Entry: entry, ArticleCard: &card, Location: location}, nil
	}
	media, err := store.EntryMediaUsages(ctx, id)
	if err != nil {
		return nil, err
	}
	html, err := renderEntryHTML(entry, nil, nil, nil)
	if err != nil {
		return nil, storedEntryError(id)
	}
	return &QuotedPost{Entry: entry, ContentHTML: html, Media: media, Location: location}, nil
}

func renderPost(entry domain.Entry, view articleView, media []MediaUsageProjection, location *domain.Location, cards []LinkCardProjection, commentCount int) (Post, error) {
	if err := validatePostMediaProjections(media); err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	// A list read carries only the card, so there is no body to check or
	// render; a detail read carries the body and both happen.
	if entry.Kind == domain.KindArticle && view.Body == nil {
		if view.Card == nil {
			return Post{}, storedEntryError(entry.ID)
		}
		return assemblePost(entry, view, "", media, location, cards, commentCount)
	}
	if err := validateStoredEntryBody(entry, view.Body, len(media)); err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	html, err := renderEntryHTML(entry, view.Body, media, LinkCardsOf(cards))
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
	if err := domain.ValidateLinkCards(LinkCardsOf(cards)); err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	return assemblePost(entry, view, html, media, location, cards, commentCount)
}

// articleCardFor answers the card of an article row and nothing for a moment.
func articleCardFor(cards map[domain.ID]ArticleCard, entry domain.Entry) *ArticleCard {
	if entry.Kind != domain.KindArticle {
		return nil
	}
	card, found := cards[entry.ID]
	if !found {
		return nil
	}
	return &card
}

// loadArticleView reads everything a detail read needs of an article and
// nothing for a moment. The card comes along because only it carries the
// cover's geometry, which the browser needs to reserve space for the picture.
// An article row with no body is a read that lost half of itself.
func loadArticleView(ctx context.Context, store PostStore, entry domain.Entry) (articleView, error) {
	if entry.Kind != domain.KindArticle {
		return articleView{}, nil
	}
	article, found, err := store.ArticleByEntryID(ctx, entry.ID)
	if err != nil {
		return articleView{}, err
	}
	if !found {
		return articleView{}, storedEntryError(entry.ID)
	}
	cards, err := store.ArticleCards(ctx, []domain.ID{entry.ID})
	if err != nil {
		return articleView{}, err
	}
	view := articleView{Body: &article}
	if card, present := cards[entry.ID]; present {
		view.Card = &card
	}
	return view, nil
}

// articleView is how much of an article one read carries: the whole body on a
// detail read, only the card on a list. A moment has neither.
type articleView struct {
	Body *domain.Article
	Card *ArticleCard
}

func assemblePost(entry domain.Entry, view articleView, html string, media []MediaUsageProjection, location *domain.Location, cards []LinkCardProjection, commentCount int) (Post, error) {
	if commentCount < 0 {
		return Post{}, storedEntryError(entry.ID)
	}
	post := Post{
		Entry:        entry,
		ContentHTML:  html,
		Article:      view.Body,
		ArticleCard:  view.Card,
		Media:        cloneMediaUsageProjections(media),
		Location:     domain.CloneLocation(location),
		LinkCards:    cloneLinkCardProjections(cards),
		CommentCount: commentCount,
	}
	// A detail read has the body, so its card costs nothing to derive and
	// every article answers with one whichever way it was read.
	if post.ArticleCard == nil && view.Body != nil {
		post.ArticleCard = &ArticleCard{Title: view.Body.Title, Excerpt: view.Body.Excerpt()}
	}
	return post, nil
}

// linkCardMediaIDs collects the managed thumbnails a card set references so
// reference reconciliation covers this source like any other.
func linkCardMediaIDs(cards []domain.LinkCard) []domain.ID {
	ids := make([]domain.ID, 0, len(cards)*2)
	for _, card := range cards {
		for _, reference := range []string{card.ThumbnailMediaID, card.FaviconMediaID} {
			if reference == "" {
				continue
			}
			if id, err := domain.ParseID(reference); err == nil {
				ids = append(ids, id)
			}
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
