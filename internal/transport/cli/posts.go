package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/moment/moment/internal/transport/commentdto"
	"github.com/moment/moment/internal/transport/locationdto"
	"github.com/moment/moment/internal/transport/postdto"
	"github.com/moment/moment/internal/transport/searchdto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const maxPostAPIResponse = 64 << 20

const maxPostMediaManifestBytes = 1 << 20

const maxPostLocationBytes = 64 << 10

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
		Path:   []string{"post", "create"},
		Short:  "Publish a Moment post",
		Long:   "Publish one Markdown post through the shared application service in direct mode or the authenticated owner HTTP API.",
		Inputs: "Provide --content, managed media, or both. Add either --location-name or a canonical --location-json path|-; media and location JSON cannot both read stdin.",
		Output: "The canonical created post and replay metadata.",
		Errors: "Content validation, authentication, and idempotency conflicts use stable codes and exit statuses.",
		Example: "moment post create --data-dir ./moment-data --content 'hello **moment**' --media ./cat.jpg --media-alt 'cat sleeping' --idempotency-key demo-001 --json\n" +
			"moment post create --data-dir ./moment-data --media ./photo.jpg --idempotency-key demo-002 --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("content", "", "authoritative UTF-8 Markdown content")
			flags.String("idempotency-key", "", "retry key forwarded unchanged to the application service")
			registerPostMediaFlags(flags)
			registerPostLocationFlags(flags, false)
		},
		Run: runPostCreate,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"post", "update"},
		ArgsUsage: "<post-id>",
		Short:     "Update a Moment post",
		Long:      "Replace one post's authoritative Markdown while preserving its creation and publication position.",
		Inputs:    "Provide one post ULID and at least one content, media, or location change. Omitted location preserves it; --clear-location removes it.",
		Output:    "The complete updated canonical post DTO.",
		Errors:    "Invalid content, missing posts, and authentication failures use stable codes and exit statuses.",
		Example: "moment post update 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --content 'updated' --json\n" +
			"printf '[]' | moment post update 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --media-manifest - --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("content", "", "replacement UTF-8 Markdown content")
			registerPostMediaFlags(flags)
			registerPostLocationFlags(flags, true)
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
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "pin"},
		ArgsUsage:  "<post-id>",
		Short:      "Pin one Moment post to the public profile",
		Long:       "Atomically select one published Moment for profile emphasis without changing its chronological timeline position.",
		Inputs:     "Provide one canonical post ULID. Pinning replaces the prior selection after explicit confirmation.",
		Output:     "The complete site settings DTO with pinned_post_id set to the selected post.",
		Errors:     "Invalid or absent posts, declined confirmation, and authentication failures use stable codes and exit statuses.",
		Safety:     "Destructive profile presentation change. --yes confirms replacement; no post content or timestamp is changed.",
		Example:    "moment post pin 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --yes --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Policy: SafetyPolicy{
			Destructive:  true,
			Confirmation: "Pin this Moment post to the public profile?",
		},
		Run: runPostPin,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"post", "unpin"},
		ArgsUsage:  "<post-id>",
		Short:      "Remove a pinned Moment from the public profile",
		Long:       "Atomically clear the singleton profile pin while preserving the post and its chronological timeline position.",
		Inputs:     "Provide the canonical ULID of the currently pinned post. Repeating the same clear is an idempotent success.",
		Output:     "The complete site settings DTO with pinned_post_id cleared.",
		Errors:     "Invalid IDs, declined confirmation, and authentication failures use stable codes and exit statuses.",
		Safety:     "Destructive profile presentation change. --yes confirms removal; no post content is deleted.",
		Example:    "moment post unpin 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --yes --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Policy: SafetyPolicy{
			Destructive:  true,
			Confirmation: "Remove this pinned Moment from the public profile?",
		},
		Run: runPostUnpin,
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
	locationInput, err := postLocationInputFromFlags(ctx, input, false)
	if err != nil {
		return Result{}, err
	}
	mediaInput, err := postMediaInputFromFlags(ctx, input)
	if err != nil {
		return Result{}, err
	}
	content, err := input.String("content")
	if err != nil {
		return Result{}, err
	}
	if !input.Changed("content") && !mediaInput.Present {
		return Result{}, invalidArgument("post create requires --content or a media input", "provide --content, --media, or --media-manifest")
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
		usages, err := resolvePostMediaInput(mediaInput, func(path string) (domain.ID, error) {
			return ingestDirectPostMedia(ctx, direct.media, path)
		})
		if err != nil {
			return Result{}, err
		}
		created, err := direct.service.Create(ctx, app.CreatePostInput{ContentMD: content, Media: usages, Location: locationInput.Location, IdempotencyKey: idempotencyKey})
		if err != nil {
			return Result{}, postCLIError(err)
		}
		post = postdto.FromPost(created.Post)
		meta = map[string]any{"replayed": created.Replayed}
	case ModeAPI:
		usages, err := resolvePostMediaInput(mediaInput, func(path string) (domain.ID, error) {
			return ingestAPIPostMedia(ctx, invocation, path)
		})
		if err != nil {
			return Result{}, err
		}
		payload := postWritePayload{ContentMD: &content}
		if mediaInput.Present {
			wire := postMediaDTOs(usages)
			payload.Media = &wire
		}
		if locationInput.Present {
			wire := locationdto.FromDomain(*locationInput.Location)
			pointer := &wire
			payload.Location = &pointer
		}
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
	content, err := input.String("content")
	if err != nil {
		return Result{}, err
	}
	contentPresent := input.Changed("content")
	mediaInput, err := postMediaInputFromFlags(ctx, input)
	if err != nil {
		return Result{}, err
	}
	locationInput, err := postLocationInputFromFlags(ctx, input, true)
	if err != nil {
		return Result{}, err
	}
	if !contentPresent && !mediaInput.Present && !locationInput.Present {
		return Result{}, invalidArgument("post update requires a content, media, or location change", "provide --content, media input, --location-name, --location-json, or --clear-location")
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
		usages, err := resolvePostMediaInput(mediaInput, func(path string) (domain.ID, error) {
			return ingestDirectPostMedia(ctx, direct.media, path)
		})
		if err != nil {
			return Result{}, err
		}
		update := app.UpdatePostInput{ID: id, ContentMD: content, PreserveContent: !contentPresent}
		if mediaInput.Present {
			update.Media = &usages
		}
		if locationInput.Present {
			location := domain.CloneLocation(locationInput.Location)
			update.Location = &location
		}
		updated, err := direct.service.Update(ctx, update)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		post = postdto.FromPost(updated)
	case ModeAPI:
		usages, err := resolvePostMediaInput(mediaInput, func(path string) (domain.ID, error) {
			return ingestAPIPostMedia(ctx, invocation, path)
		})
		if err != nil {
			return Result{}, err
		}
		payload := postWritePayload{}
		if contentPresent {
			payload.ContentMD = &content
		}
		if mediaInput.Present {
			wire := postMediaDTOs(usages)
			payload.Media = &wire
		}
		if locationInput.Present {
			if locationInput.Location == nil {
				var cleared *locationdto.Location
				payload.Location = &cleared
			} else {
				wire := locationdto.FromDomain(*locationInput.Location)
				pointer := &wire
				payload.Location = &pointer
			}
		}
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodPatch, "/api/v1/owner/posts/"+id.String(), nil, payload, "", true, &post)
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

func runPostPin(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	return runPostPinChange(ctx, invocation, input, true)
}

func runPostUnpin(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	return runPostPinChange(ctx, invocation, input, false)
}

func runPostPinChange(ctx context.Context, invocation Invocation, input CommandInput, pin bool) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	var (
		settings app.SiteSettings
		meta     map[string]any
	)
	switch invocation.Mode() {
	case ModeDirect:
		settings, err = directSitePinChange(ctx, invocation, id, pin)
	case ModeAPI:
		method := http.MethodPut
		if !pin {
			method = http.MethodDelete
		}
		meta, err = siteAPIRequest(ctx, invocation, method, "/api/v1/owner/posts/"+id.String()+"/pin", http.StatusOK, nil, true, &settings)
		if err == nil {
			if (pin && (settings.PinnedPostID == nil || *settings.PinnedPostID != id.String())) || (!pin && settings.PinnedPostID != nil) {
				return Result{}, invalidAPIResponse()
			}
		}
	default:
		err = errors.New("unsupported post pin transport")
	}
	if err != nil {
		return Result{}, siteCLIError(err)
	}
	action := "Post unpinned"
	if pin {
		action = "Post pinned"
	}
	result := siteResult(action, settings)
	result.Meta = meta
	return result, nil
}

