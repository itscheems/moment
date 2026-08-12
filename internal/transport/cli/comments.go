package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/moment/moment/internal/transport/commentdto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerComments(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"comment", "list"}, ArgsUsage: "<post-id>", Short: "List one comment thread",
		Long:    "Read one stable flattened one-level thread through the selected local instance or public HTTP API.",
		Inputs:  "Provide one canonical post ULID. Public API reads do not require an owner token.",
		Output:  "An ordered data.items array with roots followed immediately by their direct replies.",
		Errors:  "Invalid IDs and missing posts return stable codes and non-zero exit statuses.",
		Example: "moment comment list 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json",
		Args:    cobra.ExactArgs(1), Capability: CapabilityBoth, Run: runCommentList,
	})
	application.MustRegisterLeaf(commentCreateSpec("create", "Create an owner comment", "Create one top-level owner comment through the application service or authenticated owner API.", "moment comment create 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --content 'owner note' --json", runCommentCreate))
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"comment", "reply"}, ArgsUsage: "<post-id> <parent-comment-id>", Short: "Reply as the owner",
		Long:    "Create one owner reply to a top-level comment through the shared one-level comment service.",
		Inputs:  "Provide a post ULID, top-level parent comment ULID, --content, and optional --idempotency-key. CLI mutations always use owner identity.",
		Output:  "The canonical created owner comment and replay metadata.",
		Errors:  "Missing/deleted parents, depth violations, disabled comments, and authentication failures use stable codes.",
		Example: "moment comment reply 01ARZ3NDEKTSV4RRFFQ69G5FAV 01ARZ3NDEKTSV4RRFFQ69G5FAW --data-dir ./moment-data --content 'thanks' --json",
		Args:    cobra.ExactArgs(2), Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("content", "", "authoritative UTF-8 Markdown content")
			flags.String("idempotency-key", "", "retry key forwarded unchanged to the application service")
			registerPostMediaFlags(flags)
		},
		Run: runCommentReply,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"comment", "delete"}, ArgsUsage: "<comment-id>", Short: "Delete a comment as the owner",
		Long:    "Preview or delete one comment. Deleting a top-level comment atomically cascades its direct replies.",
		Inputs:  "Provide one comment ULID. Use --dry-run to inspect the exact cascade count or --yes for non-interactive deletion.",
		Output:  "The canonical target comment plus deleted, dry_run, and replies_affected metadata.",
		Errors:  "Missing comments, declined confirmation, cancellation, and authentication failures use stable codes.",
		Safety:  "Destructive. --yes confirms intent only; --dry-run leaves the comment thread unchanged.",
		Example: "moment comment delete 01ARZ3NDEKTSV4RRFFQ69G5FAW --data-dir ./moment-data --dry-run --json",
		Args:    cobra.ExactArgs(1), Capability: CapabilityBoth,
		Policy: SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: "Delete this comment and any direct replies permanently?"},
		Run:    runCommentDelete,
	})
}

func commentCreateSpec(verb, short, long, example string, run Handler) LeafSpec {
	return LeafSpec{
		Path: []string{"comment", verb}, ArgsUsage: "<post-id>", Short: short, Long: long,
		Inputs:  "Provide one post ULID, --content, and optional --idempotency-key. CLI mutations always create owner comments and never accept guest names or client IPs.",
		Output:  "The canonical created owner comment and replay metadata.",
		Errors:  "Content validation, disabled comments, missing posts, authentication, and idempotency conflicts use stable codes.",
		Example: example, Args: cobra.ExactArgs(1), Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("content", "", "authoritative UTF-8 Markdown content")
			flags.String("idempotency-key", "", "retry key forwarded unchanged to the application service")
			registerPostMediaFlags(flags)
		},
		Run: run,
	}
}

func runCommentList(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	postID, err := domain.ParseID(input.Args()[0])
	if err != nil {
		return Result{}, commentCLIError(err)
	}
	var thread commentdto.Thread
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		service, database, err := openDirectCommentRead(ctx, invocation)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		defer func() { _ = database.Close() }()
		result, err := service.List(ctx, postID)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		thread = commentdto.FromThread(result)
	case ModeAPI:
		var status int
		meta, status, err = commentAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/posts/"+postID.String()+"/comments", nil, nil, "", false, &thread)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
	default:
		return Result{}, errors.New("unsupported comment list transport")
	}
	return Result{Data: thread, Text: commentThreadText(thread), Meta: meta}, nil
}

