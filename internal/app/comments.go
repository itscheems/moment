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

const (
	ownerCommentCreateIdempotencyScope = "comments.owner.create.v1"
	guestCommentCreateIdempotencyScope = "comments.guest.create.v1"
)

// CommentStore is the complete comment authority available within one
// repository transaction.
type CommentStore interface {
	MediaReferenceReconciler
	EntryExists(context.Context, domain.ID) (bool, error)
	CommentsEnabled(context.Context) (bool, error)
	CommentByID(context.Context, domain.ID) (domain.Comment, bool, error)
	ListComments(context.Context, domain.ID) ([]domain.Comment, error)
	CommentMediaUsages(context.Context, domain.ID) ([]MediaUsageProjection, error)
	ProjectMediaUsages(context.Context, []domain.EntryMediaUsage) ([]MediaUsageProjection, error)
	InsertComment(context.Context, domain.Comment) error
	ReplaceCommentLocation(context.Context, domain.ID, *domain.Location) error
	ReplaceCommentMediaUsages(context.Context, domain.ID, []domain.EntryMediaUsage) error
	CommentCascadeMediaIDs(context.Context, domain.ID) ([]domain.ID, error)
	CountCommentReplies(context.Context, domain.ID) (int, error)
	DeleteComment(context.Context, domain.ID) (bool, error)
	IdempotencyRecord(context.Context, string, string) (IdempotencyRecord, bool, error)
	InsertIdempotencyRecord(context.Context, IdempotencyRecord) error
}

// CommentTransactions supplies consistent reads and atomic comment writes.
type CommentTransactions interface {
	Read(context.Context, func(CommentStore) error) error
	Write(context.Context, func(CommentStore) error) error
}

// Comment includes the authoritative record and its safe derived HTML.
type Comment struct {
	Comment     domain.Comment
	ContentHTML string
	Media       []MediaUsageProjection
}

// CommentThread is a stable flattened one-level thread. Roots are ordered by
// creation time and ID; each root is immediately followed by similarly ordered
// direct replies.
type CommentThread struct {
	Items []Comment
}

// CreateCommentInput contains owner/guest application identity, never network
// identity. Adapters cannot use it to persist an IP address.
type CreateCommentInput struct {
	EntryID        domain.ID
	ParentID       *domain.ID
	AuthorKind     domain.CommentAuthorKind
	AuthorName     string
	ContentMD      string
	Media          []domain.EntryMediaUsage
	Location       *domain.Location
	IdempotencyKey string
}

// CreateCommentResult reports a safe idempotent retry.
type CreateCommentResult struct {
	Comment  Comment
	Replayed bool
}

// DeleteCommentResult describes the exact cascade surface for confirmation and
// moderation output.
type DeleteCommentResult struct {
	Comment         Comment
	RepliesAffected int
	Deleted         bool
	DryRun          bool
}

// CommentClock supplies deterministic creation instants.
type CommentClock func() time.Time

// CommentIDGenerator supplies deterministic comment identities.
type CommentIDGenerator func(time.Time) (domain.ID, error)

// CommentServiceOption customizes deterministic service dependencies.
type CommentServiceOption func(*CommentService) error

// WithCommentClock replaces the production clock.
func WithCommentClock(clock CommentClock) CommentServiceOption {
	return func(service *CommentService) error {
		if clock == nil {
			return errors.New("comment clock is required")
		}
		service.clock = clock
		return nil
	}
}

// WithCommentIDGenerator replaces production identity generation.
func WithCommentIDGenerator(generator CommentIDGenerator) CommentServiceOption {
	return func(service *CommentService) error {
		if generator == nil {
			return errors.New("comment ID generator is required")
		}
		service.ids = generator
		return nil
	}
}

// CommentService owns one-level threads, identity and moderation semantics.
type CommentService struct {
	transactions CommentTransactions
	clock        CommentClock
	ids          CommentIDGenerator
}