type postWritePayload struct {
	ContentMD *string                `json:"content_md,omitempty"`
	Media     *[]postdto.MediaUsage  `json:"media,omitempty"`
	Location  **locationdto.Location `json:"location,omitempty"`
}

type postLocationInput struct {
	Present  bool
	Location *domain.Location
}

type postMediaInput struct {
	Present bool
	Items   []postMediaSource
}

type postMediaSource struct {
	Path    string
	MediaID domain.ID
	AltText *string
}

func registerPostMediaFlags(flags *pflag.FlagSet) {
	flags.Var(&onceStringFlag{name: "--media"}, "media", "one local image path (mutually exclusive with --media-manifest)")
	flags.Var(&onceStringFlag{name: "--media-alt"}, "media-alt", "non-empty provided alt text for --media")
	flags.Bool("media-decorative", false, "encode --media with explicit decorative alt text")
	flags.Var(&onceStringFlag{name: "--media-manifest"}, "media-manifest", "ordered JSON manifest path or - for stdin")
}

func registerPostLocationFlags(flags *pflag.FlagSet, allowClear bool) {
	flags.Var(&onceStringFlag{name: "--location-name"}, "location-name", "manual place label (mutually exclusive with --location-json)")
	flags.Var(&onceStringFlag{name: "--location-json"}, "location-json", "canonical location object path or - for stdin")
	if allowClear {
		flags.Bool("clear-location", false, "remove the post location")
	}
}