func runCommentCreate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	return runOwnerCommentCreate(ctx, invocation, input, nil)
}

func runCommentReply(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	parentID, err := domain.ParseID(input.Args()[1])
	if err != nil {
		return Result{}, commentCLIError(err)
	}
	return runOwnerCommentCreate(ctx, invocation, input, &parentID)
}

func runOwnerCommentCreate(ctx context.Context, invocation Invocation, input CommandInput, parentID *domain.ID) (Result, error) {
	postID, err := domain.ParseID(input.Args()[0])
	if err != nil {
		return Result{}, commentCLIError(err)
	}
	if !input.Changed("content") {
		return Result{}, invalidArgument("--content is required", "provide the comment Markdown with --content")
	}
	content, err := input.String("content")
	if err != nil {
		return Result{}, err
	}
	idempotencyKey, err := input.String("idempotency-key")
	if err != nil {
		return Result{}, err
	}
	mediaInput, err := postMediaInputFromFlags(ctx, input)
	if err != nil {
		return Result{}, err
	}
	if len(mediaInput.Items) > domain.MaxCommentMediaUsages {
		return Result{}, invalidArgument("comment media may contain at most 4 items", "remove media until positions fit 0 through 3")
	}
	var comment commentdto.Comment
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		direct, err := openDirectCommentWrite(ctx, invocation)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		usages, err := resolveCommentMediaInput(mediaInput, func(path string) (domain.ID, error) {
			return ingestDirectPostMedia(ctx, direct.media, path)
		})
		if err != nil {
			return Result{}, err
		}
		result, err := direct.service.CreateOwner(ctx, app.CreateCommentInput{EntryID: postID, ParentID: parentID, ContentMD: content, Media: usages, IdempotencyKey: idempotencyKey})
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		comment = commentdto.FromComment(result.Comment)
		meta = map[string]any{"replayed": result.Replayed}
	case ModeAPI:
		usages, err := resolveCommentMediaInput(mediaInput, func(path string) (domain.ID, error) {
			return ingestAPIPostMedia(ctx, invocation, path)
		})
		if err != nil {
			return Result{}, err
		}
		payload := map[string]any{"content_md": content, "media": postMediaDTOs(usages)}
		if parentID != nil {
			payload["parent_id"] = parentID.String()
		}
		var status int
		meta, status, err = commentAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/owner/posts/"+postID.String()+"/comments", nil, payload, idempotencyKey, true, &comment)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		replayed, ok := meta["replayed"].(bool)
		if !ok || (status == http.StatusCreated && replayed) || (status == http.StatusOK && !replayed) || (status != http.StatusCreated && status != http.StatusOK) {
			return Result{}, invalidAPIResponse()
		}
	default:
		return Result{}, errors.New("unsupported comment create transport")
	}
	return commentResult("Comment created", comment, meta), nil
}

func runCommentDelete(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := domain.ParseID(input.Args()[0])
	if err != nil {
		return Result{}, commentCLIError(err)
	}
	var comment commentdto.Comment
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		if invocation.Safety().DryRun {
			service, database, err := openDirectCommentRead(ctx, invocation)
			if err != nil {
				return Result{}, commentCLIError(err)
			}
			defer func() { _ = database.Close() }()
			result, err := service.Delete(ctx, id, true)
			if err != nil {
				return Result{}, commentCLIError(err)
			}
			comment = commentdto.FromComment(result.Comment)
			meta = commentDeleteMeta(result)
		} else {
			direct, err := openDirectCommentWrite(ctx, invocation)
			if err != nil {
				return Result{}, commentCLIError(err)
			}
			defer func() { _ = direct.Close() }()
			result, err := direct.service.Delete(ctx, id, false)
			if err != nil {
				return Result{}, commentCLIError(err)
			}
			comment = commentdto.FromComment(result.Comment)
			meta = commentDeleteMeta(result)
		}
	case ModeAPI:
		query := url.Values{}
		if invocation.Safety().DryRun {
			query.Set("dry_run", "true")
		}
		var status int
		meta, status, err = commentAPIRequest(ctx, invocation, http.MethodDelete, "/api/v1/owner/comments/"+id.String(), query, nil, "", true, &comment)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		deleted, deletedOK := meta["deleted"].(bool)
		dryRun, dryRunOK := meta["dry_run"].(bool)
		_, repliesOK := meta["replies_affected"].(float64)
		if status != http.StatusOK || !deletedOK || !dryRunOK || !repliesOK || deleted == dryRun || dryRun != invocation.Safety().DryRun {
			return Result{}, invalidAPIResponse()
		}
	default:
		return Result{}, errors.New("unsupported comment delete transport")
	}
	action := "Comment deleted"
	if invocation.Safety().DryRun {
		action = "Would delete comment"
	}
	return commentResult(action, comment, meta), nil
}

