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

// Replay records written by the retired anonymous path still carry the scope
// "comments.guest.create.v1". Nothing produces that scope any more, and the
// rows are deliberately left in place: deleting them would change the replay
// answer for keys a caller may still hold.
const ownerCommentCreateIdempotencyScope = "comments.owner.create.v1"

// CommentStore is the complete comment authority available within one
// repository transaction.
type CommentStore interface {
	MediaReferenceReconciler
	EntryExists(context.Context, domain.ID) (bool, error)
	CommentsEnabled(context.Context) (bool, error)
	CommentByID(context.Context, domain.ID) (domain.Comment, bool, error)
	CommentsByID(context.Context, []domain.ID) (map[domain.ID]domain.Comment, error)
	CommentAuthorIdentities(context.Context, []string) (map[string]CommentAuthorIdentity, error)
	SiteOwnerID(context.Context) (string, error)
	ListComments(context.Context, domain.ID) ([]domain.Comment, error)
	PinnedCommentID(context.Context, domain.ID) (*domain.ID, error)
	CommentMediaUsages(context.Context, domain.ID) ([]MediaUsageProjection, error)
	ProjectMediaUsages(context.Context, []domain.EntryMediaUsage) ([]MediaUsageProjection, error)
	InsertComment(context.Context, domain.Comment) error
	ReplaceCommentLocation(context.Context, domain.ID, *domain.Location) error
	ReplaceCommentMediaUsages(context.Context, domain.ID, []domain.EntryMediaUsage) error
	CommentCascadeMediaIDs(context.Context, domain.ID) ([]domain.ID, error)
	CountCommentReplies(context.Context, domain.ID) (int, error)
	DeleteComment(context.Context, domain.ID) (bool, error)
	ReplacePinnedComment(context.Context, domain.ID, *domain.ID) error
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
	Author      CommentAuthor
	ReplyTo     *CommentReplyTarget
	ContentHTML string
	Media       []MediaUsageProjection
}

// CommentReplyTarget names the comment a reply is answering, together with the
// identity to show for it.
//
// The target can sit outside the page the reader has loaded, so the author is
// resolved server side rather than looked up client side against whatever
// happens to be on screen.
type CommentReplyTarget struct {
	CommentID domain.ID
	Author    CommentAuthor
}

// CommentAuthor is the identity a reader sees beside a comment, resolved at
// read time rather than copied out of the row.
//
// Reading through a reference is what lets one rename reach every comment an
// account ever wrote, and it is what stopped the web and the CLI disagreeing:
// the web used to overlay the site identity on top of the stored "Owner"
// placeholder and the CLI printed the placeholder as-is.
//
// A legacy guest row has no account behind it, so it carries only DisplayName;
// Username, UserID and ProfileURL are empty and callers must render it as
// plain text with no link.
type CommentAuthor struct {
	UserID        string
	Username      string
	DisplayName   string
	AvatarMediaID *string
	ProfileURL    string
}

// CommentAuthorIdentity is one account's public profile as the comment reader
// needs it.
type CommentAuthorIdentity struct {
	UserID        string
	Username      string
	DisplayName   string
	AvatarMediaID *string
}

// CommentThread is a stable flattened one-level thread. Roots are ordered by
// creation time and ID; each root is immediately followed by similarly ordered
// direct replies.
type CommentThread struct {
	Items           []Comment
	PinnedCommentID *domain.ID
}

// CommentPin is the singleton top-level comment selected for one post.
type CommentPin struct {
	EntryID         domain.ID
	PinnedCommentID *domain.ID
}