func postLocationInputFromFlags(ctx context.Context, input CommandInput, allowClear bool) (postLocationInput, error) {
	name, err := input.String("location-name")
	if err != nil {
		return postLocationInput{}, err
	}
	path, err := input.String("location-json")
	if err != nil {
		return postLocationInput{}, err
	}
	nameChanged, pathChanged := input.Changed("location-name"), input.Changed("location-json")
	clear := false
	if allowClear {
		clear, err = input.Bool("clear-location")
		if err != nil {
			return postLocationInput{}, err
		}
	}
	selected := 0
	for _, value := range []bool{nameChanged, pathChanged, clear} {
		if value {
			selected++
		}
	}
	if selected > 1 {
		return postLocationInput{}, invalidArgument("location flags are mutually exclusive", "choose --location-name, --location-json, or --clear-location")
	}
	if selected == 0 {
		return postLocationInput{}, nil
	}
	if clear {
		return postLocationInput{Present: true}, nil
	}
	if nameChanged {
		name = strings.TrimSpace(name)
		location := &domain.Location{Name: name, DisplayName: name, Provider: "manual"}
		if err := location.Validate(); err != nil {
			return postLocationInput{}, postCLIError(err)
		}
		return postLocationInput{Present: true, Location: location}, nil
	}
	if strings.TrimSpace(path) == "" {
		return postLocationInput{}, invalidArgument("--location-json cannot be empty", "provide a canonical JSON object path or - for stdin")
	}
	if path == "-" && input.Changed("media-manifest") {
		mediaManifest, _ := input.String("media-manifest")
		if mediaManifest == "-" {
			return postLocationInput{}, invalidArgument("location and media JSON cannot both read stdin", "put one input in a regular file")
		}
	}
	data, err := readBoundedPostLocation(ctx, input.In(), path)
	if err != nil {
		return postLocationInput{}, err
	}
	var wire locationdto.Location
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&wire); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return postLocationInput{}, invalidArgument("--location-json must contain one canonical location object", "use one item returned by 'moment location search --json'")
	}
	location, err := wire.Domain()
	if err != nil {
		return postLocationInput{}, postCLIError(err)
	}
	return postLocationInput{Present: true, Location: &location}, nil
}

