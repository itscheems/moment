package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerMedia(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"media", "add"}, ArgsUsage: "<path>", Short: "Add one managed image",
		Long:    "Validate one local JPEG, PNG, WebP, or GIF, store its immutable original, and build the current preview recipe through the selected transport.",
		Inputs:  "One readable local file path. API mode authenticates through MOMENT_TOKEN or --token-file.",
		Output:  "Managed media metadata, preview metadata, and explicit original/preview URLs.",
		Errors:  "Invalid images use stable MEDIA_* codes; filesystem failures use exit code 6; cancellation uses exit code 9.",
		Example: "moment media add ./fixtures/cat.jpg --data-dir ./moment-data --json",
		Args:    exactArgs(1), Capability: CapabilityBoth, Run: runMediaAdd,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"media", "get"}, ArgsUsage: "<media-id>", Short: "Save an explicit immutable original",
		Long:    "Read the explicitly requested original through the selected transport and atomically save it without overwriting an existing path.",
		Inputs:  "A canonical media ID and required --output path. GIF originals remain animated; this command never substitutes a preview.",
		Output:  "Media ID, SHA-256, controlled MIME type, byte count, and absolute output path.",
		Errors:  "Missing media returns MEDIA_NOT_FOUND; corrupt originals return MEDIA_CORRUPT; existing output is rejected.",
		Example: "moment media get 01ARZ3NDEKTSV4RRFFQ69G5FAV --output ./downloaded.jpg --data-dir ./moment-data --json",
		Args:    exactArgs(1), Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) { flags.String("output", "", "new file path for the immutable original") },
		Run:   runMediaGet,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"media", "verify"}, ArgsUsage: "[media-id]", Short: "Verify managed originals and references",
		Long:    "Checksum immutable originals and report central resolver clock consistency without mutating authority or cache data.",
		Inputs:  "An optional canonical media ID; omitting it verifies every media record in the selected local instance.",
		Output:  "Deterministic healthy, corrupt, and reference-inconsistent counts plus per-media status.",
		Errors:  "Invalid IDs return INVALID_ARGUMENT; unavailable or malformed authority data returns an integrity or IO error.",
		Example: "moment media verify --data-dir ./moment-data --json",
		Args:    rangeArgs(0, 1), Capability: CapabilityDirectOnly, Run: runMediaVerify,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"media", "list"}, Short: "List managed media lifecycle state",
		Long:       "List managed media with the central reference decision, continuous unreferenced clock, and current preview cache status.",
		Inputs:     "An initialized local data directory. This read-only command does not accept API mode.",
		Output:     "Recipe version and stable ID-ordered media lifecycle items.",
		Errors:     "Malformed authority data returns DATA_INTEGRITY_FAILED; lock cancellation uses exit code 9.",
		Example:    "moment media list --data-dir ./moment-data --json",
		Capability: CapabilityDirectOnly, Run: runMediaList,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"media", "rebuild-previews"}, Short: "Repair derived preview cache",
		Long:       "Inspect or deterministically rebuild missing, stale, and partially corrupt current-recipe previews without modifying originals or database authority.",
		Inputs:     "Use --dry-run first to inspect counts and estimated additional bytes. Actual work is bounded, cancellable, and idempotent.",
		Output:     "Recipe version and total, ready, missing, stale, rebuilt, and estimated-space counts.",
		Errors:     "Corrupt or missing originals return MEDIA_CORRUPT; cancellation returns exit code 9 and a rerun safely resumes.",
		Safety:     "Actual rebuild replaces only derived preview cache artifacts and requires confirmation or --yes.",
		Example:    "moment media rebuild-previews --data-dir ./moment-data --dry-run --json",
		Capability: CapabilityDirectOnly,
		Policy:     SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: "Rebuild derived preview cache now?"},
		Run:        runMediaRebuild,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"media", "prune"}, Short: "Delete continuously unreferenced media",
		Long:       "Delete only media rechecked by the central resolver as continuously unreferenced for at least 24 hours inside the exclusive destructive boundary.",
		Inputs:     "Use --dry-run first. Actual deletion requires confirmation or --yes; confirmation never bypasses reachability or the 24-hour clock.",
		Output:     "Eligible/deleted counts, reclaimed bytes, cleanup warnings, and the exact candidate IDs.",
		Errors:     "Lock cancellation uses exit code 9; authority corruption and storage failures use stable integrity/IO behavior.",
		Safety:     "Originals and media rows are destructive; database authority commits before best-effort blob/cache cleanup.",
		Example:    "moment media prune --data-dir ./moment-data --dry-run --json",
		Capability: CapabilityDirectOnly,
		Policy:     SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: "Permanently prune eligible media now?"},
		Run:        runMediaPrune,
	})
}

