package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/moment/moment/internal/transport/postdto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const maxPostAPIResponse = 64 << 20

func registerPosts(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "list"},
		Short:      "List the public Moment timeline",
		Long:       "Read one stable reverse-chronological page through the selected local instance or HTTP API.",
		Inputs:     "Use --cursor for continuation, --limit from 1 to 100, and optional YYYY-MM-DD boundaries: inclusive --from and exclusive --to in the configured site timezone.",
		Output:     "A canonical post array in data.items and nullable meta.next_cursor; internal Entry kind is never exposed.",
		Errors:     "Invalid cursors, limits, dates, or site authority return stable machine codes.",
		Example:    "moment post list --data-dir ./moment-data --limit 20 --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("cursor", "", "opaque continuation cursor from a prior page")
			flags.Int("limit", 0, "page size from 1 to 100 (default 20)")
			flags.String("from", "", "inclusive local date in YYYY-MM-DD format")
			flags.String("to", "", "exclusive local date in YYYY-MM-DD format")
		},
		Run: runPostList,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "get"},
		ArgsUsage:  "<post-id>",
		Short:      "Get one Moment post",
		Long:       "Read one complete plain-text post through the selected local instance or public HTTP API.",
		Inputs:     "Provide one canonical post ULID.",
		Output:     "The same canonical post DTO in direct and API modes.",
		Errors:     "Invalid IDs return INVALID_ARGUMENT and absent posts return POST_NOT_FOUND.",
		Example:    "moment post get 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run:        runPostGet,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "create"},
		Short:      "Publish a Moment post",
		Long:       "Publish one Markdown post through the shared application service in direct mode or the authenticated owner HTTP API.",
		Inputs:     "Provide --content. Optional --idempotency-key makes retries return the original create result; API mode reads MOMENT_TOKEN or --token-file.",
		Output:     "The canonical created post and replay metadata.",
		Errors:     "Content validation, authentication, and idempotency conflicts use stable codes and exit statuses.",
		Example:    "moment post create --data-dir ./moment-data --content 'hello **moment**' --idempotency-key demo-001 --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("content", "", "authoritative UTF-8 Markdown content")
			flags.String("idempotency-key", "", "retry key forwarded unchanged to the application service")
		},
		Run: runPostCreate,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "update"},
		ArgsUsage:  "<post-id>",
		Short:      "Update a Moment post",
		Long:       "Replace one post's authoritative Markdown while preserving its creation and publication position.",
		Inputs:     "Provide one post ULID and an explicit --content value. API mode reads MOMENT_TOKEN or --token-file.",
		Output:     "The complete updated canonical post DTO.",
		Errors:     "Invalid content, missing posts, and authentication failures use stable codes and exit statuses.",
		Example:    "moment post update 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --content 'updated' --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("content", "", "replacement UTF-8 Markdown content")
		},
		Run: runPostUpdate,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "delete"},
		ArgsUsage:  "<post-id>",
		Short:      "Delete a Moment post",
		Long:       "Delete one post through the shared application service, including its transactional dependent references.",
		Inputs:     "Provide one post ULID. Use --dry-run to inspect the exact post without mutation or --yes for non-interactive deletion.",
		Output:     "The canonical post that would be or was deleted, with dry_run or deleted metadata.",
		Errors:     "Missing posts, declined confirmation, and authentication failures use stable codes and exit statuses.",
		Safety:     "Destructive. --yes confirms intent only; --dry-run performs a read and leaves authority unchanged.",
		Example:    "moment post delete 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --dry-run --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Policy: SafetyPolicy{
			Destructive:    true,
			SupportsDryRun: true,
			Confirmation:   "Delete this Moment post permanently?",
		},
		Run: runPostDelete,
	})
}

