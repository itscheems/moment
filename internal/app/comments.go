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
//
// Replay records written by the retired anonymous path still carry the scope
// "comments.guest.create.v1", and records written before the account joined the
// scope carry "comments.owner.create.v1". Nothing produces either any more and
// no lookup reaches either, so a key still in flight across this change writes
// a second comment instead of replaying the first. The rows stay where they
// are: an idempotency key is a short-lived retry token, and deleting rows for
// tidiness is not worth a migration.
func commentCreateIdempotencyScope(authorID string) string {
	return "comments.account.create.v1." + authorID
}

// CommentStore is the complete comment authority available within one
// repository transaction.
type CommentStore interface {
	MediaReferenceReconciler
	EntryExists(context.Context, domain.ID) (bool, error)
	CommentsEnabled(context.Context) (bool, error)
	CommentByID(context.Context, domain.ID) (domain.Comment, bool, error)
	CommentsByID(context.Context, []domain.ID) (map[domain.ID]domain.Comment, error)
	ListCommentRoots(context.Context, domain.ID, *CommentPosition, int) ([]domain.Comment, error)
	ListCommentReplies(context.Context, domain.ID, *CommentPosition, int) ([]domain.Comment, error)
	ReplyPreviews(context.Context, []domain.ID, int) (map[domain.ID][]domain.Comment, error)
	CommentMediaUsagesByComment(context.Context, []domain.ID) (map[domain.ID][]MediaUsageProjection, error)
	ReplyCounts(context.Context, []domain.ID) (map[domain.ID]int, error)
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
}

