package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
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