// NewCommentService constructs the shared comment application service.
func NewCommentService(transactions CommentTransactions, options ...CommentServiceOption) (*CommentService, error) {
	if transactions == nil {
		return nil, errors.New("comment transactions are required")
	}
	service := &CommentService{
		transactions: transactions,
		clock:        time.Now,
		ids: func(now time.Time) (domain.ID, error) {
			return domain.NewID(now, rand.Reader)
		},
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("comment service option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// CreateGuest persists a visitor comment without accepting network identity.
func (service *CommentService) CreateGuest(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	input.AuthorKind = domain.CommentAuthorGuest
	return service.create(ctx, input)
}

// CreateOwner persists an owner-marked comment. CLI and owner HTTP adapters
// use this method and cannot impersonate a guest.
func (service *CommentService) CreateOwner(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	input.AuthorKind = domain.CommentAuthorOwner
	input.AuthorName = "Owner"
	return service.create(ctx, input)
}

// ValidateCreateTarget confirms that a public reply upload is anchored to an
// existing post whose comment surface is currently enabled. It deliberately
// performs no mutation; the final comment transaction repeats these checks.
func (service *CommentService) ValidateCreateTarget(ctx context.Context, entryID domain.ID) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateCommentID(entryID); err != nil {
		return err
	}
	return service.transactions.Read(ctx, func(store CommentStore) error {
		exists, err := store.EntryExists(ctx, entryID)
		if err != nil {
			return err
		}
		if !exists {
			return postNotFound(entryID)
		}
		enabled, err := store.CommentsEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return domain.NewError(domain.CodeCommentsDisabled, "new comments are disabled", nil)
		}
		return nil
	})
}

func (service *CommentService) create(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	if err := contextError(ctx); err != nil {
		return CreateCommentResult{}, err
	}
	if err := validateCommentID(input.EntryID); err != nil {
		return CreateCommentResult{}, err
	}
	if input.ParentID != nil {
		if err := validateCommentID(*input.ParentID); err != nil {
			return CreateCommentResult{}, err
		}
	}
	name, err := domain.NormalizeCommentAuthorName(input.AuthorName)
	if err != nil {
		return CreateCommentResult{}, err
	}
	input.AuthorName = name
	input.Location = domain.CloneLocation(input.Location)
	if input.Location != nil {
		if err := input.Location.Validate(); err != nil {
			return CreateCommentResult{}, err
		}
	}
	requestHash := hashCreateComment(input)
	idempotencyScope := commentCreateIdempotencyScope(input.AuthorKind)
	if input.IdempotencyKey != "" {
		replayed, found, replayErr := service.loadCommentReplay(ctx, input, idempotencyScope, requestHash)
		if replayErr != nil {
			return CreateCommentResult{}, replayErr
		}
		if found {
			return replayed, nil
		}
	}
	if input.ContentMD != "" {
		if _, err := domain.RenderCommentMarkdown(input.ContentMD); err != nil {
			return CreateCommentResult{}, err
		}
	} else if input.Location == nil {
		return CreateCommentResult{}, domain.ValidateCommentMarkdown(input.ContentMD)
	}
	if err := domain.ValidateCommentMediaUsages(input.Media); err != nil {
		return CreateCommentResult{}, err
	}

	result := CreateCommentResult{}
	err = service.transactions.Write(ctx, func(store CommentStore) error {
		if input.IdempotencyKey != "" {
			record, found, storeErr := store.IdempotencyRecord(ctx, idempotencyScope, input.IdempotencyKey)
			if storeErr != nil {
				return storeErr
			}
			if found {
				comment, replayErr := reconstructCommentReplay(input, idempotencyScope, requestHash, record)
				if replayErr != nil {
					return replayErr
				}
				result.Comment.Comment = comment
				result.Comment.Media, replayErr = store.ProjectMediaUsages(ctx, input.Media)
				if replayErr != nil {
					return replayErr
				}
				result.Replayed = true
				return nil
			}
		}
		exists, storeErr := store.EntryExists(ctx, input.EntryID)
		if storeErr != nil {
			return storeErr
		}
		if !exists {
			return postNotFound(input.EntryID)
		}
		enabled, storeErr := store.CommentsEnabled(ctx)
		if storeErr != nil {
			return storeErr
		}
		if !enabled {
			return domain.NewError(domain.CodeCommentsDisabled, "new comments are disabled", nil)
		}
		if input.ParentID != nil {
			parent, found, parentErr := store.CommentByID(ctx, *input.ParentID)
			if parentErr != nil {
				return parentErr
			}
			if !found {
				return commentNotFound(*input.ParentID)
			}
			if parent.EntryID != input.EntryID || parent.ParentID != nil {
				return domain.NewError(domain.CodeCommentDepthExceeded, "replies must target a top-level comment on the same post", map[string]any{"parent_id": input.ParentID.String()})
			}
		}
		now := domain.NormalizeTime(service.clock())
		id, generateErr := service.ids(now)
		if generateErr != nil {
			return generateErr
		}
		comment := domain.Comment{ID: id, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), AuthorKind: input.AuthorKind, AuthorName: input.AuthorName, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: now}
		if validateErr := comment.Validate(); validateErr != nil {
			return validateErr
		}
		if insertErr := store.InsertComment(ctx, comment); insertErr != nil {
			return insertErr
		}
		if insertErr := store.ReplaceCommentLocation(ctx, comment.ID, comment.Location); insertErr != nil {
			return insertErr
		}
		if insertErr := store.ReplaceCommentMediaUsages(ctx, comment.ID, input.Media); insertErr != nil {
			return insertErr
		}
		if reconcileErr := store.ReconcileMediaReferences(ctx, mediaUsageIDs(input.Media), now); reconcileErr != nil {
			return reconcileErr
		}
		if input.IdempotencyKey != "" {
			if insertErr := store.InsertIdempotencyRecord(ctx, IdempotencyRecord{Scope: idempotencyScope, Key: input.IdempotencyKey, RequestHash: requestHash, ResponseResourceID: id, CreatedAt: now}); insertErr != nil {
				return insertErr
			}
		}
		result.Comment.Comment = comment
		projectedMedia, projectErr := store.ProjectMediaUsages(ctx, input.Media)
		if projectErr != nil {
			return projectErr
		}
		result.Comment.Media = projectedMedia
		return nil
	})
	if err != nil {
		return CreateCommentResult{}, normalizeCommentRepositoryError(err)
	}
	result.Comment, err = renderComment(result.Comment.Comment, result.Comment.Media)
	if err != nil {
		return CreateCommentResult{}, err
	}
	return result, nil
}