// ListRepliesInput selects one page of replies under a single root.
type ListRepliesInput struct {
	EntryID domain.ID
	RootID  domain.ID
	Cursor  string
	Limit   int
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
	AuthorKind       domain.CommentAuthorKind
	AuthorName       string
	// Author names the account writing this comment. It is supplied by the
	// caller because only the transport knows who was admitted; the service
	// refuses to write without it rather than falling back to the owner.
	Author         domain.User
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

// Create persists a comment written by an authenticated account.
//
// It is the only way to write a comment, and the author is the account the
// transport admitted rather than the site owner. Stamping the owner here was
// correct while the owner was the only account that could exist; the moment a
// second one could, it meant a member's words were published under someone
// else's name.
//
// AuthorKind stays 'owner' for every account. Its remaining job is marking rows
// a legacy guest wrote, and which account -- and what role it holds now -- is
// answered by AuthorUserID against users.
func (service *CommentService) Create(ctx context.Context, input CreateCommentInput) (CreateCommentResult, error) {
	if err := domain.ValidateUserID(input.Author.ID); err != nil {
		return CreateCommentResult{}, domain.NewError(domain.CodeUnauthorized, "a comment needs an authenticated author", nil)
	}
	if !input.Author.CanWritePublicContent() {
		return CreateCommentResult{}, domain.NewError(domain.CodeAccountNotApproved, "this account is waiting to be approved by the site owner", nil)
	}
	input.AuthorKind = domain.CommentAuthorOwner
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
		authorID := input.Author.ID
		comment := domain.Comment{ID: id, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), ReplyToCommentID: cloneCommentID(input.ReplyToCommentID), AuthorKind: input.AuthorKind, AuthorName: input.AuthorName, AuthorUserID: &authorID, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: now}
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
		exists, storeErr := store.EntryExists(ctx, input.EntryID)
		if storeErr != nil {
			return storeErr
		}
		if !exists {
			return postNotFound(input.EntryID)
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

// ListReplies returns one keyset page of replies under a single root.
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
	after, err := decodeCommentReplyCursor(input.Cursor, input.RootID)
	if err != nil {
		return CommentReplyPage{}, err
	}

	var page CommentReplyPage
	err = service.transactions.Read(ctx, func(store CommentStore) error {
		root, found, storeErr := store.CommentByID(ctx, input.RootID)
		if storeErr != nil {
			return storeErr
		}
		// The root has to belong to the post named in the request. Without
		// that check a caller could page any thread through any post's URL,
		// and the reply cursor alone would not catch it.
		if !found || root.EntryID != input.EntryID || root.ParentID != nil {
			return commentNotFound(input.RootID)
		}
		replies, storeErr := store.ListCommentReplies(ctx, input.RootID, after, limit+1)
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
			page.NextCursor = encodeCommentReplyCursor(input.RootID, CommentPosition{CreatedAtMS: domain.TimeToMillis(last.CreatedAt), ID: last.ID})
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
func (service *CommentService) List(ctx context.Context, entryID domain.ID) (CommentThread, error) {
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

// DeleteBy removes a comment on behalf of one account.
//
// The owner may remove anything -- moderating the site is theirs. Anybody else
// may remove only what they wrote, and only where removing it takes nothing of
// anybody else's with it; authorizeCommentRemoval holds that rule. Deciding it
// down there, where the stored row is in hand, is what keeps the transport from
// having to read the comment first and then make the same judgement with a
// staler copy of it.
func (service *CommentService) DeleteBy(ctx context.Context, id domain.ID, actor domain.User, dryRun bool) (DeleteCommentResult, error) {
	return service.delete(ctx, id, &actor, dryRun)
}

// Delete previews or executes owner moderation. SQLite owns reply cascade.
func (service *CommentService) Delete(ctx context.Context, id domain.ID, dryRun bool) (DeleteCommentResult, error) {
	return service.delete(ctx, id, nil, dryRun)
}

// delete does the work for both. A nil actor is the owner authority itself,
// which is what the CLI holds and what Delete offers.
//
// The judgement runs inside the same transaction as the removal rather than in
// a read before it. Read first and the reply that decides the answer can arrive
// between the two, so the check would pass against a conversation nobody had
// answered yet and the cascade would still take the answer with it.
func (service *CommentService) delete(ctx context.Context, id domain.ID, actor *domain.User, dryRun bool) (DeleteCommentResult, error) {
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

// authorizeCommentRemoval decides whether this actor may take this comment and
// everything the cascade removes with it.
//
// What somebody wrote has to be read as including what removing it takes away.
// parent_id cascades, so a root leaves with every reply under it; that cascade
// was moderation for as long as removing a comment was an owner-only route, and
// the day an approved member could reach it, deleting your own comment became a
// way to delete other people's. So a non-owner is refused whenever the cascade
// would reach a reply somebody else wrote. The preview is refused on the same
// rule rather than allowed through: it exists to report what the removal would
// take, and for a removal that cannot happen that is only a count of another
// person's replies.
func authorizeCommentRemoval(ctx context.Context, store CommentStore, record domain.Comment, actor *domain.User) error {
	if actor == nil || actor.IsOwner() {
		return nil
	}
	if record.AuthorUserID == nil || *record.AuthorUserID != actor.ID {
		return domain.NewError(domain.CodeUnauthorized, "only the author or the site owner can remove a comment", nil)
	}
	repliesByOthers, err := store.CountCommentRepliesByOthers(ctx, record.ID, actor.ID)
	if err != nil {
		return err
	}
	if repliesByOthers > 0 {
		return domain.NewError(domain.CodeUnauthorized, "removing this comment would remove replies other people wrote, which only the site owner can do", map[string]any{"comment_id": record.ID.String(), "replies_by_others": repliesByOthers})
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
	comment := domain.Comment{ID: record.ResponseResourceID, EntryID: input.EntryID, ParentID: cloneCommentID(input.ParentID), ReplyToCommentID: cloneCommentID(input.ReplyToCommentID), AuthorKind: input.AuthorKind, AuthorName: input.AuthorName, AuthorUserID: &replayAuthorID, ContentMD: input.ContentMD, Location: domain.CloneLocation(input.Location), CreatedAt: record.CreatedAt}
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

// resolveCommentAuthors maps every comment in the batch to the identity that
// should be rendered for it.
//
// The lookup is batched and keyed by account ID because that is the shape a
// real user table needs; today exactly one account can match. Owner rows
// written before author_user_id existed — anything restored from an older
// archive — have no reference of their own, so they fall back to the site
// owner, which is by definition who wrote them.
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
			ProfileURL: domain.ProfileURL(identity.Username),
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