// CreateCommentInput contains owner/guest application identity, never network
// identity. Adapters cannot use it to persist an IP address.
type CreateCommentInput struct {
	EntryID          domain.ID
	ParentID         *domain.ID
	ReplyToCommentID *domain.ID
	AuthorKind       domain.CommentAuthorKind
	AuthorName       string
	ContentMD        string
	Media            []domain.EntryMediaUsage
	Location         *domain.Location
	IdempotencyKey   string
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

// CreateOwner persists an owner-marked comment. It is the only way to write a
// comment: every adapter reaches the thread through an authenticated owner
// route, so no caller can choose the author kind.
func (service *CommentService) CreateOwner(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	input.AuthorKind = domain.CommentAuthorOwner
	input.AuthorName = "Owner"
	return service.create(ctx, input)
}

func (service *CommentService) create(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	if err := contextError(ctx); err != nil {
		return CreateCommentResult{}, err
	}
	if err := validateCommentID(input.EntryID); err != nil {
		return CreateCommentResult{}, err
	}
	if input.ReplyToCommentID != nil {
		if err := validateCommentID(*input.ReplyToCommentID); err != nil {
			return CreateCommentResult{}, err
		}
		if input.ParentID == nil {
			return CreateCommentResult{}, domain.NewError(domain.CodeInvalidArgument, "a top-level comment cannot answer another comment", map[string]any{"field": "reply_to_comment_id"})
		}
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
	idempotencyScope := ownerCommentCreateIdempotencyScope
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
				replayOwnerID, replayErr := store.SiteOwnerID(ctx)
				if replayErr != nil {
					return replayErr
				}
				comment, replayErr := reconstructCommentReplay(input, replayOwnerID, idempotencyScope, requestHash, record)
				if replayErr != nil {
					return replayErr
				}
				result.Comment.Comment = comment
				result.Comment.Media, replayErr = store.ProjectMediaUsages(ctx, input.Media)
				if replayErr != nil {
					return replayErr
				}
				authors, replayErr := resolveCommentAuthors(ctx, store, []domain.Comment{comment})
				if replayErr != nil {
					return replayErr
				}
				result.Comment.Author = authors[comment.ID]
				replayTargets, replayErr := resolveCommentReplyTargets(ctx, store, []domain.Comment{comment})
				if replayErr != nil {
					return replayErr
				}
				result.Comment.ReplyTo = replayTargets[comment.ID]
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
		// The reply target must belong to the same conversation: either the
		// root itself or another reply under it. Restated here rather than left
		// to the trigger alone, matching how the depth rule is handled — a
		// caller gets a typed error describing what it did, not a translated
		// abort message.
		if input.ReplyToCommentID != nil {
			target, found, targetErr := store.CommentByID(ctx, *input.ReplyToCommentID)
			if targetErr != nil {
				return targetErr
			}
			if !found {
				return commentNotFound(*input.ReplyToCommentID)
			}
			sameConversation := target.ID == *input.ParentID ||
				(target.ParentID != nil && *target.ParentID == *input.ParentID)
			if target.EntryID != input.EntryID || !sameConversation {
				return domain.NewError(domain.CodeInvalidArgument, "a reply can only answer its own root or another reply under it", map[string]any{"field": "reply_to_comment_id"})
			}
		}
		now := domain.NormalizeTime(service.clock())
		id, generateErr := service.ids(now)
		if generateErr != nil {
			return generateErr
		}
		// The authoring account is stamped inside the transaction that writes
		// the row, so a rename racing the insert cannot leave the comment
		// pointing at an identity that never wrote it.
		ownerID, ownerErr := store.SiteOwnerID(ctx)
		if ownerErr != nil {
			return ownerErr
		}
		comment := domain.Comment{ID: id, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), ReplyToCommentID: cloneCommentID(input.ReplyToCommentID), AuthorKind: input.AuthorKind, AuthorName: input.AuthorName, AuthorUserID: &ownerID, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: now}
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
		authors, authorErr := resolveCommentAuthors(ctx, store, []domain.Comment{comment})
		if authorErr != nil {
			return authorErr
		}
		result.Comment.Author = authors[comment.ID]
		replyTargets, authorErr := resolveCommentReplyTargets(ctx, store, []domain.Comment{comment})
		if authorErr != nil {
			return authorErr
		}
		result.Comment.ReplyTo = replyTargets[comment.ID]
		return nil
	})
	if err != nil {
		return CreateCommentResult{}, normalizeCommentRepositoryError(err)
	}
	result.Comment, err = renderComment(result.Comment.Comment, result.Comment.Author, result.Comment.ReplyTo, result.Comment.Media)
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
	var author CommentAuthor
	var replyTo *CommentReplyTarget
	err := service.transactions.Read(ctx, func(store CommentStore) error {
		var storeErr error
		record, found, storeErr = store.IdempotencyRecord(ctx, scope, input.IdempotencyKey)
		if storeErr != nil || !found {
			return storeErr
		}
		ownerID, storeErr := store.SiteOwnerID(ctx)
		if storeErr != nil {
			return storeErr
		}
		recordComment, storeErr = reconstructCommentReplay(input, ownerID, scope, requestHash, record)
		if storeErr != nil {
			return storeErr
		}
		media, storeErr = store.ProjectMediaUsages(ctx, input.Media)
		if storeErr != nil {
			return storeErr
		}
		authors, storeErr := resolveCommentAuthors(ctx, store, []domain.Comment{recordComment})
		if storeErr != nil {
			return storeErr
		}
		author = authors[recordComment.ID]
		replyTargets, storeErr := resolveCommentReplyTargets(ctx, store, []domain.Comment{recordComment})
		if storeErr != nil {
			return storeErr
		}
		replyTo = replyTargets[recordComment.ID]
		return nil
	})
	if err != nil {
		return CreateCommentResult{}, false, normalizeCommentRepositoryError(err)
	}
	if !found {
		return CreateCommentResult{}, false, nil
	}
	comment, err := renderComment(recordComment, author, replyTo, media)
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
	var pinnedCommentID *domain.ID
	var authors map[domain.ID]CommentAuthor
	var replyTargets map[domain.ID]*CommentReplyTarget
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
		pinnedCommentID, storeErr = store.PinnedCommentID(ctx, entryID)
		if storeErr != nil {
			return storeErr
		}
		for _, record := range records {
			mediaByComment[record.ID], storeErr = store.CommentMediaUsages(ctx, record.ID)
			if storeErr != nil {
				return storeErr
			}
		}
		authors, storeErr = resolveCommentAuthors(ctx, store, records)
		if storeErr != nil {
			return storeErr
		}
		replyTargets, storeErr = resolveCommentReplyTargets(ctx, store, records)
		return storeErr
	})
	if err != nil {
		return CommentThread{}, normalizeCommentRepositoryError(err)
	}
	thread := CommentThread{Items: make([]Comment, 0, len(records)), PinnedCommentID: cloneCommentID(pinnedCommentID)}
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
		comment, renderErr := renderComment(record, authors[record.ID], replyTargets[record.ID], mediaByComment[record.ID])
		if renderErr != nil {
			return CommentThread{}, corruptCommentThread(entryID)
		}
		thread.Items = append(thread.Items, comment)
	}
	if thread.PinnedCommentID != nil {
		if _, found := roots[*thread.PinnedCommentID]; !found {
			return CommentThread{}, corruptCommentThread(entryID)
		}
	}
	return thread, nil
}

