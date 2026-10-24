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

// commentCreateIdempotencyScope names one account's key namespace for writing
// a comment.
//
// The account belongs in the scope rather than in the request digest because
// the two answer different questions. The digest asks whether this is the same
// request, and a mismatch is IDEMPOTENCY_KEY_REUSED -- a true answer for the
// caller that minted the key and a lie for anybody else, who would be told a
// key they had never used was already spent and be left unable to write with
// it. The scope asks whose keys these are, and a lookup in the wrong namespace
// simply misses, so one account's retry of a key another holds writes its own
// comment. This was a single constant for the whole site, which made every key
// collide with every other account's; where the two digests also agreed -- same
// text, and the same display name, which the digest used to cover -- the second
// account was handed the first account's comment ID stamped as its own work.
func commentCreateIdempotencyScope(authorID string) string {
	return "comments.account.create.v1." + authorID
}

// CommentStore is the complete comment authority available within one
// repository transaction.
type CommentStore interface {
	MediaReferenceReconciler
	// EntryVisibility reports whether the post exists and who may read it.
	// The caller decides what to do with that through a Viewer; the store
	// never answers "exists" alone, so no thread can be reached through a
	// post its reader may not see.
	EntryVisibility(context.Context, domain.ID) (domain.Visibility, bool, error)
	CommentsEnabled(context.Context) (bool, error)
	CommentByID(context.Context, domain.ID) (domain.Comment, bool, error)
	CommentsByID(context.Context, []domain.ID) (map[domain.ID]domain.Comment, error)
	ListCommentRoots(context.Context, domain.ID, *CommentPosition, int) ([]domain.Comment, error)
	ListCommentReplies(context.Context, domain.ID, *CommentPosition, int) ([]domain.Comment, error)
	ListDirectCommentReplies(context.Context, domain.ID, domain.ID, *CommentPosition, int) ([]domain.Comment, error)
	CountDirectCommentReplies(context.Context, domain.ID, domain.ID) (int, error)
	ReplyPreviews(context.Context, []domain.ID, int) (map[domain.ID][]domain.Comment, error)
	CommentMediaUsagesByComment(context.Context, []domain.ID) (map[domain.ID][]MediaUsageProjection, error)
	ReplyCounts(context.Context, []domain.ID) (map[domain.ID]int, error)
	CommentAuthorIdentities(context.Context, []string) (map[string]CommentAuthorIdentity, error)
	ListComments(context.Context, domain.ID) ([]domain.Comment, error)
	PinnedCommentID(context.Context, domain.ID) (*domain.ID, error)
	CommentMediaUsages(context.Context, domain.ID) ([]MediaUsageProjection, error)
	ProjectMediaUsages(context.Context, []domain.EntryMediaUsage) ([]MediaUsageProjection, error)
	InsertComment(context.Context, domain.Comment) error
	ReplaceCommentLocation(context.Context, domain.ID, *domain.Location) error
	ReplaceCommentMediaUsages(context.Context, domain.ID, []domain.EntryMediaUsage) error
	CommentCascadeMediaIDs(context.Context, domain.ID) ([]domain.ID, error)
	CountCommentReplies(context.Context, domain.ID) (int, error)
	CountCommentRepliesByOthers(context.Context, domain.ID, string) (int, error)
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
	// ReplyCount and RepliesNextCursor describe a root's replies and are zero
	// on a reply. They live on the root rather than in a parallel map because
	// they are that root's own state; kept alongside it they cannot drift out
	// of step with it.
	ReplyCount        int
	RepliesNextCursor string
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

// CommentAuthor is the resolved public account identity or a historical signature.
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
//
// The array stays flat under pagination. What a partial page loses is
// completeness, not adjacency: a root still sits immediately before whichever
// of its replies came back, so every consumer that relies on that grouping —
// pinned reordering, optimistic insert, optimistic delete — keeps working
// unchanged. ReplyCount on each root reports the true total regardless of how
// many of them travelled.
type CommentThread struct {
	Items           []Comment
	PinnedCommentID *domain.ID
	// Pinned carries the pinned root and its inline replies alongside the page
	// rather than hoisted into it. Reordering Items around a pin would change
	// what the keyset cursor means the moment the pin moved; excluding the
	// pinned root from Items instead would leave a hole in pages already
	// fetched if the pin were cleared mid-scroll.
	Pinned     []Comment
	NextCursor string
}

// CommentReplyPage is one keyset page of replies under a single root.
type CommentReplyPage struct {
	Items      []Comment
	NextCursor string
}

// ListCommentsInput selects one page of root comments.
type ListCommentsInput struct {
	EntryID domain.ID
	Cursor  string
	Limit   int
	// Viewer is who is reading; a thread under a post they may not see is
	// reported as a missing post.
	Viewer Viewer
}

// ListRepliesInput selects one page of replies under a single root.
type ListRepliesInput struct {
	EntryID domain.ID
	RootID  domain.ID
	Scope   CommentReplyScope
	Cursor  string
	Limit   int
	Viewer  Viewer
}

// CommentReplyScope selects a whole floor or only answers to the selected comment.
type CommentReplyScope string

// Reply collection scopes keep the whole floor distinct from direct answers.
const (
	CommentRepliesThread CommentReplyScope = "thread"
	CommentRepliesDirect CommentReplyScope = "direct"
)

// CommentContext supports a permalink without reading every preceding floor.
type CommentContext struct {
	Comment          Comment
	ReplyTarget      *Comment
	RootCommentID    domain.ID
	PinnedCommentID  *domain.ID
	DirectReplyCount int
}

// CommentPin is the singleton top-level comment selected for one post.
type CommentPin struct {
	EntryID         domain.ID
	PinnedCommentID *domain.ID
}

// CreateCommentInput contains application identity, never network identity.
// Adapters cannot use it to persist an IP address.
type CreateCommentInput struct {
	EntryID          domain.ID
	ParentID         *domain.ID
	ReplyToCommentID *domain.ID
	AuthorName       string
	Author           domain.User
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

// Create persists a comment under its authenticated account.
func (service *CommentService) Create(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	if err := domain.ValidateUserID(input.Author.ID); err != nil {
		return CreateCommentResult{}, domain.NewError(domain.CodeUnauthorized, "a comment needs an authenticated author", nil)
	}
	if !input.Author.CanWritePublicContent() {
		return CreateCommentResult{}, domain.NewError(domain.CodeAccountNotApproved, "this account is waiting to be approved by an administrator", nil)
	}
	ctx = WithContentActor(ctx, input.Author)
	input.AuthorName = input.Author.DisplayName
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
	idempotencyScope := commentCreateIdempotencyScope(input.Author.ID)
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
		// Writing under a post is reading it first: a member cannot reply to
		// something they could not open.
		if storeErr := requireReadableEntry(ctx, store, ViewerFor(input.Author, true), input.EntryID); storeErr != nil {
			return storeErr
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
		authorID := input.Author.ID
		comment := domain.Comment{ID: id, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), ReplyToCommentID: cloneCommentID(input.ReplyToCommentID), AuthorName: input.AuthorName, AuthorUserID: &authorID, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: now}
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
		recordComment, storeErr = reconstructCommentReplay(input, scope, requestHash, record)
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

// ListPage returns one keyset page of root comments, each carrying a preview
// of its replies plus the totals a reader needs to ask for the rest.
//
// A thread used to arrive whole. That is fine for a handful of comments and
// indefensible for a popular post: the response grows without bound, and the
// reader waits for replies they will never scroll to.
func (service *CommentService) ListPage(ctx context.Context, input ListCommentsInput) (CommentThread, error) {
	if err := contextError(ctx); err != nil {
		return CommentThread{}, err
	}
	if err := validateCommentID(input.EntryID); err != nil {
		return CommentThread{}, err
	}
	limit, err := normalizeCommentLimit(input.Limit, DefaultCommentPageSize)
	if err != nil {
		return CommentThread{}, err
	}
	after, err := decodeCommentRootCursor(input.Cursor, input.EntryID)
	if err != nil {
		return CommentThread{}, err
	}

	var thread CommentThread
	err = service.transactions.Read(ctx, func(store CommentStore) error {
		if storeErr := requireReadableEntry(ctx, store, input.Viewer, input.EntryID); storeErr != nil {
			return storeErr
		}
		// One extra root answers "is there another page" without a second
		// query, the same trick the timeline uses.
		roots, storeErr := store.ListCommentRoots(ctx, input.EntryID, after, limit+1)
		if storeErr != nil {
			return storeErr
		}
		hasNext := len(roots) > limit
		if hasNext {
			roots = roots[:limit]
		}
		rootIDs := make([]domain.ID, len(roots))
		for index, root := range roots {
			rootIDs[index] = root.ID
		}
		replyCounts, storeErr := store.ReplyCounts(ctx, rootIDs)
		if storeErr != nil {
			return storeErr
		}
		records := make([]domain.Comment, 0, len(roots))
		repliesNextCursor := make(map[domain.ID]string, len(roots))
		// One windowed query for the whole page rather than one per root: the
		// page can hold up to MaxPageSize roots, and a query behind every row
		// is the shape this read already avoids for attachments, totals and
		// identities.
		previews, previewErr := store.ReplyPreviews(ctx, rootIDs, InlineReplyPreviewCount+1)
		if previewErr != nil {
			return previewErr
		}
		for _, root := range roots {
			records = append(records, root)
			if replyCounts[root.ID] == 0 {
				continue
			}
			replies := previews[root.ID]
			if len(replies) > InlineReplyPreviewCount {
				replies = replies[:InlineReplyPreviewCount]
				last := replies[len(replies)-1]
				repliesNextCursor[root.ID] = encodeCommentReplyCursor(root.ID, CommentPosition{CreatedAtMS: domain.TimeToMillis(last.CreatedAt), ID: last.ID})
			}
			records = append(records, replies...)
		}
		rendered, renderErr := service.renderCommentPage(ctx, store, records)
		if renderErr != nil {
			return renderErr
		}
		for index := range rendered {
			id := rendered[index].Comment.ID
			if rendered[index].Comment.ParentID != nil {
				continue
			}
			rendered[index].ReplyCount = replyCounts[id]
			rendered[index].RepliesNextCursor = repliesNextCursor[id]
		}
		thread.Items = rendered
		if hasNext && len(roots) > 0 {
			last := roots[len(roots)-1]
			thread.NextCursor = encodeCommentRootCursor(input.EntryID, CommentPosition{CreatedAtMS: domain.TimeToMillis(last.CreatedAt), ID: last.ID})
		}
		pinnedCommentID, storeErr := store.PinnedCommentID(ctx, input.EntryID)
		if storeErr != nil {
			return storeErr
		}
		thread.PinnedCommentID = cloneCommentID(pinnedCommentID)
		if pinnedCommentID == nil {
			return nil
		}
		pinnedRoot, found, storeErr := store.CommentByID(ctx, *pinnedCommentID)
		if storeErr != nil {
			return storeErr
		}
		if !found {
			return nil
		}
		pinnedRecords := []domain.Comment{pinnedRoot}
		pinnedReplies, storeErr := store.ListCommentReplies(ctx, pinnedRoot.ID, nil, InlineReplyPreviewCount+1)
		if storeErr != nil {
			return storeErr
		}
		pinnedNextCursor := ""
		if len(pinnedReplies) > InlineReplyPreviewCount {
			pinnedReplies = pinnedReplies[:InlineReplyPreviewCount]
			last := pinnedReplies[len(pinnedReplies)-1]
			pinnedNextCursor = encodeCommentReplyCursor(pinnedRoot.ID, CommentPosition{CreatedAtMS: domain.TimeToMillis(last.CreatedAt), ID: last.ID})
		}
		pinnedRecords = append(pinnedRecords, pinnedReplies...)
		pinnedCounts, storeErr := store.ReplyCounts(ctx, []domain.ID{pinnedRoot.ID})
		if storeErr != nil {
			return storeErr
		}
		pinnedRendered, renderErr := service.renderCommentPage(ctx, store, pinnedRecords)
		if renderErr != nil {
			return renderErr
		}
		if len(pinnedRendered) > 0 {
			pinnedRendered[0].ReplyCount = pinnedCounts[pinnedRoot.ID]
			pinnedRendered[0].RepliesNextCursor = pinnedNextCursor
		}
		thread.Pinned = pinnedRendered
		return nil
	})
	if err != nil {
		return CommentThread{}, normalizeCommentRepositoryError(err)
	}
	return thread, nil
}

// GetContext reads the selected comment and its immediate reply target atomically.
func (service *CommentService) GetContext(ctx context.Context, entryID, commentID domain.ID, viewer Viewer) (CommentContext, error) {
	if err := contextError(ctx); err != nil {
		return CommentContext{}, err
	}
	for _, id := range []domain.ID{entryID, commentID} {
		if err := validateCommentID(id); err != nil {
			return CommentContext{}, err
		}
	}
	var result CommentContext
	err := service.transactions.Read(ctx, func(store CommentStore) error {
		if err := requireReadableEntry(ctx, store, viewer, entryID); err != nil {
			return err
		}
		comment, found, err := store.CommentByID(ctx, commentID)
		if err != nil {
			return err
		}
		if !found || comment.EntryID != entryID {
			return commentNotFound(commentID)
		}
		result.RootCommentID = comment.ID
		if comment.ParentID != nil {
			result.RootCommentID = *comment.ParentID
		}
		records := []domain.Comment{comment}
		if comment.ReplyToCommentID != nil {
			target, exists, err := store.CommentByID(ctx, *comment.ReplyToCommentID)
			if err != nil {
				return err
			}
			if exists && target.EntryID == entryID && (target.ID == result.RootCommentID || (target.ParentID != nil && *target.ParentID == result.RootCommentID)) {
				records = append(records, target)
			}
		}
		rendered, err := service.renderCommentPage(ctx, store, records)
		if err != nil {
			return err
		}
		for index := range rendered {
			if rendered[index].Comment.ParentID == nil {
				count, err := store.CountCommentReplies(ctx, rendered[index].Comment.ID)
				if err != nil {
					return err
				}
				rendered[index].ReplyCount = count
			}
		}
		result.Comment = rendered[0]
		result.DirectReplyCount, err = store.CountDirectCommentReplies(ctx, entryID, commentID)
		if err != nil {
			return err
		}
		pinned, err := store.PinnedCommentID(ctx, entryID)
		if err != nil {
			return err
		}
		result.PinnedCommentID = cloneCommentID(pinned)
		if len(rendered) > 1 {
			result.ReplyTarget = &rendered[1]
		}
		return nil
	})
	return result, normalizeCommentRepositoryError(err)
}

// ListReplies returns a keyset page in the selected conversation scope.
func (service *CommentService) ListReplies(ctx context.Context, input ListRepliesInput) (CommentReplyPage, error) {
	if err := contextError(ctx); err != nil {
		return CommentReplyPage{}, err
	}
	if err := validateCommentID(input.EntryID); err != nil {
		return CommentReplyPage{}, err
	}
	if err := validateCommentID(input.RootID); err != nil {
		return CommentReplyPage{}, err
	}
	limit, err := normalizeCommentLimit(input.Limit, DefaultReplyPageSize)
	if err != nil {
		return CommentReplyPage{}, err
	}
	if input.Scope == "" {
		input.Scope = CommentRepliesThread
	}
	if input.Scope != CommentRepliesThread && input.Scope != CommentRepliesDirect {
		return CommentReplyPage{}, domain.NewError(domain.CodeInvalidArgument, "scope must be thread or direct", map[string]any{"field": "scope"})
	}
	after, err := decodeScopedCommentReplyCursor(input.Cursor, input.RootID, input.Scope)
	if err != nil {
		return CommentReplyPage{}, err
	}

	var page CommentReplyPage
	err = service.transactions.Read(ctx, func(store CommentStore) error {
		if storeErr := requireReadableEntry(ctx, store, input.Viewer, input.EntryID); storeErr != nil {
			return storeErr
		}
		root, found, storeErr := store.CommentByID(ctx, input.RootID)
		if storeErr != nil {
			return storeErr
		}
		// The root has to belong to the post named in the request. Without
		// that check a caller could page any thread through any post's URL,
		// and the reply cursor alone would not catch it.
		if !found || root.EntryID != input.EntryID || (input.Scope == CommentRepliesThread && root.ParentID != nil) {
			return commentNotFound(input.RootID)
		}
		var replies []domain.Comment
		if input.Scope == CommentRepliesDirect {
			replies, storeErr = store.ListDirectCommentReplies(ctx, input.EntryID, input.RootID, after, limit+1)
		} else {
			replies, storeErr = store.ListCommentReplies(ctx, input.RootID, after, limit+1)
		}
		if storeErr != nil {
			return storeErr
		}
		hasNext := len(replies) > limit
		if hasNext {
			replies = replies[:limit]
		}
		rendered, renderErr := service.renderCommentPage(ctx, store, replies)
		if renderErr != nil {
			return renderErr
		}
		page.Items = rendered
		if hasNext && len(replies) > 0 {
			last := replies[len(replies)-1]
			page.NextCursor = encodeScopedCommentReplyCursor(input.RootID, CommentPosition{CreatedAtMS: domain.TimeToMillis(last.CreatedAt), ID: last.ID}, input.Scope)
		}
		return nil
	})
	if err != nil {
		return CommentReplyPage{}, normalizeCommentRepositoryError(err)
	}
	return page, nil
}

// renderCommentPage resolves everything a page of comments needs in batches:
// attachments, author identities and reply targets each cost one query for the
// whole page rather than one per row.
func (service *CommentService) renderCommentPage(ctx context.Context, store CommentStore, records []domain.Comment) ([]Comment, error) {
	if len(records) == 0 {
		return []Comment{}, nil
	}
	ids := make([]domain.ID, len(records))
	for index, record := range records {
		ids[index] = record.ID
	}
	mediaByComment, err := store.CommentMediaUsagesByComment(ctx, ids)
	if err != nil {
		return nil, err
	}
	authors, err := resolveCommentAuthors(ctx, store, records)
	if err != nil {
		return nil, err
	}
	replyTargets, err := resolveCommentReplyTargets(ctx, store, records)
	if err != nil {
		return nil, err
	}
	rendered := make([]Comment, 0, len(records))
	for _, record := range records {
		comment, renderErr := renderComment(record, authors[record.ID], replyTargets[record.ID], mediaByComment[record.ID])
		if renderErr != nil {
			return nil, corruptComment(record.ID)
		}
		rendered = append(rendered, comment)
	}
	return rendered, nil
}

// List returns a whole thread in one response.
//
// ListPage is what the browser uses. This stays for callers that genuinely
// want everything at once — the CLI reading a thread, and tests asserting the
// canonical flattened order — and it keeps the invariant those callers rely
// on: every reply is present, directly after its root.
func (service *CommentService) List(ctx context.Context, entryID domain.ID, viewer Viewer) (CommentThread, error) {
	if err := contextError(ctx); err != nil {
		return CommentThread{}, err
	}
	if err := validateCommentID(entryID); err != nil {
		return CommentThread{}, err
	}
	var records []domain.Comment
	var pinnedCommentID *domain.ID
	var rendered []Comment
	var replyCounts map[domain.ID]int
	err := service.transactions.Read(ctx, func(store CommentStore) error {
		if storeErr := requireReadableEntry(ctx, store, viewer, entryID); storeErr != nil {
			return storeErr
		}
		var storeErr error
		records, storeErr = store.ListComments(ctx, entryID)
		if storeErr != nil {
			return storeErr
		}
		pinnedCommentID, storeErr = store.PinnedCommentID(ctx, entryID)
		if storeErr != nil {
			return storeErr
		}
		rootIDs := make([]domain.ID, 0, len(records))
		for _, record := range records {
			if record.ParentID == nil {
				rootIDs = append(rootIDs, record.ID)
			}
		}
		replyCounts, storeErr = store.ReplyCounts(ctx, rootIDs)
		if storeErr != nil {
			return storeErr
		}
		rendered, storeErr = service.renderCommentPage(ctx, store, records)
		return storeErr
	})
	if err != nil {
		return CommentThread{}, normalizeCommentRepositoryError(err)
	}
	thread := CommentThread{Items: make([]Comment, 0, len(rendered)), PinnedCommentID: cloneCommentID(pinnedCommentID)}
	roots := make(map[domain.ID]struct{})
	for _, comment := range rendered {
		record := comment.Comment
		if record.EntryID != entryID {
			return CommentThread{}, corruptCommentThread(entryID)
		}
		if record.ParentID == nil {
			roots[record.ID] = struct{}{}
			comment.ReplyCount = replyCounts[record.ID]
		} else if _, found := roots[*record.ParentID]; !found {
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

// Pin selects the single top-level comment shown above a post's thread.
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
	if err := requireAdministrator(ctx); err != nil {
		return CommentPin{}, err
	}
	if err := validateCommentID(entryID); err != nil {
		return CommentPin{}, err
	}
	if err := validateCommentID(commentID); err != nil {
		return CommentPin{}, err
	}
	result := CommentPin{EntryID: entryID}
	// The transport admits administrators for moderation.
	err := service.transactions.Write(ctx, func(store CommentStore) error {
		if err := requireReadableEntry(ctx, store, ViewerFromContext(ctx), entryID); err != nil {
			return err
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

// DeleteBy checks the verified actor and the full reply cascade before deletion.
func (service *CommentService) DeleteBy(ctx context.Context, id domain.ID, actor domain.User, dryRun bool) (DeleteCommentResult, error) {
	return service.delete(ctx, id, actor, dryRun)
}

// Delete uses the context account to authorize a comment and its reply cascade.
func (service *CommentService) Delete(ctx context.Context, id domain.ID, dryRun bool) (DeleteCommentResult, error) {
	actor, present := ContentActor(ctx)
	if !present {
		return DeleteCommentResult{}, domain.NewError(domain.CodeUnauthorized, "an active account identity is required", nil)
	}
	return service.delete(ctx, id, actor, dryRun)
}

// delete checks the actor and reply cascade inside the deletion transaction.
func (service *CommentService) delete(ctx context.Context, id domain.ID, actor domain.User, dryRun bool) (DeleteCommentResult, error) {
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
		if domain.ValidateUserID(actor.ID) != nil || !actor.CanWritePublicContent() {
			return domain.NewError(domain.CodeUnauthorized, "an active account is required to remove comments", nil)
		}
		if err := requireReadableEntry(ctx, store, ViewerFor(actor, true), record.EntryID); err != nil {
			return err
		}
		if err := authorizeCommentRemoval(ctx, store, record, actor); err != nil {
			return err
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

func authorizeCommentRemoval(ctx context.Context, store CommentStore, record domain.Comment, actor domain.User) error {
	if actor.IsAdmin() {
		return nil
	}
	if record.AuthorUserID == nil || *record.AuthorUserID != actor.ID {
		return domain.NewError(domain.CodeUnauthorized, "only the author or the administrator can remove a comment", nil)
	}
	repliesByOthers, err := store.CountCommentRepliesByOthers(ctx, record.ID, actor.ID)
	if err != nil {
		return err
	}
	if repliesByOthers > 0 {
		return domain.NewError(domain.CodeUnauthorized, "removing this comment would remove replies other people wrote, which only the administrator can do", map[string]any{"comment_id": record.ID.String(), "replies_by_others": repliesByOthers})
	}
	return nil
}

// reconstructCommentReplay rebuilds the stored comment from the request that
// produced it. The request hash proves the input is byte-identical, so every
// field the input carries reconstructs exactly.
//
// The author comes from the same admitted account the original write used, so
// a replay reconstructs the identity it was written under rather than looking
// one up. That is sound because an idempotency key belongs to the caller that
// minted it: the scope carries the account, so a different caller looking the
// same key up never finds this record at all.
func reconstructCommentReplay(input CreateCommentInput, scope, requestHash string, record IdempotencyRecord) (domain.Comment, error) {
	if record.Scope != scope || record.Key != input.IdempotencyKey || !validRequestHash(record.RequestHash) {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	if record.RequestHash != requestHash {
		return domain.Comment{}, domain.NewError(domain.CodeIdempotencyKeyReused, "idempotency key was already used with different comment input", map[string]any{"field": "idempotency_key"})
	}
	replayAuthorID := input.Author.ID
	comment := domain.Comment{ID: record.ResponseResourceID, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), ReplyToCommentID: cloneCommentID(input.ReplyToCommentID), AuthorName: input.AuthorName, AuthorUserID: &replayAuthorID, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: record.CreatedAt}
	if err := comment.Validate(); err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency result violates the comment contract", nil)
	}
	return comment, nil
}

// hashCreateComment digests what the caller actually sent, and nothing the
// server stamped on top of it.
//
// Version 3 drops the author name and the author kind. Create derives both from
// the admitted account, so neither is part of the request: somebody who renamed
// themselves between a write and its retry sent identical bytes the second time
// and was answered IDEMPOTENCY_KEY_REUSED for a request that had not changed.
// Which account is writing is the scope's business now, not the digest's.
//
// Version 2 added the reply target. A digest that ignored it would let one
// idempotency key replay a stored comment for a request that named a different
// target, returning a perfectly well-formed answer to the wrong comment.
// Retiring a version costs nothing here: the scope moved in the same change, so
// a key minted under an older digest is out of reach of the namespace the new
// one is looked up in.
func hashCreateComment(input CreateCommentInput) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("moment-comment-create-v3\x00"))
	for _, value := range []string{input.EntryID.String(), commentIDString(input.ParentID), commentIDString(input.ReplyToCommentID), input.ContentMD} {
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

// resolveCommentAuthors resolves real accounts and preserves anonymous snapshots.
func resolveCommentAuthors(ctx context.Context, store CommentStore, records []domain.Comment) (map[domain.ID]CommentAuthor, error) {
	wanted := map[string]bool{}
	for _, record := range records {
		if record.AuthorUserID != nil {
			wanted[*record.AuthorUserID] = true
		}
	}
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	identities := map[string]CommentAuthorIdentity{}
	if len(ids) > 0 {
		var err error
		identities, err = store.CommentAuthorIdentities(ctx, ids)
		if err != nil {
			return nil, err
		}
	}
	authors := make(map[domain.ID]CommentAuthor, len(records))
	for _, record := range records {
		author := CommentAuthor{DisplayName: record.AuthorName}
		if record.AuthorUserID != nil {
			if identity, found := identities[*record.AuthorUserID]; found {
				author = CommentAuthor{UserID: identity.UserID, Username: identity.Username, DisplayName: identity.DisplayName, AvatarMediaID: identity.AvatarMediaID, ProfileURL: domain.ProfileURL(identity.Username)}
			}
		}
		authors[record.ID] = author
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

// requireReadableEntry is the one gate between a comment operation and the
// post it names. Missing and unreadable are the same answer on purpose.
func requireReadableEntry(ctx context.Context, store CommentStore, viewer Viewer, entryID domain.ID) error {
	if readable, ok := store.(interface {
		ReadableEntry(context.Context, string, Viewer) (bool, error)
	}); ok {
		allowed, err := readable.ReadableEntry(ctx, entryID.String(), viewer)
		if err != nil {
			return err
		}
		if !allowed {
			return postHiddenFrom(entryID)
		}
		return nil
	}
	return domain.NewError(domain.CodeDataIntegrityFailed, "comment visibility storage is unavailable", nil)
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
