package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

// A body is a document rather than a line of text, so it arrives as a file or
// on stdin. The ceiling matches the one the domain holds bodies to.
const maxArticleBodyInputBytes = domain.MaxArticleBodyBytes

func registerArticles(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"article", "drafts"},
		Short:      "List unfinished articles",
		Long:       "Read every article draft this site keeps, newest first, through the selected local instance or the authenticated owner HTTP API.",
		Inputs:     "None.",
		Output:     "data is an array of drafts, each with its body and the revision a later save must name.",
		Errors:     "Authentication failures use stable machine codes.",
		Example:    "moment article drafts --data-dir ./moment-data --json",
		Capability: CapabilityBoth,
		Run:        runArticleDraftList,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"article", "draft"},
		ArgsUsage:  "<draft-id>",
		Short:      "Read one article draft",
		Long:       "Read one article draft, body included, through the selected local instance or the authenticated owner HTTP API.",
		Inputs:     "Provide one canonical draft ULID.",
		Output:     "The canonical draft DTO in direct and API modes.",
		Errors:     "Absent drafts return ARTICLE_DRAFT_NOT_FOUND.",
		Example:    "moment article draft 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run:        runArticleDraftGet,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:   []string{"article", "new"},
		Short:  "Start an article",
		Long:   "Create one article draft from a structured body. A draft may be untitled and may say nothing yet; publishing is where both stop being allowed.",
		Inputs: "Provide --body as a file path or - for stdin, holding one canonical article document. --title and --cover are optional.",
		Output: "The created draft, including the revision a later save must name.",
		Errors: "Body grammar violations name the node that broke the rule.",
		Example: "moment article new --data-dir ./moment-data --title '长安的荔枝' --body ./body.json --json\n" +
			"echo '{\"schema_version\":1,\"type\":\"doc\",\"content\":[]}' | moment article new --data-dir ./moment-data --body - --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("title", "", "article title, which may be empty while it is a draft")
			flags.String("body", "", "canonical article document path, or - for stdin")
			flags.String("cover", "", "managed media ULID to lead the article with")
		},
		Run: runArticleDraftCreate,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"article", "save"},
		ArgsUsage:  "<draft-id>",
		Short:      "Replace an article draft",
		Long:       "Replace a draft whole. The revision it was based on must be named, so a save that is behind is refused instead of erasing somebody else's words.",
		Inputs:     "Provide --body, --expected-updated-at from the draft you read, and optionally --title and --cover.",
		Output:     "The replaced draft with its new revision.",
		Errors:     "A stale revision returns ARTICLE_DRAFT_STALE.",
		Example:    "moment article save 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --body ./body.json --expected-updated-at 2026-09-25T10:00:00Z --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("title", "", "article title")
			flags.String("body", "", "canonical article document path, or - for stdin")
			flags.String("cover", "", "managed media ULID to lead the article with")
			flags.String("expected-updated-at", "", "the revision this save is based on, as returned by a read")
		},
		Run: runArticleDraftUpdate,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"article", "discard"},
		ArgsUsage: "<draft-id>",
		Short:     "Discard an article draft",
		Long:      "Throw away one unfinished article. The words are not recoverable afterwards.",
		Safety:    "Destructive. --yes confirms intent; an unfinished article exists only here and cannot be recovered afterwards.",
		Policy: SafetyPolicy{
			Destructive:  true,
			Confirmation: "Discard this unfinished article permanently?",
		},
		Inputs:     "Provide one canonical draft ULID.",
		Output:     "The discarded id.",
		Errors:     "Absent drafts return ARTICLE_DRAFT_NOT_FOUND.",
		Example:    "moment article discard 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run:        runArticleDraftDelete,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"article", "publish"},
		ArgsUsage:  "<draft-id>",
		Short:      "Publish an article draft",
		Long:       "Put a draft on the timeline, or replace the article it is a working copy of, and throw the draft away once it has landed.",
		Inputs:     "Provide one canonical draft ULID. --visibility everyone|only_me chooses who may read it; --idempotency-key makes a retry safe.",
		Output:     "The published post, body and rendering included.",
		Errors:     "An empty or untitled draft is refused; a reused key for a different draft returns IDEMPOTENCY_KEY_REUSED.",
		Example:    "moment article publish 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --idempotency-key article-001 --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			registerPostVisibilityFlag(flags)
			flags.String("idempotency-key", "", "retry key forwarded unchanged to the application service")
		},
		Run: runArticlePublish,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"article", "rebuild"},
		Short:      "Repair the values derived from article bodies",
		Long:       "Re-derive the excerpt a card shows and the picture projection reclamation counts from each article's stored body, without touching the article itself or its timestamps.",
		Inputs:     "A local data directory. Use --dry-run for a read-only count; API mode is rejected as MODE_UNSUPPORTED.",
		Output:     "Total, drifted, rebuilt and unparsable article counts.",
		Errors:     "A body that no longer parses is counted and left alone; it is repaired by restoring that article from a backup.",
		Safety:     "Only derived copies are rewritten. Bodies, titles, covers, visibility and timestamps are left exactly as they are, so a repaired article is not marked edited.",
		Example:    "moment article rebuild --data-dir ./moment-data --dry-run --json",
		Capability: CapabilityDirectOnly,
		Policy:     SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: "Re-derive the stored article excerpts and picture projections now?"},
		Run:        runArticleRebuild,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"article", "edit"},
		ArgsUsage:  "<post-id>",
		Short:      "Open a published article for editing",
		Long:       "Open the working copy of a published article, reusing the one that already exists. Saving the copy changes nothing a reader sees until it is published in turn.",
		Inputs:     "Provide the canonical post ULID of a published article.",
		Output:     "The working copy, which names the article it belongs to.",
		Errors:     "A post that is not an article returns INVALID_ARGUMENT; an absent one returns POST_NOT_FOUND.",
		Example:    "moment article edit 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run:        runArticleEdit,
	})
}