func exactArgs(count int) func(*cobra.Command, []string) error { return cobra.ExactArgs(count) }

func rangeArgs(minimum, maximum int) func(*cobra.Command, []string) error {
	return cobra.RangeArgs(minimum, maximum)
}

func runMediaAdd(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	path := input.Args()[0]
	file, err := os.Open(path)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	defer func() { _ = file.Close() }()
	var result media.UploadResult
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		direct, err := openDirectMedia(ctx, invocation)
		if err != nil {
			return Result{}, mediaCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		value, preview, err := direct.store.Ingest(ctx, filepath.Base(path), file)
		if err != nil {
			return Result{}, mediaCLIError(err)
		}
		result = media.NewUploadResult(value, preview)
	case ModeAPI:
		meta, err = mediaAPIUpload(ctx, invocation, filepath.Base(path), file, &result)
		if err != nil {
			return Result{}, mediaCLIError(err)
		}
	default:
		return Result{}, errors.New("unsupported media add transport")
	}
	return Result{Data: result, Meta: meta, Text: fmt.Sprintf("Added media %s (%s, %d bytes)", result.Media.ID, result.Media.MIMEType, result.Media.ByteSize)}, nil
}

type mediaGetResult struct {
	ID         string `json:"id"`
	SHA256     string `json:"sha256"`
	MIMEType   string `json:"mime_type"`
	ByteSize   int64  `json:"byte_size"`
	OutputPath string `json:"output_path"`
}