// Pin atomically selects one top-level comment for emphasis in its post.
// Assigning the current root is an idempotent success.
func (service *CommentService) Pin(ctx context.Context, entryID, commentID domain.ID) (CommentPin, error) {
	return service.changePin(ctx, entryID, commentID, true)
}

// Unpin clears the selected root. Repeating the same clear is safe.
func (service *CommentService) Unpin(ctx context.Context, entryID, commentID domain.ID) (CommentPin, error) {
	return service.changePin(ctx, entryID, commentID, false)
}

func (service *CommentService) changePin(ctx context.Context, entryID, commentID domain.ID, active bool) (CommentPin, error) {
	if err := contextError(ctx); err != nil {
		return CommentPin{}, err
	}
	if err := validateCommentID(entryID); err != nil {
		return CommentPin{}, err
	}
	if err := validateCommentID(commentID); err != nil {
		return CommentPin{}, err
	}
	result := CommentPin{EntryID: entryID}
	err := service.transactions.Write(ctx, func(store CommentStore) error {
		exists, err := store.EntryExists(ctx, entryID)
		if err != nil {
			return err
		}
		if !exists {
			return postNotFound(entryID)
		}
		current, err := store.PinnedCommentID(ctx, entryID)
		if err != nil {
			return err
		}
		if !active {
			if current != nil && *current != commentID {
				return commentNotFound(commentID)
			}
			if current != nil {
				if err := store.ReplacePinnedComment(ctx, entryID, nil); err != nil {
					return err
				}
			}
			return nil
		}
		record, found, err := store.CommentByID(ctx, commentID)
		if err != nil {
			return err
		}
		if !found || record.EntryID != entryID {
			return commentNotFound(commentID)
		}
		if record.ParentID != nil {
			return domain.NewError(domain.CodeInvalidArgument, "only a top-level comment can be pinned", map[string]any{"field": "comment_id"})
		}
		if current == nil || *current != commentID {
			if err := store.ReplacePinnedComment(ctx, entryID, &commentID); err != nil {
				return err
			}
		}
		result.PinnedCommentID = &commentID
		return nil
	})
	if err != nil {
		return CommentPin{}, normalizeCommentRepositoryError(err)
	}
	return result, nil
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
		authors, err := resolveCommentAuthors(ctx, store, []domain.Comment{record})
		if err != nil {
			return err
		}
		replyTargets, err := resolveCommentReplyTargets(ctx, store, []domain.Comment{record})
		if err != nil {
			return err
		}
		result.Comment, err = renderComment(record, authors[record.ID], replyTargets[record.ID], mediaUsages)
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

// reconstructCommentReplay rebuilds the stored comment from the request that
// produced it. The request hash proves the input is byte-identical, so every
// field the input carries reconstructs exactly.
//
// ownerID covers the one field it does not carry. The account that authored a
// comment is stamped by the server, not supplied by the caller, and owner_id
// is minted once at migration and never updated — SiteUpdate has no field for
// it — so reading it now yields the same value the original write stamped.
func reconstructCommentReplay(input CreateCommentInput, ownerID, scope, requestHash string, record IdempotencyRecord) (domain.Comment, error) {
	if record.Scope != scope || record.Key != input.IdempotencyKey || !validRequestHash(record.RequestHash) {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	if record.RequestHash != requestHash {
		return domain.Comment{}, domain.NewError(domain.CodeIdempotencyKeyReused, "idempotency key was already used with different comment input", map[string]any{"field": "idempotency_key"})
	}
	comment := domain.Comment{ID: record.ResponseResourceID, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), ReplyToCommentID: cloneCommentID(input.ReplyToCommentID), AuthorKind: input.AuthorKind, AuthorName: input.AuthorName, AuthorUserID: &ownerID, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: record.CreatedAt}
	if err := comment.Validate(); err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	return comment, nil
}

func hashCreateComment(input CreateCommentInput) string {
	digest := sha256.New()
	// Version 2 adds the reply target. A digest that ignored it would let one
	// idempotency key replay a stored comment for a request that named a
	// different target, returning a perfectly well-formed answer to the wrong
	// comment. Retiring v1 costs nothing: a key reused across the two versions
	// reads as a changed request, which is exactly what it is.
	_, _ = digest.Write([]byte("moment-comment-create-v2\x00"))
	for _, value := range []string{input.EntryID.String(), commentIDString(input.ParentID), commentIDString(input.ReplyToCommentID), string(input.AuthorKind), input.AuthorName, input.ContentMD} {
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

// resolveCommentAuthors maps every comment in the batch to the identity that
// should be rendered for it.
//
// The lookup is batched and keyed by account ID because that is the shape a
// real user table needs; today exactly one account can match. Owner rows
// written before author_user_id existed — anything restored from an older
// archive — have no reference of their own, so they fall back to the site
// owner, which is by definition who wrote them.
// resolveCommentReplyTargets loads the comments the batch answers and resolves
// an identity for each. Targets already present in the batch are reused rather
// than fetched again, so an ordinary thread costs no extra query at all.
func resolveCommentReplyTargets(ctx context.Context, store CommentStore, records []domain.Comment) (map[domain.ID]*CommentReplyTarget, error) {
	targets := make(map[domain.ID]*CommentReplyTarget)
	known := make(map[domain.ID]domain.Comment, len(records))
	for _, record := range records {
		known[record.ID] = record
	}
	missing := make([]domain.ID, 0)
	for _, record := range records {
		if record.ReplyToCommentID == nil {
			continue
		}
		if _, found := known[*record.ReplyToCommentID]; !found {
			missing = append(missing, *record.ReplyToCommentID)
		}
	}
	if len(missing) > 0 {
		fetched, err := store.CommentsByID(ctx, missing)
		if err != nil {
			return nil, err
		}
		for id, record := range fetched {
			known[id] = record
		}
	}
	referenced := make([]domain.Comment, 0)
	for _, record := range records {
		if record.ReplyToCommentID == nil {
			continue
		}
		if target, found := known[*record.ReplyToCommentID]; found {
			referenced = append(referenced, target)
		}
	}
	if len(referenced) == 0 {
		return targets, nil
	}
	authors, err := resolveCommentAuthors(ctx, store, referenced)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.ReplyToCommentID == nil {
			continue
		}
		target, found := known[*record.ReplyToCommentID]
		if !found {
			// ON DELETE SET NULL means a non-null reference always points at a
			// live row, so reaching here would mean the database disagrees with
			// its own constraint. Drop the annotation rather than fail the read.
			continue
		}
		targets[record.ID] = &CommentReplyTarget{CommentID: target.ID, Author: authors[target.ID]}
	}
	return targets, nil
}

func resolveCommentAuthors(ctx context.Context, store CommentStore, records []domain.Comment) (map[domain.ID]CommentAuthor, error) {
	authors := make(map[domain.ID]CommentAuthor, len(records))
	ownerID := ""
	wanted := make(map[string]struct{})
	for _, record := range records {
		if record.AuthorKind == domain.CommentAuthorGuest {
			continue
		}
		if record.AuthorUserID != nil {
			wanted[*record.AuthorUserID] = struct{}{}
			continue
		}
		if ownerID == "" {
			resolved, err := store.SiteOwnerID(ctx)
			if err != nil {
				return nil, err
			}
			ownerID = resolved
		}
		wanted[ownerID] = struct{}{}
	}
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	identities := map[string]CommentAuthorIdentity{}
	if len(ids) > 0 {
		resolved, err := store.CommentAuthorIdentities(ctx, ids)
		if err != nil {
			return nil, err
		}
		identities = resolved
	}
	for _, record := range records {
		if record.AuthorKind == domain.CommentAuthorGuest {
			authors[record.ID] = CommentAuthor{DisplayName: record.AuthorName}
			continue
		}
		id := ownerID
		if record.AuthorUserID != nil {
			id = *record.AuthorUserID
		}
		identity, found := identities[id]
		if !found {
			// An owner comment whose account cannot be resolved still has to
			// render. The stored name is the only thing left to show.
			authors[record.ID] = CommentAuthor{DisplayName: record.AuthorName}
			continue
		}
		authors[record.ID] = CommentAuthor{
			UserID: identity.UserID, Username: identity.Username,
			DisplayName: identity.DisplayName, AvatarMediaID: identity.AvatarMediaID,
			ProfileURL: "/@" + identity.Username,
		}
	}
	return authors, nil
}

func renderComment(record domain.Comment, author CommentAuthor, replyTo *CommentReplyTarget, media []MediaUsageProjection) (Comment, error) {
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
	return Comment{Comment: record, Author: author, ReplyTo: replyTo, ContentHTML: html, Media: cloneMediaUsageProjections(media)}, nil
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