func runArticleDraftList(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	switch invocation.Mode() {
	case ModeDirect:
		service, _, database, err := openDirectPostRead(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = database.Close() }()
		records, err := service.ArticleDrafts(ctx)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return articleDraftListResult(postdto.FromArticleDrafts(records), nil), nil
	case ModeAPI:
		var drafts []postdto.ArticleDraft
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/owner/articles/drafts", nil, nil, "", true, &drafts)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		return articleDraftListResult(drafts, meta), nil
	default:
		return Result{}, errors.New("unsupported article draft list transport")
	}
}

func runArticleDraftGet(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	switch invocation.Mode() {
	case ModeDirect:
		service, _, database, err := openDirectPostRead(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = database.Close() }()
		record, err := service.ArticleDraft(ctx, id)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return articleDraftResult("Draft", postdto.FromArticleDraft(record), nil), nil
	case ModeAPI:
		var draft postdto.ArticleDraft
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/owner/articles/drafts/"+id.String(), nil, nil, "", true, &draft)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		return articleDraftResult("Draft", draft, meta), nil
	default:
		return Result{}, errors.New("unsupported article draft get transport")
	}
}

func runArticleDraftCreate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	payload, err := articleDraftPayload(ctx, input)
	if err != nil {
		return Result{}, err
	}
	switch invocation.Mode() {
	case ModeDirect:
		runtime, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = runtime.Close() }()
		saveInput, err := payload.serviceInput(nil)
		if err != nil {
			return Result{}, err
		}
		record, err := runtime.service.SaveArticleDraft(ctx, saveInput)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return articleDraftResult("Created draft", postdto.FromArticleDraft(record), nil), nil
	case ModeAPI:
		var draft postdto.ArticleDraft
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/owner/articles/drafts", nil, payload.wire(), "", true, &draft)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusCreated {
			return Result{}, invalidAPIResponse()
		}
		return articleDraftResult("Created draft", draft, meta), nil
	default:
		return Result{}, errors.New("unsupported article draft create transport")
	}
}

func runArticleDraftUpdate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	payload, err := articleDraftPayload(ctx, input)
	if err != nil {
		return Result{}, err
	}
	expected, err := input.String("expected-updated-at")
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(expected) == "" {
		return Result{}, invalidArgument("--expected-updated-at is required", "pass the updated_at value from the draft you read")
	}
	instant, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(expected))
	if parseErr != nil {
		return Result{}, invalidArgument("--expected-updated-at must be an RFC 3339 instant", "copy updated_at from the draft you read")
	}
	payload.ExpectedUpdatedAt = &instant

	switch invocation.Mode() {
	case ModeDirect:
		runtime, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = runtime.Close() }()
		saveInput, err := payload.serviceInput(&id)
		if err != nil {
			return Result{}, err
		}
		record, err := runtime.service.SaveArticleDraft(ctx, saveInput)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return articleDraftResult("Updated draft", postdto.FromArticleDraft(record), nil), nil
	case ModeAPI:
		var draft postdto.ArticleDraft
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodPut, "/api/v1/owner/articles/drafts/"+id.String(), nil, payload.wire(), "", true, &draft)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		return articleDraftResult("Updated draft", draft, meta), nil
	default:
		return Result{}, errors.New("unsupported article draft update transport")
	}
}

func runArticleDraftDelete(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	switch invocation.Mode() {
	case ModeDirect:
		runtime, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = runtime.Close() }()
		if err := runtime.service.DiscardArticleDraft(ctx, id); err != nil {
			return Result{}, postCLIError(err)
		}
		return articleDiscardResult(id, nil), nil
	case ModeAPI:
		var discarded articleDiscardResponse
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodDelete, "/api/v1/owner/articles/drafts/"+id.String(), nil, nil, "", true, &discarded)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK || discarded.ID != id.String() {
			return Result{}, invalidAPIResponse()
		}
		return articleDiscardResult(id, meta), nil
	default:
		return Result{}, errors.New("unsupported article draft delete transport")
	}
}