func runMediaGet(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id, err := domain.ParseID(input.Args()[0])
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	output, err := input.String("output")
	if err != nil || strings.TrimSpace(output) == "" {
		return Result{}, invalidArgument("--output is required", "provide a new local file path for the immutable original")
	}
	var asset media.Asset
	switch invocation.Mode() {
	case ModeDirect:
		direct, openErr := openDirectMedia(ctx, invocation)
		err = openErr
		if err != nil {
			return Result{}, mediaCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		asset, err = direct.store.ReadOriginal(ctx, id)
	case ModeAPI:
		asset, err = mediaAPIDownload(ctx, invocation, id)
	default:
		err = errors.New("unsupported media get transport")
	}
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	absolute, err := writeNewFileAtomic(ctx, output, asset.Contents)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	sha := asset.Media.SHA256
	if sha == "" {
		sha = strings.TrimSuffix(strings.TrimPrefix(asset.ETag, `"sha256-`), `"`)
	}
	data := mediaGetResult{ID: id.String(), SHA256: sha, MIMEType: asset.MIMEType, ByteSize: int64(len(asset.Contents)), OutputPath: absolute}
	return Result{Data: data, Text: fmt.Sprintf("Saved media %s to %s", id, absolute)}, nil
}

func runMediaVerify(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	direct, err := openDirectMedia(ctx, invocation)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	defer func() { _ = direct.Close() }()
	var selected *domain.ID
	if len(input.Args()) == 1 {
		id, err := domain.ParseID(input.Args()[0])
		if err != nil {
			return Result{}, mediaCLIError(err)
		}
		selected = &id
	}
	result, err := direct.store.Verify(ctx, selected)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	if result.Corrupt > 0 || result.ReferenceInconsistent > 0 {
		return Result{}, NewCommandError(
			string(domain.CodeDataIntegrityFailed),
			"media verification found authority inconsistencies",
			"inspect error details, restore corrupt originals, and reconcile media references before retrying",
			ExitIntegrity,
		).WithDetails(map[string]any{
			"total": result.Total, "healthy": result.Healthy, "corrupt": result.Corrupt,
			"reference_inconsistent": result.ReferenceInconsistent, "items": result.Items,
		})
	}
	return Result{Data: result, Text: fmt.Sprintf("Verified %d media: %d healthy, %d corrupt, %d reference-inconsistent", result.Total, result.Healthy, result.Corrupt, result.ReferenceInconsistent)}, nil
}

func runMediaList(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	direct, err := openDirectMedia(ctx, invocation)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	defer func() { _ = direct.Close() }()
	result, err := direct.store.List(ctx)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	return Result{Data: result, Text: fmt.Sprintf("Managed media: %d (preview recipe %s)", len(result.Items), result.RecipeVersion)}, nil
}

func runMediaRebuild(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	direct, err := openDirectMedia(ctx, invocation)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	defer func() { _ = direct.Close() }()
	result, err := direct.store.RebuildPreviews(ctx, invocation.Safety().DryRun)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	return Result{Data: result, Text: fmt.Sprintf("Preview recipe %s: %d ready, %d rebuilt, %d missing, %d stale", result.RecipeVersion, result.Ready, result.Rebuilt, result.Missing, result.Stale)}, nil
}

func runMediaPrune(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	direct, err := openDirectMedia(ctx, invocation)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	defer func() { _ = direct.Close() }()
	result, err := direct.store.Prune(ctx, time.Now().UTC(), invocation.Safety().DryRun)
	if err != nil {
		return Result{}, mediaCLIError(err)
	}
	return Result{Data: result, Text: fmt.Sprintf("Media prune: %d eligible, %d deleted, %d bytes reclaimed, %d cleanup warnings", result.Eligible, result.Deleted, result.BytesReclaimed, result.CleanupFailures)}, nil
}

type directMediaRuntime struct {
	store       *media.Store
	database    *sqlitestore.Database
	maintenance *runtimefs.Lease
}

func (direct *directMediaRuntime) Close() error {
	if direct == nil {
		return nil
	}
	return errors.Join(direct.database.Close(), direct.maintenance.Release())
}

func openDirectMedia(ctx context.Context, invocation Invocation) (*directMediaRuntime, error) {
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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, NewCommandError("DATABASE_NOT_READY", "the current Moment database could not become ready", "inspect the selected data directory and migration history, then retry", ExitIntegrity).WithCause(err)
	}
	store, err := media.NewStoreWithinMaintenance(runtimeState, database, media.Options{}, maintenance)
	if err != nil {
		_ = database.Close()
		_ = maintenance.Release()
		return nil, err
	}
	return &directMediaRuntime{store: store, database: database, maintenance: maintenance}, nil
}

func mediaAPIUpload(ctx context.Context, invocation Invocation, filename string, source io.Reader, destination *media.UploadResult) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, io.LimitReader(cliContextReader{ctx: ctx, source: source}, media.DefaultMaxBytes+1)); err != nil {
		return nil, err
	}
	if body.Len() > int(media.DefaultMaxBytes)+(1<<20) {
		return nil, domain.NewError(domain.CodeMediaTooLarge, "image exceeds the configured byte budget", map[string]any{"max_bytes": media.DefaultMaxBytes})
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/media")
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, err
	}
	if len(responseBody) > maxAPIResponse {
		return nil, invalidAPIResponse()
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return nil, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeOwnerAPIFailure(response.StatusCode, responseBody)
	}
	if response.StatusCode != http.StatusCreated {
		return nil, invalidAPIResponse()
	}
	data, meta, err := decodeStrictAPISuccess(responseBody, destination)
	if err != nil {
		return nil, err
	}
	if destination == nil || !validMediaUploadAPIData(data, *destination) {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}

