package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerBackup(application *Application, info buildinfo.Info) {
	application.MustRegisterLeaf(LeafSpec{
		Path:   []string{"backup", "export"},
		Short:  "Export one consistent portable archive",
		Long:   "Stream Moment export format v1 from one logical SQLite and reachable-media snapshot through direct or authenticated API mode.",
		Inputs: "Choose exactly one destination: --output <zip> for atomic file publication or --stdout for raw ZIP bytes. Direct file output must be outside the selected --data-dir. --stdout is mutually exclusive with --json. API mode authenticates through MOMENT_TOKEN or --token-file.",
		Output: "File mode validates v1 then reports path, format_version, archive_bytes, and archive_sha256 in the normal human/JSON contract. --stdout fail-closes on the HTTP format header before writing only ZIP bytes; a transport failure after the first byte leaves an intentionally truncated stdout stream and diagnostics only on stderr.",
		Errors: "Invalid output flags use exit 2; unsafe or unavailable destinations use exit 6; existing destinations without --overwrite use OUTPUT_EXISTS; data/checksum failures use exit 7; cancellation and timeout use exit 9.",
		Safety: "File output is prepared and flushed beside the destination, then atomically published. Direct output inside --data-dir is rejected before creating a partial. --overwrite replaces only the exact regular non-symlink destination and never weakens archive validation.",
		Example: "moment backup export --data-dir ./moment-data --output ./moment-export.zip --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./owner.token backup export --stdout > moment-export.zip",
		Capability: CapabilityBoth,
		RawStdout:  true,
		Flags: func(flags *pflag.FlagSet) {
			registerSingleStringFlag(flags, "output", "atomically publish the ZIP at this path")
			registerSingleBoolFlag(flags, "stdout", "write raw ZIP bytes to stdout")
			registerSingleBoolFlag(flags, "overwrite", "atomically replace an existing regular output file")
		},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupExport(ctx, invocation, input, info.Version)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"backup", "inspect"},
		ArgsUsage: "<archive.zip>",
		Short:     "Inspect a portable archive into restartable staging",
		Long:      "Treat one Moment export as hostile input, validate it completely, rebuild the current schema and search index in a restartable staging generation, and issue one expiring commit token without changing active data.",
		Inputs:    "Exactly one regular non-symlink v1 ZIP path. Direct mode stages locally and binds the result to the current owner epoch. API mode streams application/zip to the authenticated server. --timeout bounds upload, validation, media checks, migrations, and index rebuild.",
		Output:    "The normal human/JSON contract reports format, counts, time range, media bytes, missing-alt warnings, replacement impact, the complete peak disk estimate, expiry, and the one-time staging token. Save the token for backup import confirmation.",
		Errors:    "Hostile or corrupt archives use stable integrity errors and exit 7; unavailable input or disk uses exit 6; an active inspection uses IMPORT_IN_PROGRESS; cancellation uses exit 9.",
		Safety:    "Inspection never changes CURRENT or active media. ZIP paths, links, duplicates, counts, sizes, ratios, checksums, records, references, and image bytes are bounded before staging members become authority candidates.",
		Example: "moment backup inspect ./moment-export.zip --data-dir ./moment-data --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./owner.token backup inspect ./moment-export.zip --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run:        runBackupInspect,
	})
}

func runBackupInspect(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	archivePath := input.Args()[0]
	var result archive.Inspection
	var meta map[string]any
	var err error
	switch invocation.Mode() {
	case ModeDirect:
		target, targetErr := invocation.DirectTransport()
		if targetErr != nil {
			return Result{}, targetErr
		}
		runtimeState, openErr := runtimefs.OpenContext(ctx, target.DataDirectory())
		if openErr != nil {
			return Result{}, backupInspectCLIError(openErr)
		}
		status, statusErr := auth.ReadStatus(runtimeState.Root)
		if statusErr != nil {
			return Result{}, ownerAuthError(statusErr)
		}
		if !status.Initialized || status.SessionEpoch == 0 {
			return Result{}, ownerAuthError(auth.ErrNotInitialized)
		}
		inspector, createErr := archive.NewInspector(archive.InspectOptions{DataDir: runtimeState.Root})
		if createErr != nil {
			return Result{}, createErr
		}
		result, err = inspector.InspectFile(ctx, archivePath, status.SessionEpoch)
	case ModeAPI:
		result, meta, err = uploadImportInspection(ctx, invocation, archivePath)
	default:
		return Result{}, errors.New("unsupported backup inspect transport")
	}
	if err != nil {
		return Result{}, backupInspectCLIError(err)
	}
	text := fmt.Sprintf("Archive inspected into restartable staging\nFormat: v%d\nEntries: %d\nComments: %d\nMedia: %d\nRequired additional bytes: %d\nAvailable bytes: %d\nExpires: %s\nStaging token: %s",
		result.FormatVersion, result.Counts.Entries, result.Counts.Comments, result.Counts.Media,
		result.Disk.RequiredAdditionalBytes, result.Disk.AvailableBytes, result.ExpiresAt.Format(time.RFC3339), result.StagingToken)
	return Result{Data: result, Text: text, Meta: meta}, nil
}