func runArticlePublish(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	requested, err := postVisibilityFromFlags(input)
	if err != nil {
		return Result{}, err
	}
	var visibility domain.Visibility
	if requested != nil {
		visibility = *requested
	}
	key, err := input.String("idempotency-key")
	if err != nil {
		return Result{}, err
	}
	switch invocation.Mode() {
	case ModeDirect:
		runtime, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = runtime.Close() }()
		post, err := runtime.service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{
			DraftID: id, Visibility: visibility, IdempotencyKey: key,
		})
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return postResult("Published article", postdto.FromPost(post), nil), nil
	case ModeAPI:
		var post postdto.Post
		payload := map[string]any{}
		if visibility != "" {
			payload["visibility"] = string(visibility)
		}
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodPost,
			"/api/v1/owner/articles/drafts/"+id.String()+"/publish", nil, payload, key, true, &post)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusCreated {
			return Result{}, invalidAPIResponse()
		}
		return postResult("Published article", post, meta), nil
	default:
		return Result{}, errors.New("unsupported article publish transport")
	}
}

func runArticleEdit(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	switch invocation.Mode() {
	case ModeDirect:
		runtime, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = runtime.Close() }()
		record, err := runtime.service.EditArticle(ctx, id)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return articleDraftResult("Working copy", postdto.FromArticleDraft(record), nil), nil
	case ModeAPI:
		var draft postdto.ArticleDraft
		meta, status, err := postAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/owner/articles/"+id.String()+"/draft", nil, nil, "", true, &draft)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		return articleDraftResult("Working copy", draft, meta), nil
	default:
		return Result{}, errors.New("unsupported article edit transport")
	}
}

type articleDraftInput struct {
	Title             string
	Body              []byte
	CoverMediaID      *string
	ExpectedUpdatedAt *time.Time
}

type articleDiscardResponse struct {
	ID        string `json:"id"`
	Discarded bool   `json:"discarded"`
}

func validArticleResponseID(value string) bool {
	id, err := domain.ParseID(value)
	return err == nil && id.String() == value
}

// Draft responses have their own contract: empty bodies are legal, and the
// revision must retain the exact canonical spelling needed by the next save.
func validRawAPIArticleDraft(data json.RawMessage, draft postdto.ArticleDraft) bool {
	if !hasRequiredNonNullAPIDataFields(data, "id", "title", "body", "body_schema_version", "created_at", "updated_at") ||
		!hasRequiredAPIDataFields(data, "cover_media_id", "published_entry_id") ||
		!validArticleResponseID(draft.ID) ||
		!validCanonicalAPIInstantField(data, "created_at", draft.CreatedAt) ||
		!validCanonicalAPIInstantField(data, "updated_at", draft.UpdatedAt) {
		return false
	}
	var coverID *domain.ID
	if draft.CoverMediaID != nil {
		if !validArticleResponseID(*draft.CoverMediaID) {
			return false
		}
		id, _ := domain.ParseID(*draft.CoverMediaID)
		coverID = &id
	}
	if draft.PublishedEntryID != nil && !validArticleResponseID(*draft.PublishedEntryID) {
		return false
	}
	body, err := domain.ParseArticleDraftDocument(draft.Body)
	if err != nil || body.SchemaVersion != draft.BodySchemaVersion {
		return false
	}
	_, err = domain.NewArticleDraft(draft.Title, body, coverID)
	return err == nil
}

func (payload articleDraftInput) wire() map[string]any {
	body := map[string]any{"title": payload.Title, "body": json.RawMessage(payload.Body)}
	if payload.CoverMediaID != nil {
		body["cover_media_id"] = *payload.CoverMediaID
	}
	if payload.ExpectedUpdatedAt != nil {
		body["expected_updated_at"] = payload.ExpectedUpdatedAt.UTC()
	}
	return body
}

func (payload articleDraftInput) serviceInput(id *domain.ID) (app.SaveArticleDraftInput, error) {
	input := app.SaveArticleDraftInput{
		ID: id, Title: payload.Title, BodyJSON: payload.Body, ExpectedUpdatedAt: payload.ExpectedUpdatedAt,
	}
	if payload.CoverMediaID != nil {
		cover, err := domain.ParseID(*payload.CoverMediaID)
		if err != nil {
			return app.SaveArticleDraftInput{}, invalidArgument("--cover must be a managed media ULID", "pass an id returned by a media upload")
		}
		input.CoverMediaID = &cover
	}
	return input, nil
}