func commentDeleteMeta(result app.DeleteCommentResult) map[string]any {
	return map[string]any{"deleted": result.Deleted, "dry_run": result.DryRun, "replies_affected": result.RepliesAffected}
}

func openDirectCommentRead(ctx context.Context, invocation Invocation) (*app.CommentService, *sqlitestore.Database, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return nil, nil, err
	}
	runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, nil, ownerRuntimeError(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return nil, nil, postDatabaseError(err)
	}
	repository, err := sqlitestore.NewCommentRepository(database, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	service, err := app.NewCommentService(repository)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	return service, database, nil
}

type directCommentRuntime struct {
	service     *app.CommentService
	media       *media.Store
	database    *sqlitestore.Database
	maintenance *runtimefs.Lease
}

func (runtime *directCommentRuntime) Close() error {
	return errors.Join(runtime.database.Close(), runtime.maintenance.Release())
}

func openDirectCommentWrite(ctx context.Context, invocation Invocation) (*directCommentRuntime, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return nil, err
	}
	runtimeState, maintenance, err := runtimefs.OpenForWriteContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, ownerRuntimeError(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		_ = maintenance.Release()
		return nil, postDatabaseError(err)
	}
	repository, err := sqlitestore.NewCommentRepositoryWithinMaintenance(database, maintenance, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	service, err := app.NewCommentService(repository)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	mediaStore, err := media.NewStoreWithinMaintenance(runtimeState, database, media.Options{}, maintenance)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	return &directCommentRuntime{service: service, media: mediaStore, database: database, maintenance: maintenance}, nil
}

func resolveCommentMediaInput(input postMediaInput, upload func(string) (domain.ID, error)) ([]domain.EntryMediaUsage, error) {
	usages, err := resolvePostMediaInput(input, upload)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return nil, commentCLIError(err)
	}
	return usages, nil
}

func commentAPIRequest(ctx context.Context, invocation Invocation, method, endpoint string, query url.Values, payload any, idempotencyKey string, authenticate bool, destination any) (map[string]any, int, error) {
	meta, status, err := postAPIRequest(ctx, invocation, method, endpoint, query, payload, idempotencyKey, authenticate, destination)
	if err != nil {
		return nil, status, err
	}
	switch typed := destination.(type) {
	case *commentdto.Comment:
		if err := commentdto.ValidateComment(*typed); err != nil {
			return nil, status, invalidAPIResponse()
		}
	case *commentdto.Thread:
		if err := commentdto.ValidateThread(*typed); err != nil {
			return nil, status, invalidAPIResponse()
		}
	}
	return meta, status, nil
}

func commentResult(action string, comment commentdto.Comment, meta map[string]any) Result {
	return Result{Data: comment, Text: fmt.Sprintf("%s %s.\nAuthor: %s (%s)\n%s", action, comment.ID, comment.AuthorName, comment.AuthorKind, comment.ContentMD), Meta: meta}
}

func commentThreadText(thread commentdto.Thread) string {
	if len(thread.Items) == 0 {
		return "No comments found"
	}
	lines := make([]string, 0, len(thread.Items))
	for _, comment := range thread.Items {
		prefix := ""
		if comment.Depth == 1 {
			prefix = "  ↳ "
		}
		content := strings.ReplaceAll(comment.ContentMD, "\n", " ")
		if len([]rune(content)) > 80 {
			content = string([]rune(content)[:80]) + "…"
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s  %s", prefix, comment.ID, comment.AuthorName, content))
	}
	return strings.Join(lines, "\n")
}

func commentCLIError(err error) error {
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return err
	}
	return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
}