func uploadImportInspection(ctx context.Context, invocation Invocation, archivePath string) (archive.Inspection, map[string]any, error) {
	before, err := os.Lstat(archivePath)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return archive.Inspection{}, nil, invalidArgument("archive input must be a regular non-symlink file", "provide one complete Moment export ZIP")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return archive.Inspection{}, nil, errors.Join(invalidArgument("archive input changed while opening", "retry with a stable regular non-symlink ZIP"), err)
	}
	target, err := invocation.APITransport()
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/import/inspect")
	if err != nil {
		return archive.Inspection{}, nil, invalidArgument("the import inspection endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, file)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	request.ContentLength = after.Size()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/zip")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if len(body) > maxAPIResponse {
		return archive.Inspection{}, nil, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusCreated {
		return archive.Inspection{}, nil, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "application/json" {
		return archive.Inspection{}, nil, invalidAPIResponse()
	}
	inspection, meta, err := decodeImportInspectionAPI(body, after.Size())
	if err != nil {
		return archive.Inspection{}, nil, invalidAPIResponse()
	}
	return inspection, meta, nil
}

func decodeImportInspectionAPI(body []byte, expectedArchiveBytes int64) (archive.Inspection, map[string]any, error) {
	if !utf8.Valid(body) {
		return archive.Inspection{}, nil, errors.New("import inspection response is not valid UTF-8")
	}
	envelope, err := decodeExactJSONObject(body, "data", "meta")
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	data, err := decodeExactJSONObject(envelope["data"],
		"format_version", "archive_sha256", "generation_id", "counts", "time_range", "media_bytes", "missing_alt",
		"disk", "replacement", "warnings", "expires_at", "staging_token",
	)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if _, err := decodeExactJSONObject(data["counts"], "entries", "comments", "media"); err != nil {
		return archive.Inspection{}, nil, err
	}
	timeRange, err := decodeImportTimeRangeObject(data["time_range"])
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if _, err := decodeExactJSONObject(data["disk"],
		"archive_bytes", "expanded_bytes", "staging_bytes", "pre_import_backup_bytes", "rollback_reserve_bytes",
		"safety_margin_bytes", "required_additional_bytes", "estimated_peak_footprint_bytes", "available_bytes", "space_ok",
	); err != nil {
		return archive.Inspection{}, nil, err
	}
	replacement, err := decodeExactJSONObject(data["replacement"], "counts", "media_bytes", "data_bytes")
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if _, err := decodeExactJSONObject(replacement["counts"], "entries", "comments", "media"); err != nil {
		return archive.Inspection{}, nil, err
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	var inspection archive.Inspection
	decoder := json.NewDecoder(bytes.NewReader(envelope["data"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inspection); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return archive.Inspection{}, nil, errors.New("import inspection data cannot be decoded exactly")
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if err := validateImportInspectionAPI(inspection, data, timeRange, expectedArchiveBytes); err != nil {
		return archive.Inspection{}, nil, err
	}
	return inspection, map[string]any{"request_id": requestID}, nil
}

func decodeExactJSONObject(raw []byte, expectedKeys ...string) (map[string]json.RawMessage, error) {
	expected := make(map[string]struct{}, len(expectedKeys))
	for _, key := range expectedKeys {
		if key == "" {
			return nil, errors.New("empty expected JSON key")
		}
		expected[key] = struct{}{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("JSON value is not an object")
	}
	fields := make(map[string]json.RawMessage, len(expectedKeys))
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("JSON object key is invalid")
		}
		if _, allowed := expected[key]; !allowed {
			return nil, errors.New("JSON object contains an unknown key")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errors.New("JSON object contains a duplicate key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("JSON object value is invalid")
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(fields) != len(expected) {
		return nil, errors.New("JSON object is incomplete")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("JSON object contains trailing data")
	}
	return fields, nil
}

func decodeImportTimeRangeObject(raw []byte) (map[string]json.RawMessage, error) {
	if empty, err := decodeExactJSONObject(raw); err == nil {
		return empty, nil
	}
	return decodeExactJSONObject(raw, "from", "to")
}

func decodeImportRequestID(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) < 5 || len(value) > 128 || !strings.HasPrefix(value, "req_") {
		return "", errors.New("import inspection request ID is invalid")
	}
	for _, character := range value[4:] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return "", errors.New("import inspection request ID is invalid")
		}
	}
	return value, nil
}

func validateImportInspectionAPI(value archive.Inspection, raw map[string]json.RawMessage, timeRange map[string]json.RawMessage, expectedArchiveBytes int64) error {
	if value.FormatVersion != archive.FormatVersion || !validLowerHex(value.ArchiveSHA256, sha256.Size) || !validImportGenerationID(value.GenerationID) {
		return errors.New("import inspection identity is invalid")
	}
	if !validImportCounts(value.Counts, true) || !validImportCounts(value.Replacement.Counts, false) || value.MediaBytes < 0 || value.MissingAlt < 0 || value.Replacement.MediaBytes < 0 || value.Replacement.DataBytes < 0 || value.Replacement.MediaBytes > value.Replacement.DataBytes {
		return errors.New("import inspection counts are invalid")
	}
	if value.Counts.Entries == 0 {
		if value.Counts.Comments != 0 || value.MissingAlt != 0 || len(timeRange) != 0 || value.TimeRange.From != nil || value.TimeRange.To != nil {
			return errors.New("empty import inspection has a time range")
		}
	} else {
		if len(timeRange) != 2 || value.TimeRange.From == nil || value.TimeRange.To == nil {
			return errors.New("non-empty import inspection has no complete time range")
		}
		from, err := decodeCanonicalUTCTime(timeRange["from"])
		if err != nil {
			return err
		}
		to, err := decodeCanonicalUTCTime(timeRange["to"])
		if err != nil || from.After(to) || !value.TimeRange.From.Equal(from) || !value.TimeRange.To.Equal(to) {
			return errors.New("import inspection time range is invalid")
		}
	}
	expiresAt, err := decodeCanonicalUTCTime(raw["expires_at"])
	if err != nil || !value.ExpiresAt.Equal(expiresAt) {
		return errors.New("import inspection expiry is invalid")
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(value.StagingToken)
	if err != nil || len(token) != sha256.Size || base64.RawURLEncoding.EncodeToString(token) != value.StagingToken {
		return errors.New("import inspection staging token is invalid")
	}
	if value.Warnings == nil || len(value.Warnings) > 1 {
		return errors.New("import inspection warnings are invalid")
	}
	if value.MissingAlt == 0 {
		if len(value.Warnings) != 0 {
			return errors.New("import inspection warnings do not match missing alt count")
		}
	} else if len(value.Warnings) != 1 || value.Warnings[0] != fmt.Sprintf("%d media usages have no image description", value.MissingAlt) {
		return errors.New("import inspection warnings do not match missing alt count")
	}
	return validateImportDiskEstimate(value, expectedArchiveBytes)
}

func validLowerHex(value string, decodedBytes int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == decodedBytes && hex.EncodeToString(decoded) == value
}

func validImportGenerationID(value string) bool {
	const prefix = "import-"
	const timestampLength = len("20060102T150405000Z")
	if len(value) != len(prefix)+timestampLength+1+16 || !strings.HasPrefix(value, prefix) || value[len(prefix)+timestampLength] != '-' {
		return false
	}
	timestamp := value[len(prefix) : len(prefix)+timestampLength]
	parsed, err := time.Parse("20060102T150405000Z", timestamp)
	return err == nil && parsed.Location() == time.UTC && parsed.Format("20060102T150405000Z") == timestamp && validLowerHex(value[len(prefix)+timestampLength+1:], 8)
}

func validImportCounts(value archive.ImportCounts, enforcePortableLimits bool) bool {
	if value.Entries < 0 || value.Comments < 0 || value.Media < 0 {
		return false
	}
	return !enforcePortableLimits || value.Entries <= 100_000 && value.Comments <= 500_000 && value.Media <= 100_000
}

func decodeCanonicalUTCTime(raw json.RawMessage) (time.Time, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || !strings.HasSuffix(text, "Z") {
		return time.Time{}, errors.New("timestamp is not a canonical UTC string")
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || value.IsZero() || value.Location() != time.UTC || value.Format(time.RFC3339Nano) != text {
		return time.Time{}, errors.New("timestamp is not canonical UTC")
	}
	return value, nil
}

func validateImportDiskEstimate(value archive.Inspection, expectedArchiveBytes int64) error {
	disk := value.Disk
	if expectedArchiveBytes <= 0 || disk.ArchiveBytes != expectedArchiveBytes || disk.ExpandedBytes <= 0 || value.MediaBytes > disk.ExpandedBytes || !disk.SpaceOK {
		return errors.New("import inspection disk estimate has invalid base values")
	}
	jsonBytes := disk.ExpandedBytes - value.MediaBytes
	staging, err := checkedImportResponseAdd(value.MediaBytes, jsonBytes, jsonBytes)
	if err != nil || disk.StagingBytes != staging || disk.PreImportBackupBytes != value.Replacement.DataBytes || disk.RollbackReserveBytes != value.Replacement.DataBytes {
		return errors.New("import inspection staging estimate is invalid")
	}
	base, err := checkedImportResponseAdd(staging, value.Replacement.DataBytes, value.Replacement.DataBytes)
	if err != nil {
		return err
	}
	margin := base / 10
	if margin < int64(2<<30) {
		margin = int64(2 << 30)
	}
	required, err := checkedImportResponseAdd(base, margin)
	if err != nil || disk.SafetyMarginBytes != margin || disk.RequiredAdditionalBytes != required || disk.AvailableBytes < required {
		return errors.New("import inspection required disk estimate is invalid")
	}
	peak, err := checkedImportResponseAdd(disk.ArchiveBytes, value.Replacement.DataBytes, required)
	if err != nil || disk.EstimatedPeakFootprintBytes != peak {
		return errors.New("import inspection peak disk estimate is invalid")
	}
	return nil
}

func checkedImportResponseAdd(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 || value > int64(^uint64(0)>>1)-total {
			return 0, errors.New("import inspection byte count overflows")
		}
		total += value
	}
	return total, nil
}

func backupInspectCLIError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		exit := ExitIntegrity
		suggestion := "repair or re-export the archive, then inspect it again"
		switch domainErr.Code {
		case domain.CodeInvalidArgument:
			exit = ExitInvalidInput
		case domain.CodeUnauthorized:
			exit = ExitUnauthorized
		case domain.CodeImportInProgress, domain.CodeImportStagingExpired:
			exit = ExitConflict
			suggestion = "finish, discard, or wait for the existing staging inspection before retrying"
		case domain.CodeImportDiskSpaceInsufficient:
			exit = ExitIO
			suggestion = "free the reported additional bytes on the Moment data filesystem, then retry"
		}
		return NewCommandError(string(domainErr.Code), domainErr.Message, suggestion, exit).WithDetails(domainErr.Details).WithCause(err)
	}
	if errors.Is(err, runtimefs.ErrIncompleteLayout) || errors.Is(err, runtimefs.ErrInvalidInstance) || errors.Is(err, runtimefs.ErrInvalidCurrent) {
		return NewCommandError("INSTANCE_NOT_READY", "the Moment data directory is not a complete readable instance", "initialize or repair the selected data directory before retrying import inspection", ExitIO).WithCause(err)
	}
	return err
}

func runBackupExport(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	output, err := input.String("output")
	if err != nil {
		return Result{}, err
	}
	stdout, err := input.Bool("stdout")
	if err != nil {
		return Result{}, err
	}
	overwrite, err := input.Bool("overwrite")
	if err != nil {
		return Result{}, err
	}
	outputSelected := input.Changed("output")
	if outputSelected == stdout {
		return Result{}, invalidArgument("choose exactly one of --output or --stdout", "provide a destination path or explicitly select raw stdout")
	}
	if outputSelected && (strings.TrimSpace(output) == "" || strings.ContainsRune(output, 0)) {
		return Result{}, invalidArgument("--output must name a ZIP file", "provide a non-empty local destination path")
	}
	if stdout && invocation.Options().JSON {
		return Result{}, invalidArgument("--stdout cannot be combined with --json", "remove --json or use --output for a machine-readable result")
	}
	if stdout && overwrite {
		return Result{}, invalidArgument("--overwrite only applies to --output", "remove --overwrite when streaming to stdout")
	}
	if outputSelected && invocation.Mode() == ModeDirect {
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		if err := ensureBackupOutputOutsideDataDir(output, target.DataDirectory()); err != nil {
			return Result{}, err
		}
	}

	producer, err := backupProducer(ctx, invocation, appVersion)
	if err != nil {
		return Result{}, backupCLIError(err)
	}
	if stdout {
		return Result{Binary: func(destination io.Writer) error {
			_, err := producer(ctx, destination)
			return backupCLIError(err)
		}}, nil
	}
	summary, err := archive.WriteFile(ctx, output, overwrite, producer)
	if err != nil {
		return Result{}, backupCLIError(err)
	}
	return backupResult(summary), nil
}

func ensureBackupOutputOutsideDataDir(output, dataDir string) error {
	root, err := runtimefs.ResolveDataDir(dataDir)
	if err != nil {
		return backupOutputUnavailable(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return backupOutputUnavailable(err)
	}
	rootInfo, err := os.Stat(realRoot)
	if err != nil {
		return backupOutputUnavailable(err)
	}
	outputPath, err := filepath.Abs(filepath.Clean(output))
	if err != nil {
		return backupOutputUnavailable(err)
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(outputPath))
	if err != nil {
		return backupOutputUnavailable(err)
	}
	for candidate := realParent; ; candidate = filepath.Dir(candidate) {
		info, err := os.Stat(candidate)
		if err != nil {
			return backupOutputUnavailable(err)
		}
		if os.SameFile(rootInfo, info) {
			return NewCommandError("OUTPUT_UNAVAILABLE", "direct export output must be outside the selected Moment data directory", "choose an output path outside --data-dir and retry", ExitIO)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	return nil
}

func backupOutputUnavailable(cause error) error {
	return NewCommandError("OUTPUT_UNAVAILABLE", "the archive output path cannot be resolved safely", "choose an existing writable directory outside --data-dir and retry", ExitIO).WithCause(cause)
}

func backupProducer(_ context.Context, invocation Invocation, appVersion string) (func(context.Context, io.Writer) (archive.Summary, error), error) {
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return nil, err
		}
		exporter, err := archive.NewExporter(archive.Options{DataDir: target.DataDirectory(), AppVersion: appVersion})
		if err != nil {
			return nil, err
		}
		return exporter.Export, nil
	case ModeAPI:
		return func(operationContext context.Context, destination io.Writer) (archive.Summary, error) {
			return downloadExport(operationContext, invocation, destination)
		}, nil
	default:
		return nil, errors.New("unsupported backup export transport")
	}
}

func downloadExport(ctx context.Context, invocation Invocation, destination io.Writer) (archive.Summary, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return archive.Summary{}, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return archive.Summary{}, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/export")
	if err != nil {
		return archive.Summary{}, invalidArgument("the export endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return archive.Summary{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/zip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.Summary{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 200 && response.StatusCode < 300 && response.StatusCode != http.StatusOK {
		return archive.Summary{}, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
		if readErr != nil {
			return archive.Summary{}, readErr
		}
		if len(body) > maxAPIResponse {
			return archive.Summary{}, invalidAPIResponse()
		}
		return archive.Summary{}, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "application/zip" {
		return archive.Summary{}, invalidAPIResponse()
	}
	if response.Header.Get("X-Archive-Format") != archive.FormatHeaderValue {
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the server did not confirm Moment export format v1", "upgrade the server or inspect the reverse proxy before retrying", ExitUnavailable)
	}
	digest := sha256.New()
	counted := &backupDigestWriter{writer: io.MultiWriter(&backupDestinationWriter{writer: destination}, digest)}
	copied, err := io.CopyBuffer(counted, &backupContextReader{ctx: ctx, reader: response.Body}, make([]byte, 128<<10))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return archive.Summary{}, contextErr
		}
		var destinationErr *backupDestinationError
		if errors.As(err, &destinationErr) {
			return archive.Summary{}, fmt.Errorf("%w: %w", archive.ErrDestinationUnavailable, destinationErr.Unwrap())
		}
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the archive download ended unexpectedly", "retry the export and inspect the server or reverse proxy", ExitUnavailable).WithCause(err)
	}
	if response.ContentLength >= 0 && copied != response.ContentLength {
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the archive download ended before its declared length", "retry the export and inspect the server or reverse proxy", ExitUnavailable)
	}
	if copied == 0 {
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the archive download was empty", "retry the export and inspect the server or reverse proxy", ExitUnavailable)
	}
	return archive.Summary{FormatVersion: archive.FormatVersion, ArchiveBytes: counted.size, ArchiveSHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func backupResult(summary archive.Summary) Result {
	return Result{
		Data: summary,
		Text: fmt.Sprintf("Exported Moment archive to %s\nFormat: v%d\nBytes: %d\nSHA-256: %s", summary.Path, summary.FormatVersion, summary.ArchiveBytes, summary.ArchiveSHA256),
	}
}

func backupCLIError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, fs.ErrExist) {
		return NewCommandError("OUTPUT_EXISTS", "the archive output path already exists", "choose another --output path or explicitly add --overwrite", ExitConflict).WithCause(err)
	}
	if errors.Is(err, runtimefs.ErrIncompleteLayout) || errors.Is(err, runtimefs.ErrInvalidInstance) || errors.Is(err, runtimefs.ErrInvalidCurrent) || errors.Is(err, runtimefs.ErrEnvironmentMismatch) {
		return NewCommandError("INSTANCE_NOT_READY", "the Moment data directory is not a complete readable instance", "initialize or repair the selected data directory before retrying export", ExitIO).WithCause(err)
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		exit := ExitIntegrity
		suggestion := "run diagnostics and repair authoritative data before retrying export"
		if domainErr.Code == domain.CodeInvalidArgument {
			exit = ExitInvalidInput
			suggestion = "correct the export input and retry"
		}
		return NewCommandError(string(domainErr.Code), domainErr.Message, suggestion, exit).WithDetails(domainErr.Details).WithCause(err)
	}
	if errors.Is(err, archive.ErrDestinationUnavailable) {
		return NewCommandError("OUTPUT_UNAVAILABLE", "the archive output path cannot be published safely", "choose a writable file path with an existing directory, then retry", ExitIO).WithCause(err)
	}
	return err
}

type backupContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *backupContextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

type backupDigestWriter struct {
	writer io.Writer
	size   int64
}

type backupDestinationWriter struct{ writer io.Writer }

func (writer *backupDestinationWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return written, &backupDestinationError{cause: err}
	}
	return written, nil
}

type backupDestinationError struct{ cause error }

func (err *backupDestinationError) Error() string { return "archive destination write failed" }
func (err *backupDestinationError) Unwrap() error { return err.cause }

func (writer *backupDigestWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	writer.size += int64(written)
	return written, err
}

type singleStringFlag struct {
	value string
	set   bool
}

func (flag *singleStringFlag) Set(value string) error {
	if flag.set {
		return errors.New("flag may only be provided once")
	}
	flag.value, flag.set = value, true
	return nil
}
func (flag *singleStringFlag) String() string { return flag.value }
func (*singleStringFlag) Type() string        { return "string" }

type singleBoolFlag struct {
	value bool
	set   bool
}

func (flag *singleBoolFlag) Set(value string) error {
	if flag.set {
		return errors.New("flag may only be provided once")
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	flag.value, flag.set = parsed, true
	return nil
}
func (flag *singleBoolFlag) String() string { return strconv.FormatBool(flag.value) }
func (*singleBoolFlag) Type() string        { return "bool" }
func (*singleBoolFlag) IsBoolFlag() bool    { return true }

func registerSingleStringFlag(flags *pflag.FlagSet, name, usage string) {
	flags.Var(&singleStringFlag{}, name, usage)
}

func registerSingleBoolFlag(flags *pflag.FlagSet, name, usage string) {
	flags.Var(&singleBoolFlag{}, name, usage)
	flags.Lookup(name).NoOptDefVal = "true"
}