func runPostList(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	listInput, err := postListFlags(input)
	if err != nil {
		return Result{}, err
	}
	var (
		page       postdto.Page
		meta       map[string]any
		nextCursor *string
	)
	switch invocation.Mode() {
	case ModeDirect:
		service, site, database, err := openDirectPostRead(ctx, invocation)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		defer func() { _ = database.Close() }()
		settings, err := site.Get(ctx)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		listInput.Timezone = settings.Timezone
		result, err := service.List(ctx, listInput)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		page = postdto.FromPage(result)
		nextCursor = postdto.NextCursor(result)
		meta = map[string]any{"next_cursor": nextCursor}
	case ModeAPI:
		query := url.Values{}
		if listInput.Cursor != "" {
			query.Set("cursor", listInput.Cursor)
		}
		if listInput.Limit != 0 {
			query.Set("limit", strconv.Itoa(listInput.Limit))
		}
		if listInput.FromDate != "" {
			query.Set("from", listInput.FromDate)
		}
		if listInput.ToDate != "" {
			query.Set("to", listInput.ToDate)
		}
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/posts", query, nil, "", false, &page)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		nextCursor, err = postCursorFromMeta(meta)
		if err != nil {
			return Result{}, err
		}
	default:
		return Result{}, errors.New("unsupported post list transport")
	}
	return Result{Data: page, Text: postPageText(page, nextCursor), Meta: meta}, nil
}

func runPostGet(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	post, meta, err := getPostByTransport(ctx, invocation, id)
	if err != nil {
		return Result{}, postCLIError(err)
	}
	return postResult("Post", post, meta), nil
}

func runPostCreate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	content, err := requiredContentFlag(input)
	if err != nil {
		return Result{}, err
	}
	idempotencyKey, err := input.String("idempotency-key")
	if err != nil {
		return Result{}, err
	}
	var (
		post postdto.Post
		meta map[string]any
	)
	switch invocation.Mode() {
	case ModeDirect:
		direct, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		created, err := direct.service.Create(ctx, app.CreatePostInput{ContentMD: content, IdempotencyKey: idempotencyKey})
		if err != nil {
			return Result{}, postCLIError(err)
		}
		post = postdto.FromPost(created.Post)
		meta = map[string]any{"replayed": created.Replayed}
	case ModeAPI:
		payload := map[string]string{"content_md": content}
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/owner/posts", nil, payload, idempotencyKey, true, &post)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		replayed, ok := meta["replayed"].(bool)
		if !ok || (status == http.StatusCreated && replayed) || (status == http.StatusOK && !replayed) ||
			(status != http.StatusCreated && status != http.StatusOK) {
			return Result{}, invalidAPIResponse()
		}
	default:
		return Result{}, errors.New("unsupported post create transport")
	}
	return postResult("Post published", post, meta), nil
}

func runPostUpdate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	content, err := requiredContentFlag(input)
	if err != nil {
		return Result{}, err
	}
	var (
		post postdto.Post
		meta map[string]any
	)
	switch invocation.Mode() {
	case ModeDirect:
		direct, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		updated, err := direct.service.Update(ctx, app.UpdatePostInput{ID: id, ContentMD: content})
		if err != nil {
			return Result{}, postCLIError(err)
		}
		post = postdto.FromPost(updated)
	case ModeAPI:
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodPatch, "/api/v1/owner/posts/"+id.String(), nil, map[string]string{"content_md": content}, "", true, &post)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
	default:
		return Result{}, errors.New("unsupported post update transport")
	}
	return postResult("Post updated", post, meta), nil
}

func runPostDelete(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	if invocation.Safety().DryRun {
		post, meta, err := getPostByTransport(ctx, invocation, id)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		if meta == nil {
			meta = map[string]any{}
		}
		meta["dry_run"] = true
		return postResult("Would delete post", post, meta), nil
	}
	var (
		post postdto.Post
		meta map[string]any
	)
	switch invocation.Mode() {
	case ModeDirect:
		direct, err := openDirectPostWrite(ctx, invocation)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		deleted, err := direct.service.Delete(ctx, id)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		post = postdto.FromPost(deleted)
		meta = map[string]any{"deleted": true}
	case ModeAPI:
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodDelete, "/api/v1/owner/posts/"+id.String(), nil, nil, "", true, &post)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		deleted, ok := meta["deleted"].(bool)
		if status != http.StatusOK || !ok || !deleted {
			return Result{}, invalidAPIResponse()
		}
	default:
		return Result{}, errors.New("unsupported post delete transport")
	}
	return postResult("Post deleted", post, meta), nil
}