func (service *CommentService) loadCommentReplay(ctx context.Context, input CreateCommentInput, scope, requestHash string) (CreateCommentResult, bool, error) {
	var record IdempotencyRecord
	var found bool
	var media []MediaUsageProjection
	var recordComment domain.Comment
	err := service.transactions.Read(ctx, func(store CommentStore) error {
		var storeErr error
		record, found, storeErr = store.IdempotencyRecord(ctx, scope, input.IdempotencyKey)
		if storeErr != nil || !found {
			return storeErr
		}
		recordComment, storeErr = reconstructCommentReplay(input, scope, requestHash, record)
		if storeErr != nil {
			return storeErr
		}
		media, storeErr = store.ProjectMediaUsages(ctx, input.Media)
		return storeErr
	})
	if err != nil {
		return CreateCommentResult{}, false, normalizeCommentRepositoryError(err)
	}
	if !found {
		return CreateCommentResult{}, false, nil
	}
	comment, err := renderComment(recordComment, media)
	if err != nil {
		return CreateCommentResult{}, false, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	return CreateCommentResult{Comment: comment, Replayed: true}, true, nil
}

// List returns one stable flattened thread and verifies stored parent order.
func (service *CommentService) List(ctx context.Context, entryID domain.ID) (CommentThread, error) {
	if err := contextError(ctx); err != nil {
		return CommentThread{}, err
	}
	if err := validateCommentID(entryID); err != nil {
		return CommentThread{}, err
	}
	var records []domain.Comment
	mediaByComment := make(map[domain.ID][]MediaUsageProjection)
	err := service.transactions.Read(ctx, func(store CommentStore) error {
		exists, storeErr := store.EntryExists(ctx, entryID)
		if storeErr != nil {
			return storeErr
		}
		if !exists {
			return postNotFound(entryID)
		}
		records, storeErr = store.ListComments(ctx, entryID)
		if storeErr != nil {
			return storeErr
		}
		for _, record := range records {
			mediaByComment[record.ID], storeErr = store.CommentMediaUsages(ctx, record.ID)
			if storeErr != nil {
				return storeErr
			}
		}
		return storeErr
	})
	if err != nil {
		return CommentThread{}, normalizeCommentRepositoryError(err)
	}
	thread := CommentThread{Items: make([]Comment, 0, len(records))}
	roots := make(map[domain.ID]struct{})
	for _, record := range records {
		if record.EntryID != entryID {
			return CommentThread{}, corruptCommentThread(entryID)
		}
		if record.ParentID == nil {
			roots[record.ID] = struct{}{}
		} else if _, found := roots[*record.ParentID]; !found {
			return CommentThread{}, corruptCommentThread(entryID)
		}
		comment, renderErr := renderComment(record, mediaByComment[record.ID])
		if renderErr != nil {
			return CommentThread{}, corruptCommentThread(entryID)
		}
		thread.Items = append(thread.Items, comment)
	}
	return thread, nil
}

// Delete previews or executes owner moderation. SQLite owns reply cascade.
func (service *CommentService) Delete(ctx context.Context, id domain.ID, dryRun bool) (DeleteCommentResult, error) {
	if err := contextError(ctx); err != nil {
		return DeleteCommentResult{}, err
	}
	if err := validateCommentID(id); err != nil {
		return DeleteCommentResult{}, err
	}
	result := DeleteCommentResult{DryRun: dryRun}
	operation := func(store CommentStore) error {
		record, found, err := store.CommentByID(ctx, id)
		if err != nil {
			return err
		}
		if !found {
			return commentNotFound(id)
		}
		result.RepliesAffected, err = store.CountCommentReplies(ctx, id)
		if err != nil {
			return err
		}
		mediaUsages, err := store.CommentMediaUsages(ctx, id)
		if err != nil {
			return err
		}
		result.Comment, err = renderComment(record, mediaUsages)
		if err != nil {
			return corruptComment(id)
		}
		if dryRun {
			return nil
		}
		affectedMedia, err := store.CommentCascadeMediaIDs(ctx, id)
		if err != nil {
			return err
		}
		result.Deleted, err = store.DeleteComment(ctx, id)
		if err != nil {
			return err
		}
		if !result.Deleted {
			return commentNotFound(id)
		}
		if err := store.ReconcileMediaReferences(ctx, affectedMedia, domain.NormalizeTime(service.clock())); err != nil {
			return err
		}
		return nil
	}
	var err error
	if dryRun {
		err = service.transactions.Read(ctx, operation)
	} else {
		err = service.transactions.Write(ctx, operation)
	}
	if err != nil {
		return DeleteCommentResult{}, normalizeCommentRepositoryError(err)
	}
	return result, nil
}

func reconstructCommentReplay(input CreateCommentInput, scope, requestHash string, record IdempotencyRecord) (domain.Comment, error) {
	if record.Scope != scope || record.Key != input.IdempotencyKey || !validRequestHash(record.RequestHash) {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	if record.RequestHash != requestHash {
		return domain.Comment{}, domain.NewError(domain.CodeIdempotencyKeyReused, "idempotency key was already used with different comment input", map[string]any{"field": "idempotency_key"})
	}
	comment := domain.Comment{ID: record.ResponseResourceID, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), AuthorKind: input.AuthorKind, AuthorName: input.AuthorName, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: record.CreatedAt}
	if err := comment.Validate(); err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	return comment, nil
}

func commentCreateIdempotencyScope(kind domain.CommentAuthorKind) string {
	if kind == domain.CommentAuthorGuest {
		return guestCommentCreateIdempotencyScope
	}
	return ownerCommentCreateIdempotencyScope
}

func hashCreateComment(input CreateCommentInput) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("moment-comment-create-v1\x00"))
	for _, value := range []string{input.EntryID.String(), commentIDString(input.ParentID), string(input.AuthorKind), input.AuthorName, input.ContentMD} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	for _, usage := range input.Media {
		for _, value := range []string{usage.MediaID.String(), fmt.Sprintf("%d", usage.Position), altPointerString(usage.AltText)} {
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = digest.Write(length[:])
			_, _ = digest.Write([]byte(value))
		}
	}
	for _, value := range commentLocationHashValues(input.Location) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func renderComment(record domain.Comment, media []MediaUsageProjection) (Comment, error) {
	if err := record.Validate(); err != nil {
		return Comment{}, err
	}
	html := ""
	if record.ContentMD != "" {
		var err error
		html, err = domain.RenderCommentMarkdown(record.ContentMD)
		if err != nil {
			return Comment{}, err
		}
	}
	if err := validateCommentMediaProjections(media); err != nil {
		return Comment{}, err
	}
	record.Location = domain.CloneLocation(record.Location)
	return Comment{Comment: record, ContentHTML: html, Media: cloneMediaUsageProjections(media)}, nil
}