func articleDraftPayload(ctx context.Context, input CommandInput) (articleDraftInput, error) {
	title, err := input.String("title")
	if err != nil {
		return articleDraftInput{}, err
	}
	path, err := input.String("body")
	if err != nil {
		return articleDraftInput{}, err
	}
	if strings.TrimSpace(path) == "" {
		return articleDraftInput{}, invalidArgument("--body is required", "pass a canonical article document path or - for stdin")
	}
	body, err := readBoundedArticleBody(ctx, input.In(), path)
	if err != nil {
		return articleDraftInput{}, err
	}
	payload := articleDraftInput{Title: title, Body: body}
	cover, err := input.String("cover")
	if err != nil {
		return articleDraftInput{}, err
	}
	if strings.TrimSpace(cover) != "" {
		trimmed := strings.TrimSpace(cover)
		payload.CoverMediaID = &trimmed
	}
	return payload, nil
}

// readBoundedArticleBody mirrors the location reader: a regular file that does
// not change under it, or stdin, and never more than the domain would accept.
func readBoundedArticleBody(ctx context.Context, stdin io.Reader, path string) ([]byte, error) {
	var reader io.Reader
	if path == "-" {
		if stdin == nil {
			return nil, invalidArgument("stdin is unavailable for --body -", "pipe one canonical article document to stdin")
		}
		reader = stdin
	} else {
		clean := filepath.Clean(path)
		info, err := os.Lstat(clean)
		if err != nil {
			return nil, postCLIError(err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxArticleBodyInputBytes {
			return nil, invalidArgument("--body must be a regular file no larger than 512 KiB", "provide a stable canonical JSON file or use - for stdin")
		}
		file, err := os.Open(clean)
		if err != nil {
			return nil, postCLIError(err)
		}
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			_ = file.Close()
			return nil, invalidArgument("--body changed while it was opened", "retry with a stable regular file")
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(cliContextReader{ctx: ctx, source: reader}, maxArticleBodyInputBytes+1))
	if err != nil {
		return nil, postCLIError(err)
	}
	if len(data) > maxArticleBodyInputBytes || !utf8.Valid(data) {
		return nil, invalidArgument("--body must be valid UTF-8 JSON no larger than 512 KiB", "provide one canonical article document")
	}
	return data, nil
}

func articleDraftResult(action string, draft postdto.ArticleDraft, meta map[string]any) Result {
	title := draft.Title
	if title == "" {
		title = "(untitled)"
	}
	return Result{
		Data: draft,
		Text: action + " " + draft.ID + ".\nTitle: " + title + "\nRevision: " + draft.UpdatedAt.Format(time.RFC3339Nano),
		Meta: meta,
	}
}

func articleDraftListResult(drafts []postdto.ArticleDraft, meta map[string]any) Result {
	if len(drafts) == 0 {
		return Result{Data: drafts, Text: "No article drafts found", Meta: meta}
	}
	lines := make([]string, 0, len(drafts))
	for _, draft := range drafts {
		title := draft.Title
		if title == "" {
			title = "(untitled)"
		}
		lines = append(lines, draft.ID+"  "+draft.UpdatedAt.Format(time.RFC3339)+"  "+title)
	}
	return Result{Data: drafts, Text: strings.Join(lines, "\n"), Meta: meta}
}

func articleDiscardResult(id domain.ID, meta map[string]any) Result {
	return Result{
		Data: map[string]any{"id": id.String(), "discarded": true},
		Text: "Discarded draft " + id.String() + ".",
		Meta: meta,
	}
}

// runArticleRebuild repairs the copies an article's body is the authority for.
//
// It is deliberately not "open each article and publish it again": publishing
// moves updated_at and sets edited_at, which would tell every reader the author
// revised an article that in fact only had a derived value corrected.
func runArticleRebuild(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	// CapabilityDirectOnly already refuses API mode in the kernel, and both
	// helpers below resolve a direct transport, so there is no second check to
	// write here.
	dryRun := invocation.Safety().DryRun
	if dryRun {
		service, _, database, err := openDirectPostRead(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = database.Close() }()
		result, err := service.RebuildArticleDerivations(ctx, true)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return articleRebuildResult(result), nil
	}
	runtime, err := openDirectPostWrite(ctx, invocation)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = runtime.Close() }()
	result, err := runtime.service.RebuildArticleDerivations(ctx, false)
	if err != nil {
		return Result{}, postCLIError(err)
	}
	return articleRebuildResult(result), nil
}

func articleRebuildResult(result app.ArticleRebuildResult) Result {
	verb := "Articles inspected"
	if !result.DryRun {
		verb = "Articles rebuilt"
	}
	return Result{Data: result, Text: fmt.Sprintf("%s: %d total, %d drifted, %d rebuilt, %d unreadable",
		verb, result.Total, result.Drifted, result.Rebuilt, result.Unparsable)}
}