func validMediaUploadAPIData(data json.RawMessage, result media.UploadResult) bool {
	if !hasRequiredAPIDataFields(data, "media", "preview", "original_url", "preview_url") ||
		!hasRequiredNestedAPIDataFields(data, "media", "id", "sha256", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "unreferenced_since", "animated_original") ||
		!hasRequiredNestedAPIDataFields(data, "preview", "recipe_version", "sha256", "mime_type", "width", "height", "animated_original") {
		return false
	}
	id, err := domain.ParseID(result.Media.ID.String())
	if err != nil || id != result.Media.ID || result.Media.OriginalName == "" || !validAPIResponseSHA256(result.Media.SHA256) ||
		!controlledMediaType(result.Media.MIMEType) || result.Media.ByteSize <= 0 || result.Media.ByteSize > media.DefaultMaxBytes ||
		result.Media.Width == 0 || result.Media.Height == 0 || !validCanonicalAPITime(result.Media.CreatedAt) ||
		(result.Media.UnreferencedSince != nil && !validCanonicalAPITimePointer(result.Media.UnreferencedSince)) {
		return false
	}
	if result.Preview.RecipeVersion == "" || !validAPIResponseSHA256(result.Preview.SHA256) || !controlledMediaType(result.Preview.MIMEType) ||
		result.Preview.Width == 0 || result.Preview.Height == 0 || result.Preview.AnimatedOriginal != result.Media.AnimatedOriginal {
		return false
	}
	return result.OriginalURL == "/api/v1/media/"+id.String() && result.PreviewURL == "/api/v1/media/"+id.String()+"/preview"
}

func hasRequiredNestedAPIDataFields(data json.RawMessage, object string, required ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	return hasRequiredAPIDataFields(fields[object], required...)
}

func validAPIResponseSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

type cliContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader cliContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}

func mediaAPIDownload(ctx context.Context, invocation Invocation, id domain.ID) (media.Asset, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return media.Asset{}, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/media/"+id.String())
	if err != nil {
		return media.Asset{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return media.Asset{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return media.Asset{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return media.Asset{}, invalidAPIResponse()
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
		if readErr != nil {
			return media.Asset{}, readErr
		}
		if len(body) > maxAPIResponse {
			return media.Asset{}, invalidAPIResponse()
		}
		if !validAPIJSONContentType(response.Header.Get("Content-Type")) {
			return media.Asset{}, invalidAPIResponse()
		}
		return media.Asset{}, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	mimeType := response.Header.Get("Content-Type")
	if !controlledMediaType(mimeType) {
		return media.Asset{}, invalidAPIResponse()
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, media.DefaultMaxBytes+1))
	if err != nil {
		return media.Asset{}, err
	}
	if int64(len(contents)) > media.DefaultMaxBytes {
		return media.Asset{}, invalidAPIResponse()
	}
	digest := sha256.Sum256(contents)
	sha := hex.EncodeToString(digest[:])
	etag := response.Header.Get("ETag")
	if etag != `"sha256-`+sha+`"` {
		return media.Asset{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the media API checksum did not match its immutable ETag", "verify the server or reverse proxy before retrying", ExitIntegrity)
	}
	if length := response.Header.Get("Content-Length"); length != "" {
		expected, err := strconv.ParseInt(length, 10, 64)
		if err != nil || expected != int64(len(contents)) {
			return media.Asset{}, invalidAPIResponse()
		}
	}
	return media.Asset{Contents: contents, MIMEType: mimeType, ETag: etag}, nil
}

func controlledMediaType(value string) bool {
	switch strings.TrimSpace(strings.Split(value, ";")[0]) {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func writeNewFileAtomic(ctx context.Context, destination string, contents []byte) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(destination))
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(absolute)
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return "", fmt.Errorf("output directory is unavailable")
	}
	partial, err := os.CreateTemp(directory, ".moment-media-*.partial")
	if err != nil {
		return "", err
	}
	partialPath := partial.Name()
	defer func() { _ = os.Remove(partialPath) }()
	if err := partial.Chmod(0o600); err != nil {
		_ = partial.Close()
		return "", err
	}
	if err := ctx.Err(); err != nil {
		_ = partial.Close()
		return "", err
	}
	if _, err := partial.Write(contents); err != nil {
		_ = partial.Close()
		return "", err
	}
	if err := partial.Sync(); err != nil {
		_ = partial.Close()
		return "", err
	}
	if err := partial.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := runtimefs.PublishPreparedFileNoReplace(partialPath, absolute); err != nil {
		return "", fmt.Errorf("output path must not already exist: %w", err)
	}
	return absolute, nil
}

func mediaCLIError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return NewCommandError("MEDIA_IO_FAILED", "the media operation could not access its input or storage", "check the selected paths and instance, then retry", ExitIO).WithCause(err)
	}
	return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
}