func commentLocationHashValues(location *domain.Location) []string {
	if location == nil {
		return nil
	}
	values := []string{
		"<location>", location.Name, location.DisplayName, location.CountryCode,
		location.Country, location.Region, location.City, location.District,
		location.Category, location.Provider, location.ProviderPlaceID,
	}
	if location.Point == nil {
		return append(values, "<no-point>")
	}
	return append(values,
		fmt.Sprintf("%d", location.Point.LatitudeE7),
		fmt.Sprintf("%d", location.Point.LongitudeE7),
		string(location.Point.System),
	)
}

func altPointerString(value *string) string {
	if value == nil {
		return "<missing>"
	}
	return "<present>" + *value
}

func validateCommentID(id domain.ID) error { _, err := domain.ParseID(id.String()); return err }
func cloneCommentID(id *domain.ID) *domain.ID {
	if id == nil {
		return nil
	}
	value := *id
	return &value
}
func commentIDString(id *domain.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
func commentNotFound(id domain.ID) error {
	return domain.NewError(domain.CodeCommentNotFound, fmt.Sprintf("comment %s does not exist", id), map[string]any{"comment_id": id.String()})
}
func corruptComment(id domain.ID) error {
	return domain.NewError(domain.CodeDataIntegrityFailed, "stored comment violates the comment contract", map[string]any{"comment_id": id.String()})
}
func corruptCommentThread(entryID domain.ID) error {
	return domain.NewError(domain.CodeDataIntegrityFailed, "stored comment thread violates the one-level contract", map[string]any{"post_id": entryID.String()})
}

func normalizeCommentRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return domain.NewError(domain.CodeInternal, "comment storage operation failed", nil)
}