func postListFlags(input CommandInput) (app.ListPostsInput, error) {
	cursor, err := input.String("cursor")
	if err != nil {
		return app.ListPostsInput{}, err
	}
	limit, err := input.Int("limit")
	if err != nil {
		return app.ListPostsInput{}, err
	}
	if input.Changed("limit") && limit == 0 {
		return app.ListPostsInput{}, invalidArgument("--limit must be between 1 and 100", "provide a positive page size no greater than 100")
	}
	fromDate, err := input.String("from")
	if err != nil {
		return app.ListPostsInput{}, err
	}
	toDate, err := input.String("to")
	if err != nil {
		return app.ListPostsInput{}, err
	}
	if input.Changed("from") && fromDate == "" {
		return app.ListPostsInput{}, invalidArgument("--from cannot be empty", "provide an inclusive date in YYYY-MM-DD format or remove --from")
	}
	if input.Changed("to") && toDate == "" {
		return app.ListPostsInput{}, invalidArgument("--to cannot be empty", "provide an exclusive date in YYYY-MM-DD format or remove --to")
	}
	return app.ListPostsInput{Cursor: cursor, Limit: limit, FromDate: fromDate, ToDate: toDate}, nil
}

func requiredContentFlag(input CommandInput) (string, error) {
	if !input.Changed("content") {
		return "", invalidArgument("--content is required", "provide the post Markdown with --content")
	}
	return input.String("content")
}

func postID(value string) (domain.ID, error) { return domain.ParseID(value) }

func getPostByTransport(ctx context.Context, invocation Invocation, id domain.ID) (postdto.Post, map[string]any, error) {
	var (
		post postdto.Post
		meta map[string]any
	)
	switch invocation.Mode() {
	case ModeDirect:
		service, _, database, err := openDirectPostRead(ctx, invocation)
		if err != nil {
			return post, nil, err
		}
		defer func() { _ = database.Close() }()
		result, err := service.Get(ctx, id)
		if err != nil {
			return post, nil, err
		}
		post = postdto.FromPost(result)
	case ModeAPI:
		var err error
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/posts/"+id.String(), nil, nil, "", false, &post)
		if err != nil {
			return post, nil, err
		}
		if status != http.StatusOK {
			return post, nil, invalidAPIResponse()
		}
	default:
		return post, nil, errors.New("unsupported post get transport")
	}
	return post, meta, nil
}

func openDirectPostRead(ctx context.Context, invocation Invocation) (*app.PostService, *app.SiteService, *sqlitestore.Database, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return nil, nil, nil, err
	}
	runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, nil, nil, ownerRuntimeError(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return nil, nil, nil, postDatabaseError(err)
	}
	postRepository, err := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		return nil, nil, nil, err
	}
	postService, err := app.NewPostService(postRepository)
	if err != nil {
		_ = database.Close()
		return nil, nil, nil, err
	}
	siteRepository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		return nil, nil, nil, err
	}
	siteService, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: runtimeState.IsDevelopment()})
	if err != nil {
		_ = database.Close()
		return nil, nil, nil, err
	}
	return postService, siteService, database, nil
}

type directPostRuntime struct {
	service     *app.PostService
	database    *sqlitestore.Database
	maintenance *runtimefs.Lease
}

func (runtime *directPostRuntime) Close() error {
	return errors.Join(runtime.database.Close(), runtime.maintenance.Release())
}

