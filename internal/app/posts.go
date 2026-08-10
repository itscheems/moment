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
	InsertEntry(context.Context, domain.Entry) error
	EntryByID(context.Context, domain.ID) (domain.Entry, bool, error)
	ListEntries(context.Context, *TimelinePosition, int) ([]domain.Entry, error)
	UpdateEntry(context.Context, domain.Entry) (bool, error)
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
	Entry       domain.Entry
	ContentHTML string
}

// CreatePostInput contains only application semantics. Authentication,
// headers, flags, and transport DTOs belong to adapters.
type CreatePostInput struct {
	ContentMD      string
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
	ID        domain.ID
	ContentMD string
}

// ListPostsInput requests one reverse-chronological timeline page. A zero
// limit selects DefaultPostPageSize.
type ListPostsInput struct {
	Cursor string
	Limit  int
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
	requestHash := hashCreatePost(input.ContentMD)
	if input.IdempotencyKey != "" {
		replayed, found, err := service.loadCreateReplay(ctx, input, requestHash)
		if err != nil {
			return CreatePostResult{}, err
		}
		if found {
			return replayed, nil
		}
	}
	if _, err := domain.RenderMarkdown(input.ContentMD); err != nil {
		return CreatePostResult{}, err
	}

	result := CreatePostResult{}

	err := service.transactions.Write(ctx, func(store PostStore) error {
		if input.IdempotencyKey != "" {
			record, found, storeErr := store.IdempotencyRecord(ctx, postCreateIdempotencyScope, input.IdempotencyKey)
			if storeErr != nil {
				return storeErr
			}
			if found {
				entry, replayErr := reconstructCreateReplay(input, requestHash, record)
				if replayErr != nil {
					return replayErr
				}
				result.Post.Entry = entry
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
		if insertErr := store.InsertEntry(ctx, entry); insertErr != nil {
			return insertErr
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
	err := service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		record, found, storeErr = store.IdempotencyRecord(ctx, postCreateIdempotencyScope, input.IdempotencyKey)
		return storeErr
	})
	if err != nil {
		return CreatePostResult{}, false, normalizePostRepositoryError(err)
	}
	if !found {
		return CreatePostResult{}, false, nil
	}
	entry, err := reconstructCreateReplay(input, requestHash, record)
	if err != nil {
		return CreatePostResult{}, false, err
	}
	post, err := renderPost(entry)
	if err != nil {
		return CreatePostResult{}, false, idempotencySnapshotError()
	}
	return CreatePostResult{Post: post, Replayed: true}, true, nil
}

func reconstructCreateReplay(input CreatePostInput, requestHash string, record IdempotencyRecord) (domain.Entry, error) {
	if record.Scope != postCreateIdempotencyScope || record.Key != input.IdempotencyKey || !validRequestHash(record.RequestHash) {
		return domain.Entry{}, idempotencySnapshotError()
	}
	if record.RequestHash != requestHash {
		return domain.Entry{}, domain.NewError(domain.CodeIdempotencyKeyReused, "idempotency key was already used with different content", map[string]any{"field": "idempotency_key"})
	}
	if _, err := domain.ParseID(record.ResponseResourceID.String()); err != nil ||
		domain.TimeToMillis(record.CreatedAt) <= 0 || record.CreatedAt != domain.NormalizeTime(record.CreatedAt) {
		return domain.Entry{}, idempotencySnapshotError()
	}
	entry, err := domain.NewEntry(record.ResponseResourceID, input.ContentMD, record.CreatedAt)
	if err != nil {
		return domain.Entry{}, idempotencySnapshotError()
	}
	if err := entry.Validate(); err != nil {
		return domain.Entry{}, idempotencySnapshotError()
	}
	return entry, nil
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
	var found bool
	err := service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		entry, found, storeErr = store.EntryByID(ctx, id)
		return storeErr
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	if !found {
		return Post{}, postNotFound(id)
	}
	return renderPost(entry)
}

// List returns one deterministic reverse-chronological keyset page.
func (service *PostService) List(ctx context.Context, input ListPostsInput) (PostPage, error) {
	if err := contextError(ctx); err != nil {
		return PostPage{}, err
	}
	position, err := decodeTimelineCursor(input.Cursor)
	if err != nil {
		return PostPage{}, err
	}
	limit, err := normalizePostLimit(input.Limit)
	if err != nil {
		return PostPage{}, err
	}

	var entries []domain.Entry
	err = service.transactions.Read(ctx, func(store PostStore) error {
		var storeErr error
		entries, storeErr = store.ListEntries(ctx, position, limit+1)
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
	for _, entry := range entries {
		post, renderErr := renderPost(entry)
		if renderErr != nil {
			return PostPage{}, renderErr
		}
		page.Items = append(page.Items, post)
	}
	if hasNext {
		last := entries[len(entries)-1]
		page.NextCursor = encodeTimelineCursor(TimelinePosition{
			PublishedAtMS: domain.TimeToMillis(last.PublishedAt),
			ID:            last.ID,
		})
	}
	return page, nil
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
	if _, err := domain.RenderMarkdown(input.ContentMD); err != nil {
		return Post{}, err
	}

	var updated domain.Entry
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, input.ID)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(input.ID)
		}
		now := domain.NormalizeTime(service.clock())
		if !now.After(entry.UpdatedAt) {
			now = entry.UpdatedAt.Add(time.Millisecond)
		}
		if updateErr := entry.UpdateContent(input.ContentMD, now); updateErr != nil {
			return updateErr
		}
		changed, updateErr := store.UpdateEntry(ctx, entry)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			return postNotFound(input.ID)
		}
		updated = entry
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	return renderPost(updated)
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
	var deleted domain.Entry
	err := service.transactions.Write(ctx, func(store PostStore) error {
		entry, found, storeErr := store.EntryByID(ctx, id)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return postNotFound(id)
		}
		changed, deleteErr := store.DeleteEntry(ctx, id)
		if deleteErr != nil {
			return deleteErr
		}
		if !changed {
			return postNotFound(id)
		}
		deleted = entry
		return nil
	})
	if err != nil {
		return Post{}, normalizePostRepositoryError(err)
	}
	return renderPost(deleted)
}

func hashCreatePost(content string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("moment-post-create-v1\x00"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(content)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(content))
	return hex.EncodeToString(digest.Sum(nil))
}

func validRequestHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func renderPost(entry domain.Entry) (Post, error) {
	html, err := domain.RenderMarkdown(entry.ContentMD)
	if err != nil {
		return Post{}, storedEntryError(entry.ID)
	}
	return Post{Entry: entry, ContentHTML: html}, nil
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