func readBoundedPostLocation(ctx context.Context, stdin io.Reader, path string) ([]byte, error) {
	var reader io.Reader
	var file *os.File
	if path == "-" {
		if stdin == nil {
			return nil, invalidArgument("stdin is unavailable for --location-json -", "pipe one canonical location object to stdin")
		}
		reader = stdin
	} else {
		clean := filepath.Clean(path)
		info, err := os.Lstat(clean)
		if err != nil {
			return nil, postCLIError(err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxPostLocationBytes {
			return nil, invalidArgument("--location-json must be a regular file no larger than 64 KiB", "provide a stable canonical JSON file or use - for stdin")
		}
		file, err = os.Open(clean)
		if err != nil {
			return nil, postCLIError(err)
		}
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			_ = file.Close()
			return nil, invalidArgument("--location-json changed while it was opened", "retry with a stable regular file")
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(cliContextReader{ctx: ctx, source: reader}, maxPostLocationBytes+1))
	if err != nil {
		return nil, postCLIError(err)
	}
	if len(data) > maxPostLocationBytes || !utf8.Valid(data) {
		return nil, invalidArgument("--location-json must be valid UTF-8 JSON no larger than 64 KiB", "provide one canonical location object")
	}
	return data, nil
}

type onceStringFlag struct {
	name  string
	value string
	set   bool
}

func (flag *onceStringFlag) Set(value string) error {
	if flag.set {
		return fmt.Errorf("%s may be supplied only once", flag.name)
	}
	flag.value = value
	flag.set = true
	return nil
}

func (flag *onceStringFlag) String() string { return flag.value }

func (*onceStringFlag) Type() string { return "string" }

func postMediaInputFromFlags(ctx context.Context, input CommandInput) (postMediaInput, error) {
	mediaPath, err := input.String("media")
	if err != nil {
		return postMediaInput{}, err
	}
	alt, err := input.String("media-alt")
	if err != nil {
		return postMediaInput{}, err
	}
	decorative, err := input.Bool("media-decorative")
	if err != nil {
		return postMediaInput{}, err
	}
	manifestPath, err := input.String("media-manifest")
	if err != nil {
		return postMediaInput{}, err
	}
	mediaChanged := input.Changed("media")
	altChanged := input.Changed("media-alt")
	decorativeChanged := input.Changed("media-decorative")
	manifestChanged := input.Changed("media-manifest")
	if manifestChanged && (mediaChanged || altChanged || decorativeChanged) {
		return postMediaInput{}, invalidArgument("--media-manifest is mutually exclusive with single-image media flags", "use either --media-manifest or --media with one alt mode")
	}
	if altChanged && decorativeChanged {
		return postMediaInput{}, invalidArgument("--media-alt and --media-decorative are mutually exclusive", "choose provided alt text or the decorative flag")
	}
	if (altChanged || decorativeChanged) && !mediaChanged {
		return postMediaInput{}, invalidArgument("--media-alt and --media-decorative require --media", "provide one --media path or remove the alt flag")
	}
	if manifestChanged {
		if strings.TrimSpace(manifestPath) == "" {
			return postMediaInput{}, invalidArgument("--media-manifest cannot be empty", "provide a JSON file path or - for stdin")
		}
		items, err := readPostMediaManifest(ctx, input.In(), manifestPath)
		if err != nil {
			return postMediaInput{}, err
		}
		return postMediaInput{Present: true, Items: items}, nil
	}
	if !mediaChanged {
		return postMediaInput{}, nil
	}
	if strings.TrimSpace(mediaPath) == "" || strings.ContainsRune(mediaPath, 0) {
		return postMediaInput{}, invalidArgument("--media requires a non-empty local path", "provide one regular image file path")
	}
	item := postMediaSource{Path: mediaPath}
	switch {
	case decorative:
		value := ""
		item.AltText = &value
	case altChanged:
		if strings.TrimSpace(alt) == "" {
			return postMediaInput{}, invalidArgument("--media-alt must contain visible text", "use --media-decorative for an explicit empty alt or omit --media-alt for missing alt")
		}
		item.AltText = &alt
	}
	return postMediaInput{Present: true, Items: []postMediaSource{item}}, nil
}

func readPostMediaManifest(ctx context.Context, stdin io.Reader, path string) ([]postMediaSource, error) {
	var reader io.Reader
	var file *os.File
	if path == "-" {
		if stdin == nil {
			return nil, invalidArgument("stdin is unavailable for --media-manifest -", "pipe one ordered JSON array to stdin")
		}
		reader = stdin
	} else {
		clean := filepath.Clean(path)
		info, err := os.Lstat(clean)
		if err != nil {
			return nil, mediaCLIError(err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, invalidArgument("--media-manifest must name a regular non-symlink file", "provide a bounded JSON manifest file or use - for stdin")
		}
		if info.Size() > maxPostMediaManifestBytes {
			return nil, invalidArgument("--media-manifest exceeds the 1 MiB input limit", "reduce the manifest to at most 9 items")
		}
		file, err = os.Open(clean)
		if err != nil {
			return nil, mediaCLIError(err)
		}
		openedInfo, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, mediaCLIError(err)
		}
		if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			_ = file.Close()
			return nil, invalidArgument("--media-manifest changed while it was being opened", "retry with a stable regular non-symlink file or use - for stdin")
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(cliContextReader{ctx: ctx, source: reader}, maxPostMediaManifestBytes+1))
	if err != nil {
		return nil, mediaCLIError(err)
	}
	if len(data) > maxPostMediaManifestBytes {
		return nil, invalidArgument("--media-manifest exceeds the 1 MiB input limit", "reduce the manifest to at most 9 items")
	}
	return decodePostMediaManifest(data)
}

func decodePostMediaManifest(data []byte) ([]postMediaSource, error) {
	if !utf8.Valid(data) {
		return nil, invalidArgument("media manifest must be valid UTF-8 JSON", "encode one ordered JSON array as UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, invalidArgument("media manifest must be one JSON array", "provide an ordered array with path or media_id in each item")
	}
	items := make([]postMediaSource, 0, domain.MaxEntryMediaUsages)
	for decoder.More() {
		if len(items) == domain.MaxEntryMediaUsages {
			return nil, invalidArgument("media manifest may contain at most 9 items", "remove items until positions fit 0 through 8")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, invalidArgument("media manifest item is malformed", "use one object per ordered media usage")
		}
		item, err := decodePostMediaManifestItem(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, invalidArgument("media manifest must be one complete JSON array", "fix the manifest syntax and retry")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, invalidArgument("media manifest must contain exactly one JSON document", "remove trailing JSON or text")
	}
	probe := make([]domain.EntryMediaUsage, len(items))
	placeholder := domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	for index, item := range items {
		id := item.MediaID
		if id == "" {
			id = placeholder
		}
		probe[index] = domain.EntryMediaUsage{MediaID: id, Position: index, AltText: item.AltText}
	}
	if err := domain.ValidateEntryMediaUsages(probe); err != nil {
		return nil, postCLIError(err)
	}
	return items, nil
}

func decodePostMediaManifestItem(data []byte) (postMediaSource, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return postMediaSource{}, invalidArgument("media manifest items must be JSON objects", "use path or media_id plus explicit nullable alt_text")
	}
	seen := map[string]bool{}
	item := postMediaSource{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return postMediaSource{}, invalidArgument("media manifest item fields must appear exactly once", "remove duplicate fields")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return postMediaSource{}, invalidArgument("media manifest item field is malformed", "fix the JSON value and retry")
		}
		switch key {
		case "path":
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &item.Path) != nil || strings.TrimSpace(item.Path) == "" || strings.ContainsRune(item.Path, 0) {
				return postMediaSource{}, invalidArgument("manifest path must be a non-empty string", "provide one regular local image path")
			}
		case "media_id":
			var value string
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
				return postMediaSource{}, invalidArgument("manifest media_id must be a string", "provide a canonical managed media ULID")
			}
			id, err := domain.ParseID(value)
			if err != nil {
				return postMediaSource{}, postCLIError(err)
			}
			item.MediaID = id
		case "alt_text":
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				var value string
				if json.Unmarshal(raw, &value) != nil {
					return postMediaSource{}, invalidArgument("manifest alt_text must be null or a string", "use null for missing, an empty string for decorative, or visible text")
				}
				item.AltText = &value
			}
		default:
			return postMediaSource{}, invalidArgument("media manifest item contains an unknown field", "use only path, media_id, and alt_text")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || !seen["alt_text"] || (seen["path"] == seen["media_id"]) {
		return postMediaSource{}, invalidArgument("each media manifest item requires exactly one of path or media_id and explicit alt_text", "fix the manifest item and retry")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return postMediaSource{}, invalidArgument("media manifest item contains trailing data", "use one JSON object per item")
	}
	return item, nil
}

func resolvePostMediaInput(input postMediaInput, upload func(string) (domain.ID, error)) ([]domain.EntryMediaUsage, error) {
	if !input.Present {
		return nil, nil
	}
	usages := make([]domain.EntryMediaUsage, 0, len(input.Items))
	for index, item := range input.Items {
		id := item.MediaID
		if item.Path != "" {
			var err error
			id, err = upload(item.Path)
			if err != nil {
				return nil, err
			}
		}
		usages = append(usages, domain.EntryMediaUsage{MediaID: id, Position: index, AltText: item.AltText})
	}
	if err := domain.ValidateEntryMediaUsages(usages); err != nil {
		return nil, postCLIError(err)
	}
	return usages, nil
}

func ingestDirectPostMedia(ctx context.Context, store *media.Store, path string) (domain.ID, error) {
	file, name, err := openPostMediaFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	value, _, err := store.Ingest(ctx, name, file)
	if err != nil {
		return "", mediaCLIError(err)
	}
	return value.ID, nil
}

func ingestAPIPostMedia(ctx context.Context, invocation Invocation, path string) (domain.ID, error) {
	file, name, err := openPostMediaFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	var result media.UploadResult
	if _, err := mediaAPIUpload(ctx, invocation, name, file, &result); err != nil {
		return "", mediaCLIError(err)
	}
	if _, err := domain.ParseID(result.Media.ID.String()); err != nil {
		return "", invalidAPIResponse()
	}
	return result.Media.ID, nil
}

func openPostMediaFile(path string) (*os.File, string, error) {
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	if err != nil {
		return nil, "", mediaCLIError(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", invalidArgument("post media paths must name regular non-symlink files", "provide a regular JPEG, PNG, WebP, or GIF file")
	}
	if info.Size() > media.DefaultMaxBytes {
		return nil, "", mediaCLIError(domain.NewError(domain.CodeMediaTooLarge, "image exceeds the configured byte budget", map[string]any{"max_bytes": media.DefaultMaxBytes}))
	}
	file, err := os.Open(clean)
	if err != nil {
		return nil, "", mediaCLIError(err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, "", mediaCLIError(err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, "", invalidArgument("post media path changed while it was being opened", "retry with a stable regular non-symlink image file")
	}
	return file, filepath.Base(clean), nil
}

func postMediaDTOs(usages []domain.EntryMediaUsage) []postdto.MediaUsage {
	result := make([]postdto.MediaUsage, 0, len(usages))
	for _, usage := range usages {
		mapped := postdto.MediaUsage{MediaID: usage.MediaID.String(), Position: usage.Position}
		if usage.AltText != nil {
			alt := *usage.AltText
			mapped.AltText = &alt
		}
		result = append(result, mapped)
	}
	return result
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
	media       *media.Store
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
	mediaStore, err := media.NewStoreWithinMaintenance(runtimeState, database, media.Options{}, maintenance)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	return &directPostRuntime{service: service, media: mediaStore, database: database, maintenance: maintenance}, nil
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
		return nil, response.StatusCode, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the Moment API response exceeded the allowed size", "reduce --limit or inspect the compatible server", ExitUnavailable)
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return nil, response.StatusCode, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, decodeOwnerAPIFailure(response.StatusCode, responseBody)
	}
	data, meta, err := decodePostAPIEnvelope(responseBody)
	if err != nil {
		return nil, response.StatusCode, invalidAPIResponse()
	}
	dataDecoder := json.NewDecoder(bytes.NewReader(data))
	dataDecoder.DisallowUnknownFields()
	if err := dataDecoder.Decode(destination); err != nil || dataDecoder.Decode(&struct{}{}) != io.EOF {
		return nil, response.StatusCode, invalidAPIResponse()
	}
	if !validPostAPIRawData(data, destination) {
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

func validPostAPIRawData(data json.RawMessage, destination any) bool {
	switch typed := destination.(type) {
	case *postdto.Post:
		return typed != nil && validRawAPIPost(data, *typed)
	case *postdto.Page:
		return typed != nil && validRawAPIPostPage(data, *typed)
	case *commentdto.Comment:
		return typed != nil && validRawAPIComment(data, *typed)
	case *commentdto.Thread:
		return typed != nil && validRawAPICommentThread(data, *typed)
	case *searchdto.SearchData:
		return typed != nil && validRawAPISearch(data, *typed)
	default:
		return false
	}
}

func validRawAPIPost(data json.RawMessage, post postdto.Post) bool {
	if !hasRequiredNonNullAPIDataFields(data, "id", "content_md", "content_html", "created_at", "updated_at", "published_at", "comment_count", "media") ||
		!hasRequiredAPIDataFields(data, "location") ||
		!validCanonicalAPIInstantField(data, "created_at", post.CreatedAt) ||
		!validCanonicalAPIInstantField(data, "updated_at", post.UpdatedAt) ||
		!validCanonicalAPIInstantField(data, "published_at", post.PublishedAt) {
		return false
	}
	fields, ok := apiDataFields(data)
	if !ok {
		return false
	}
	mediaItems, ok := apiRawArray(fields["media"])
	if !ok || len(mediaItems) != len(post.Media) {
		return false
	}
	for _, usage := range mediaItems {
		if !hasRequiredAPIDataFields(usage, "media_id", "position", "alt_text", "width", "height") ||
			!hasRequiredNonNullAPIDataFields(usage, "media_id", "position", "width", "height") {
			return false
		}
	}
	if post.Location != nil {
		if !hasRequiredNonNullAPIDataFields(fields["location"], "name", "display_name") {
			return false
		}
	}
	return true
}

func validRawAPIPostPage(data json.RawMessage, page postdto.Page) bool {
	if !hasRequiredNonNullAPIDataFields(data, "items") {
		return false
	}
	fields, _ := apiDataFields(data)
	items, ok := apiRawArray(fields["items"])
	if !ok || len(items) != len(page.Items) {
		return false
	}
	for index := range items {
		if !validRawAPIPost(items[index], page.Items[index]) {
			return false
		}
	}
	return true
}

func validRawAPIComment(data json.RawMessage, comment commentdto.Comment) bool {
	if !hasRequiredAPIDataFields(data, "id", "post_id", "parent_id", "depth", "author_kind", "author_name", "content_md", "content_html", "created_at", "media") ||
		!hasRequiredNonNullAPIDataFields(data, "id", "post_id", "depth", "author_kind", "author_name", "content_md", "content_html", "created_at", "media") ||
		!validCanonicalAPIInstantField(data, "created_at", comment.CreatedAt) {
		return false
	}
	fields, _ := apiDataFields(data)
	mediaItems, ok := apiRawArray(fields["media"])
	if !ok || len(mediaItems) != len(comment.Media) {
		return false
	}
	for _, usage := range mediaItems {
		if !hasRequiredAPIDataFields(usage, "media_id", "position", "alt_text", "width", "height") ||
			!hasRequiredNonNullAPIDataFields(usage, "media_id", "position", "width", "height") {
			return false
		}
	}
	parent := bytes.TrimSpace(fields["parent_id"])
	if comment.ParentID == nil {
		return bytes.Equal(parent, []byte("null"))
	}
	return !bytes.Equal(parent, []byte("null"))
}

func validRawAPICommentThread(data json.RawMessage, thread commentdto.Thread) bool {
	if !hasRequiredNonNullAPIDataFields(data, "items") {
		return false
	}
	fields, _ := apiDataFields(data)
	items, ok := apiRawArray(fields["items"])
	if !ok || len(items) != len(thread.Items) {
		return false
	}
	for index := range items {
		if !validRawAPIComment(items[index], thread.Items[index]) {
			return false
		}
	}
	return true
}

func validRawAPISearch(data json.RawMessage, search searchdto.SearchData) bool {
	if !hasRequiredNonNullAPIDataFields(data, "items", "year_counts") {
		return false
	}
	fields, _ := apiDataFields(data)
	items, itemsOK := apiRawArray(fields["items"])
	years, yearsOK := apiRawArray(fields["year_counts"])
	if !itemsOK || !yearsOK || len(items) != len(search.Items) || len(years) != len(search.YearCounts) {
		return false
	}
	for index := range items {
		if !hasRequiredNonNullAPIDataFields(items[index], "snippet") || !validRawAPIPost(items[index], search.Items[index].Post) {
			return false
		}
	}
	for _, year := range years {
		if !hasRequiredNonNullAPIDataFields(year, "year", "count") {
			return false
		}
	}
	return true
}

func apiRawArray(data json.RawMessage) ([]json.RawMessage, bool) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(data, &items) != nil || items == nil {
		return nil, false
	}
	return items, true
}

func decodePostAPIEnvelope(body []byte) (json.RawMessage, map[string]any, error) {
	return decodeStrictAPIEnvelope(body)
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
	return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
}