func openDirectPostWrite(ctx context.Context, invocation Invocation) (*directPostRuntime, error) {
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
	repository, err := sqlitestore.NewPostRepositoryWithinMaintenance(database, maintenance, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	service, err := app.NewPostService(repository)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	return &directPostRuntime{service: service, database: database, maintenance: maintenance}, nil
}

func postAPIRequest(
	ctx context.Context,
	invocation Invocation,
	method, endpoint string,
	query url.Values,
	payload any,
	idempotencyKey string,
	authenticate bool,
	destination any,
) (map[string]any, int, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, 0, err
	}
	endpointURL, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, 0, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	parsed, err := url.Parse(endpointURL)
	if err != nil {
		return nil, 0, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	if len(query) > 0 {
		parsed.RawQuery = query.Encode()
	}
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if authenticate {
		token, err := readAPIToken(target)
		if err != nil {
			return nil, 0, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxPostAPIResponse+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(responseBody) > maxPostAPIResponse {
		return nil, response.StatusCode, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the post API response exceeded the allowed size", "reduce --limit or inspect the compatible server", ExitUnavailable)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, decodeOwnerAPIFailure(response.StatusCode, responseBody)
	}
	mediaType, _, contentTypeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentTypeErr != nil || mediaType != "application/json" {
		return nil, response.StatusCode, invalidAPIResponse()
	}
	data, meta, err := decodePostAPIEnvelope(responseBody)
	if err != nil {
		return nil, response.StatusCode, invalidAPIResponse()
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return nil, response.StatusCode, invalidAPIResponse()
	}
	switch typed := destination.(type) {
	case *postdto.Post:
		if err := postdto.ValidatePost(*typed); err != nil {
			return nil, response.StatusCode, invalidAPIResponse()
		}
	case *postdto.Page:
		if err := postdto.ValidatePage(*typed); err != nil {
			return nil, response.StatusCode, invalidAPIResponse()
		}
	}
	return meta, response.StatusCode, nil
}

func decodePostAPIEnvelope(body []byte) (json.RawMessage, map[string]any, error) {
	if !utf8.Valid(body) {
		return nil, nil, errors.New("API response is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil, errors.New("API response is not an object")
	}
	var (
		data     json.RawMessage
		meta     map[string]any
		seenData bool
		seenMeta bool
	)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, nil, errors.New("API response key is invalid")
		}
		switch key {
		case "data":
			if seenData || decoder.Decode(&data) != nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
				return nil, nil, errors.New("API response data is invalid")
			}
			seenData = true
		case "meta":
			if seenMeta || decoder.Decode(&meta) != nil || meta == nil {
				return nil, nil, errors.New("API response meta is invalid")
			}
			seenMeta = true
		default:
			return nil, nil, errors.New("API response contains an unknown field")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || !seenData || !seenMeta {
		return nil, nil, errors.New("API response envelope is incomplete")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, nil, errors.New("API response contains trailing data")
	}
	return data, meta, nil
}

func postResult(action string, post postdto.Post, meta map[string]any) Result {
	return Result{
		Data: post,
		Text: fmt.Sprintf("%s %s.\nPublished: %s\n%s", action, post.ID, post.PublishedAt.Format("2006-01-02 15:04:05Z07:00"), post.ContentMD),
		Meta: meta,
	}
}

func postPageText(page postdto.Page, nextCursor *string) string {
	if len(page.Items) == 0 {
		return "No posts found"
	}
	lines := make([]string, 0, len(page.Items)+1)
	for _, post := range page.Items {
		content := strings.ReplaceAll(post.ContentMD, "\n", " ")
		if len([]rune(content)) > 80 {
			content = string([]rune(content)[:80]) + "…"
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s", post.ID, post.PublishedAt.Format("2006-01-02T15:04:05Z"), content))
	}
	if nextCursor != nil {
		lines = append(lines, "Next cursor: "+*nextCursor)
	}
	return strings.Join(lines, "\n")
}

func postDatabaseError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return NewCommandError("DATABASE_NOT_READY", "the current Moment database could not become ready", "inspect the selected data directory and migration history, then retry", ExitIntegrity).WithCause(err)
}

func postCursorFromMeta(meta map[string]any) (*string, error) {
	value, present := meta["next_cursor"]
	if !present || value == nil {
		if !present {
			return nil, invalidAPIResponse()
		}
		return nil, nil
	}
	cursor, ok := value.(string)
	if !ok || cursor == "" {
		return nil, invalidAPIResponse()
	}
	return &cursor, nil
}

func postCLIError(err error) error {
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return err
	}
	exit := ExitInternal
	suggestion := "retry once; if it repeats, inspect the selected Moment instance"
	switch domainErr.Code {
	case domain.CodeInvalidArgument:
		exit = ExitInvalidInput
		suggestion = "correct the named post input and retry"
	case domain.CodePostNotFound:
		exit = ExitNotFound
		suggestion = "run 'moment post list --json' to inspect available posts"
	case domain.CodeContentEmpty, domain.CodeContentTooLarge, domain.CodeExternalImageNotAllowed, domain.CodeIdempotencyKeyReused:
		exit = ExitConflict
		suggestion = "correct the post content or use a new idempotency key, then retry"
	case domain.CodeDataIntegrityFailed:
		exit = ExitIntegrity
		suggestion = "run diagnostics before changing posts"
	}
	return NewCommandError(string(domainErr.Code), domainErr.Message, suggestion, exit).WithDetails(domainErr.Details).WithCause(err)
}
