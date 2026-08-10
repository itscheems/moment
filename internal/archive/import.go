package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	defaultMaxArchiveBytes   = int64(32 << 30)
	defaultMaxExpandedBytes  = int64(32 << 30)
	defaultMaxEntries        = int64(100_000)
	defaultMaxComments       = int64(500_000)
	defaultMaxMedia          = int64(100_000)
	defaultMaxCompression    = uint64(1_000)
	defaultInspectionTTL     = 30 * time.Minute
	minimumSafetyMarginBytes = int64(2 << 30)
	stagingMetadataLimit     = int64(16 << 10)
)

const (
	stagingReady      = "ready"
	stagingCommitting = "committing"
	stagingUsed       = "used"
	stagingFailed     = "failed"
)

// InspectLimits bounds every attacker-controlled archive dimension before a
// ZIP member is materialized. Zero values select the product ceilings.
type InspectLimits struct {
	MaxArchiveBytes  int64
	MaxExpandedBytes int64
	MaxEntries       int64
	MaxComments      int64
	MaxMedia         int64
	MaxCompression   uint64
}

// InspectOptions supplies runtime identity and deterministic test seams.
type InspectOptions struct {
	DataDir        string
	Limits         InspectLimits
	TokenTTL       time.Duration
	Clock          func() time.Time
	Random         io.Reader
	AvailableBytes func(string) (uint64, error)
}

// ImportCounts are portable record counts safe for Web and CLI preview.
type ImportCounts struct {
	Entries  int64 `json:"entries"`
	Comments int64 `json:"comments"`
	Media    int64 `json:"media"`
}

// ImportTimeRange describes the published range that will replace the site.
type ImportTimeRange struct {
	From *time.Time `json:"from,omitempty"`
	To   *time.Time `json:"to,omitempty"`
}

// ReplacementImpact describes current authority data that commit would
// replace. Inspection itself leaves these rows and files untouched.
type ReplacementImpact struct {
	Counts     ImportCounts `json:"counts"`
	MediaBytes int64        `json:"media_bytes"`
	DataBytes  int64        `json:"data_bytes"`
}

// DiskEstimate is the stable pre-import peak-space contract.
type DiskEstimate struct {
	ArchiveBytes                int64 `json:"archive_bytes"`
	ExpandedBytes               int64 `json:"expanded_bytes"`
	StagingBytes                int64 `json:"staging_bytes"`
	PreImportBackupBytes        int64 `json:"pre_import_backup_bytes"`
	RollbackReserveBytes        int64 `json:"rollback_reserve_bytes"`
	SafetyMarginBytes           int64 `json:"safety_margin_bytes"`
	RequiredAdditionalBytes     int64 `json:"required_additional_bytes"`
	EstimatedPeakFootprintBytes int64 `json:"estimated_peak_footprint_bytes"`
	AvailableBytes              int64 `json:"available_bytes"`
	SpaceOK                     bool  `json:"space_ok"`
}

// Inspection is the complete read-only replacement preview. StagingToken is
// returned only by the successful inspection call and is never persisted.
type Inspection struct {
	FormatVersion int               `json:"format_version"`
	ArchiveSHA256 string            `json:"archive_sha256"`
	GenerationID  string            `json:"generation_id"`
	Counts        ImportCounts      `json:"counts"`
	TimeRange     ImportTimeRange   `json:"time_range"`
	MediaBytes    int64             `json:"media_bytes"`
	MissingAlt    int64             `json:"missing_alt"`
	Disk          DiskEstimate      `json:"disk"`
	Replacement   ReplacementImpact `json:"replacement"`
	Warnings      []string          `json:"warnings"`
	ExpiresAt     time.Time         `json:"expires_at"`
	StagingToken  string            `json:"staging_token,omitempty"`
}

// StagingClaim is the one-time, already verified handoff consumed by import
// commit. Claiming does not switch CURRENT or mutate active authority data.
type StagingClaim struct {
	GenerationID string
	ArchivePath  string
	Generation   runtimefs.Generation
	MetadataPath string
}

type stagingMetadata struct {
	TokenHash    string `json:"token_hash"`
	ExpiresAtMS  int64  `json:"expires_at_ms"`
	ArchiveSHA   string `json:"archive_sha256"`
	InstanceID   string `json:"instance_id"`
	SessionEpoch uint64 `json:"session_epoch"`
	GenerationID string `json:"generation_id"`
	State        string `json:"state"`
}

// Inspector validates hostile v1 archives and builds isolated, restartable
// current-schema staging generations.
type Inspector struct {
	dataDir        string
	limits         InspectLimits
	tokenTTL       time.Duration
	clock          func() time.Time
	random         io.Reader
	availableBytes func(string) (uint64, error)
}

// NewInspector constructs an import inspection use case.
func NewInspector(options InspectOptions) (*Inspector, error) {
	limits := options.Limits
	if limits.MaxArchiveBytes == 0 {
		limits.MaxArchiveBytes = defaultMaxArchiveBytes
	}
	if limits.MaxExpandedBytes == 0 {
		limits.MaxExpandedBytes = defaultMaxExpandedBytes
	}
	if limits.MaxEntries == 0 {
		limits.MaxEntries = defaultMaxEntries
	}
	if limits.MaxComments == 0 {
		limits.MaxComments = defaultMaxComments
	}
	if limits.MaxMedia == 0 {
		limits.MaxMedia = defaultMaxMedia
	}
	if limits.MaxCompression == 0 {
		limits.MaxCompression = defaultMaxCompression
	}
	if limits.MaxArchiveBytes < 1 || limits.MaxArchiveBytes > defaultMaxArchiveBytes ||
		limits.MaxExpandedBytes < 1 || limits.MaxExpandedBytes > defaultMaxExpandedBytes ||
		limits.MaxEntries < 1 || limits.MaxEntries > defaultMaxEntries ||
		limits.MaxComments < 1 || limits.MaxComments > defaultMaxComments ||
		limits.MaxMedia < 1 || limits.MaxMedia > defaultMaxMedia ||
		limits.MaxCompression < 1 || limits.MaxCompression > defaultMaxCompression {
		return nil, errors.New("import inspection limits must be positive and no greater than product ceilings")
	}
	if options.TokenTTL == 0 {
		options.TokenTTL = defaultInspectionTTL
	}
	if options.TokenTTL <= 0 || options.TokenTTL > defaultInspectionTTL {
		return nil, errors.New("import staging token TTL must be positive and no greater than 30 minutes")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.AvailableBytes == nil {
		options.AvailableBytes = runtimefs.AvailableBytes
	}
	return &Inspector{
		dataDir: options.DataDir, limits: limits, tokenTTL: options.TokenTTL,
		clock: options.Clock, random: options.Random, availableBytes: options.AvailableBytes,
	}, nil
}

// InspectFile securely opens one regular non-symlink local archive.
func (inspector *Inspector) InspectFile(ctx context.Context, archivePath string, sessionEpoch uint64) (Inspection, error) {
	if strings.TrimSpace(archivePath) == "" || strings.ContainsRune(archivePath, 0) {
		return Inspection{}, domain.NewError(domain.CodeInvalidArgument, "archive path is required", map[string]any{"field": "archive"})
	}
	before, err := os.Lstat(archivePath)
	if err != nil {
		return Inspection{}, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return Inspection{}, domain.NewError(domain.CodeExportInvalid, "archive input must be a regular non-symlink file", nil)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return Inspection{}, err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return Inspection{}, errors.Join(domain.NewError(domain.CodeExportInvalid, "archive input changed while opening", nil), err)
	}
	return inspector.Inspect(ctx, file, sessionEpoch)
}

// Inspect streams one archive into an isolated staging directory, validates it
// completely, rebuilds current schema and FTS, and finally publishes a ready
// token hash. Active CURRENT data is never opened for mutation.
func (inspector *Inspector) Inspect(ctx context.Context, source io.Reader, sessionEpoch uint64) (result Inspection, resultErr error) {
	if inspector == nil || source == nil {
		return Inspection{}, errors.New("import inspector and archive input are required")
	}
	if sessionEpoch == 0 {
		return Inspection{}, domain.NewError(domain.CodeUnauthorized, "owner session epoch is required for import inspection", nil)
	}
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	runtimeState, err := runtimefs.OpenContext(ctx, inspector.dataDir)
	if err != nil {
		return Inspection{}, err
	}
	lease, err := runtimeState.Locks.Acquire(ctx, runtimefs.ImportLock, runtimefs.Exclusive)
	if err != nil {
		return Inspection{}, fmt.Errorf("acquire import inspection lock: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Release()) }()
	current, err := runtimeState.ResolveCurrent()
	if err != nil {
		return Inspection{}, err
	}
	runtimeState.Current = current
	now := inspector.clock().UTC().Truncate(time.Millisecond)
	if now.IsZero() {
		return Inspection{}, errors.New("import inspection clock returned zero time")
	}
	if err := inspector.cleanupAndCheckActive(runtimeState.Root, now); err != nil {
		return Inspection{}, err
	}

	generationID, stageRoot, err := inspector.createStage(runtimeState.Root, now)
	if err != nil {
		return Inspection{}, err
	}
	metadata := stagingMetadata{
		ExpiresAtMS: now.Add(inspector.tokenTTL).UnixMilli(), InstanceID: runtimeState.Instance.ID,
		SessionEpoch: sessionEpoch, GenerationID: generationID, State: stagingFailed,
	}
	metadataWritten := false
	ready := false
	defer func() {
		if ready {
			return
		}
		if !metadataWritten {
			metadataWritten = writeStagingMetadata(stageRoot, metadata) == nil
		} else {
			_ = writeStagingMetadata(stageRoot, metadata)
		}
	}()

	archivePath := filepath.Join(stageRoot, "archive.zip")
	archiveBytes, archiveSHA, err := inspector.copyArchive(ctx, source, archivePath)
	if err != nil {
		return Inspection{}, err
	}
	metadata.ArchiveSHA = archiveSHA
	if err := writeStagingMetadata(stageRoot, metadata); err != nil {
		return Inspection{}, err
	}
	metadataWritten = true

	document, err := inspector.preflight(ctx, archivePath)
	if err != nil {
		return Inspection{}, err
	}
	if err := ValidateFileContext(ctx, archivePath); err != nil {
		return Inspection{}, err
	}
	portable, err := summarizePortable(ctx, archivePath, document)
	if err != nil {
		return Inspection{}, importInvalid("portable archive summary is invalid", err)
	}
	replacement, err := currentReplacementImpact(ctx, runtimeState)
	if err != nil {
		return Inspection{}, importInvalid("current replacement impact could not be inspected", err)
	}
	disk, err := inspector.estimateDisk(runtimeState.Root, archiveBytes, document.Limits.ExpandedBytes, portable.mediaBytes, replacement)
	if err != nil {
		return Inspection{}, err
	}
	if !disk.SpaceOK {
		return Inspection{}, domain.NewError(domain.CodeImportDiskSpaceInsufficient, "available disk space is below the import preflight requirement", diskDetails(disk))
	}

	generation, err := createStagingGeneration(stageRoot, generationID)
	if err != nil {
		return Inspection{}, err
	}
	if err := rebuildPortableGeneration(ctx, runtimeState, generation, archivePath, document); err != nil {
		return Inspection{}, importInvalid("portable archive could not rebuild the current schema", err)
	}

	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(inspector.random, tokenBytes); err != nil {
		return Inspection{}, fmt.Errorf("generate import staging token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	metadata.TokenHash = hex.EncodeToString(tokenHash[:])
	metadata.State = stagingReady
	if err := writeStagingMetadata(stageRoot, metadata); err != nil {
		return Inspection{}, err
	}
	ready = true
	warnings := make([]string, 0, 1)
	if portable.missingAlt > 0 {
		warnings = append(warnings, fmt.Sprintf("%d media usages have no image description", portable.missingAlt))
	}
	return Inspection{
		FormatVersion: FormatVersion, ArchiveSHA256: archiveSHA, GenerationID: generationID,
		Counts: portable.counts, TimeRange: portable.timeRange, MediaBytes: portable.mediaBytes,
		MissingAlt: portable.missingAlt, Disk: disk, Replacement: replacement,
		Warnings: warnings, ExpiresAt: time.UnixMilli(metadata.ExpiresAtMS).UTC(), StagingToken: token,
	}, nil
}

// Resume validates an unexpired ready token and every persisted binding after
// process restart without consuming the token.
func (inspector *Inspector) Resume(ctx context.Context, token string, sessionEpoch uint64) (Inspection, error) {
	state, document, stageRoot, lease, err := inspector.loadReady(ctx, token, sessionEpoch)
	if err != nil {
		return Inspection{}, err
	}
	defer func() { _ = lease.Release() }()
	portable, err := summarizePortable(ctx, filepath.Join(stageRoot, "archive.zip"), document)
	if err != nil {
		return Inspection{}, importStagingInvalid("staged portable summary is invalid", err)
	}
	return Inspection{
		FormatVersion: FormatVersion, ArchiveSHA256: state.ArchiveSHA, GenerationID: state.GenerationID,
		Counts: portable.counts, TimeRange: portable.timeRange, MediaBytes: portable.mediaBytes,
		MissingAlt: portable.missingAlt, ExpiresAt: time.UnixMilli(state.ExpiresAtMS).UTC(),
	}, nil
}

// Claim atomically changes one verified ready token to committing. A second
// claim or any restart attempt against committing/used/failed state is rejected.
func (inspector *Inspector) Claim(ctx context.Context, token string, sessionEpoch uint64) (StagingClaim, error) {
	state, _, stageRoot, lease, err := inspector.loadReady(ctx, token, sessionEpoch)
	if err != nil {
		return StagingClaim{}, err
	}
	defer func() { _ = lease.Release() }()
	state.State = stagingCommitting
	if err := writeStagingMetadata(stageRoot, state); err != nil {
		return StagingClaim{}, err
	}
	generation := stagingGeneration(stageRoot, state.GenerationID)
	return StagingClaim{
		GenerationID: state.GenerationID, ArchivePath: filepath.Join(stageRoot, "archive.zip"),
		Generation: generation, MetadataPath: filepath.Join(stageRoot, "staging.json"),
	}, nil
}

func (inspector *Inspector) copyArchive(ctx context.Context, source io.Reader, destination string) (size int64, digestHex string, resultErr error) {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("create staged archive: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	digest := sha256.New()
	limited := io.LimitReader(contextReader{ctx: ctx, reader: source}, inspector.limits.MaxArchiveBytes+1)
	size, err = io.CopyBuffer(io.MultiWriter(file, digest), limited, make([]byte, copyBufferSize))
	if err != nil {
		return 0, "", err
	}
	if size > inspector.limits.MaxArchiveBytes {
		return 0, "", domain.NewError(domain.CodeExportInvalid, "archive exceeds the compressed byte budget", map[string]any{"max_archive_bytes": inspector.limits.MaxArchiveBytes})
	}
	if err := file.Sync(); err != nil {
		return 0, "", fmt.Errorf("sync staged archive: %w", err)
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

func (inspector *Inspector) preflight(ctx context.Context, archivePath string) (manifest, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return manifest{}, err
	}
	var magic [4]byte
	_, readErr := io.ReadFull(file, magic[:])
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(magic[:], []byte{'P', 'K', 3, 4}) {
		return manifest{}, importInvalid("archive ZIP magic bytes are invalid", errors.Join(readErr, closeErr))
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return manifest{}, importInvalid("archive ZIP central directory is invalid", err)
	}
	defer func() { _ = reader.Close() }()
	maxMembers := inspector.limits.MaxMedia + 7
	if int64(len(reader.File)) > maxMembers || len(reader.File) < 7 {
		return manifest{}, importInvalid("archive ZIP member count exceeds the inspection budget", nil)
	}
	seen := make(map[string]struct{}, len(reader.File))
	var expanded uint64
	var compressed uint64
	var checksumBytes uint64
	for _, member := range reader.File {
		if err := ctx.Err(); err != nil {
			return manifest{}, err
		}
		if err := validateImportMemberName(member.Name); err != nil {
			return manifest{}, importInvalid("archive ZIP contains an unsafe member path", err)
		}
		if _, exists := seen[member.Name]; exists {
			return manifest{}, importInvalid("archive ZIP contains a duplicate member path", nil)
		}
		seen[member.Name] = struct{}{}
		mode := member.Mode()
		if !mode.IsRegular() || mode&os.ModeSymlink != 0 || mode&os.ModeType != 0 {
			return manifest{}, importInvalid("archive ZIP contains a link or non-regular member", nil)
		}
		if member.Method != zip.Store && member.Method != zip.Deflate {
			return manifest{}, importInvalid("archive ZIP uses an unsupported compression method", nil)
		}
		if member.CompressedSize64 > uint64(inspector.limits.MaxArchiveBytes)-min(compressed, uint64(inspector.limits.MaxArchiveBytes)) {
			return manifest{}, importInvalid("archive ZIP compressed member bytes exceed the inspection budget", nil)
		}
		compressed += member.CompressedSize64
		if member.Name == "checksums.sha256" {
			checksumBudget := uint64(maxMembers) * 160
			if member.UncompressedSize64 > checksumBudget-checksumBytes {
				return manifest{}, importInvalid("archive ZIP checksums exceed the inspection budget", nil)
			}
			checksumBytes += member.UncompressedSize64
		} else {
			if member.UncompressedSize64 > uint64(inspector.limits.MaxExpandedBytes)-min(expanded, uint64(inspector.limits.MaxExpandedBytes)) {
				return manifest{}, importInvalid("archive ZIP expanded bytes exceed the inspection budget", nil)
			}
			expanded += member.UncompressedSize64
		}
		if member.UncompressedSize64 > 0 {
			if member.CompressedSize64 == 0 || member.UncompressedSize64/member.CompressedSize64 > inspector.limits.MaxCompression {
				return manifest{}, importInvalid("archive ZIP compression ratio exceeds the inspection budget", nil)
			}
		}
	}
	manifestFile := findZIPMember(reader.File, "manifest.json")
	data, err := readSmallZIPMember(ctx, manifestFile, 64<<10)
	if err != nil {
		return manifest{}, importInvalid("archive manifest is unreadable", err)
	}
	var document manifest
	fields, err := decodePortableJSONObject(data, &document, []string{"format", "format_version", "created_at", "source", "counts", "limits"})
	if err != nil {
		return manifest{}, importInvalid("archive manifest is invalid", err)
	}
	if err := requireNonNull(fields, "format", "format_version", "created_at", "source", "counts", "limits"); err != nil {
		return manifest{}, importInvalid("archive manifest is invalid", err)
	}
	if document.Format != formatName {
		return manifest{}, importInvalid("archive format identifier is unsupported", nil)
	}
	if document.FormatVersion != FormatVersion {
		return manifest{}, domain.NewError(domain.CodeExportVersionUnsupported, "archive format version is not supported", map[string]any{"format_version": document.FormatVersion, "supported_version": FormatVersion})
	}
	if document.Counts.Entries < 0 || document.Counts.Entries > inspector.limits.MaxEntries ||
		document.Counts.Comments < 0 || document.Counts.Comments > inspector.limits.MaxComments ||
		document.Counts.Media < 0 || document.Counts.Media > inspector.limits.MaxMedia ||
		document.Limits.ExpandedBytes < 1 || document.Limits.ExpandedBytes > inspector.limits.MaxExpandedBytes {
		return manifest{}, importInvalid("archive manifest counts or expanded bytes exceed the inspection budget", nil)
	}
	return document, nil
}

func validateImportMemberName(name string) error {
	if err := validateMemberName(name); err != nil {
		return err
	}
	first := name
	if index := strings.IndexByte(first, '/'); index >= 0 {
		first = first[:index]
	}
	if strings.Contains(first, ":") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return errors.New("archive member path is absolute or volume-qualified")
	}
	return nil
}

func findZIPMember(files []*zip.File, name string) *zip.File {
	for _, file := range files {
		if file.Name == name {
			return file
		}
	}
	return nil
}

type portableSummary struct {
	counts     ImportCounts
	timeRange  ImportTimeRange
	mediaBytes int64
	missingAlt int64
}

func summarizePortable(ctx context.Context, archivePath string, document manifest) (portableSummary, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return portableSummary{}, err
	}
	defer func() { _ = reader.Close() }()
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	result := portableSummary{counts: ImportCounts{Entries: document.Counts.Entries, Comments: document.Counts.Comments, Media: document.Counts.Media}}
	_, err = validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], []string{"id", "kind", "content_md", "created_at", "updated_at", "published_at", "media"}, func(value entryRecord, _ map[string]json.RawMessage) error {
		published, err := parsePortableTimestamp(value.PublishedAt)
		if err != nil {
			return err
		}
		if result.timeRange.From == nil || published.Before(*result.timeRange.From) {
			publishedAt := published
			result.timeRange.From = &publishedAt
		}
		if result.timeRange.To == nil || published.After(*result.timeRange.To) {
			publishedAt := published
			result.timeRange.To = &publishedAt
		}
		for _, usage := range value.Media {
			if usage.AltText == nil {
				result.missingAlt++
			}
		}
		return nil
	})
	if err != nil {
		return portableSummary{}, err
	}
	_, err = validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at"}, func(value mediaRecord, _ map[string]json.RawMessage) error {
		if value.ByteSize > math.MaxInt64-result.mediaBytes {
			return errors.New("portable media byte count overflows")
		}
		result.mediaBytes += value.ByteSize
		return nil
	})
	return result, err
}

func currentReplacementImpact(ctx context.Context, runtimeState *runtimefs.Runtime) (ReplacementImpact, error) {
	database, err := sqlitestore.OpenSearchReadOnly(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return ReplacementImpact{}, err
	}
	defer func() { _ = database.Close() }()
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		return ReplacementImpact{}, err
	}
	defer func() { _ = snapshot.Close() }()
	impact, err := snapshot.Impact(ctx)
	if err != nil {
		return ReplacementImpact{}, err
	}
	dataBytes, err := regularTreeBytes(ctx, runtimeState.Current.Root)
	if err != nil {
		return ReplacementImpact{}, err
	}
	return ReplacementImpact{
		Counts:     ImportCounts{Entries: impact.Entries, Comments: impact.Comments, Media: impact.Media},
		MediaBytes: impact.MediaBytes, DataBytes: dataBytes,
	}, nil
}

func regularTreeBytes(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("active generation contains a symlink")
		}
		if info.Mode().IsRegular() {
			if info.Size() > math.MaxInt64-total {
				return errors.New("active generation byte count overflows")
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func (inspector *Inspector) estimateDisk(root string, archiveBytes, expandedBytes, portableMediaBytes int64, replacement ReplacementImpact) (DiskEstimate, error) {
	available, err := inspector.availableBytes(root)
	if err != nil {
		return DiskEstimate{}, fmt.Errorf("inspect import disk space: %w", err)
	}
	availableBytes := int64(math.MaxInt64)
	if available <= math.MaxInt64 {
		availableBytes = int64(available)
	}
	if portableMediaBytes < 0 || portableMediaBytes > expandedBytes {
		return DiskEstimate{}, importInvalid("portable media bytes exceed expanded bytes", nil)
	}
	// Originals are copied once. JSON authority also becomes SQLite tables,
	// indexes, normalized text, and FTS, so reserve twice its portable bytes.
	stagingBytes, err := checkedAdd(portableMediaBytes, 2*(expandedBytes-portableMediaBytes))
	if err != nil {
		return DiskEstimate{}, importInvalid("import staging estimate overflows", err)
	}
	base, err := checkedAdd(stagingBytes, replacement.DataBytes, replacement.DataBytes)
	if err != nil {
		return DiskEstimate{}, importInvalid("import disk estimate overflows", err)
	}
	margin := base / 10
	if margin < minimumSafetyMarginBytes {
		margin = minimumSafetyMarginBytes
	}
	required, err := checkedAdd(base, margin)
	if err != nil {
		return DiskEstimate{}, importInvalid("import disk estimate overflows", err)
	}
	peak, err := checkedAdd(archiveBytes, replacement.DataBytes, required)
	if err != nil {
		return DiskEstimate{}, importInvalid("import peak disk estimate overflows", err)
	}
	return DiskEstimate{
		ArchiveBytes: archiveBytes, ExpandedBytes: expandedBytes, StagingBytes: stagingBytes,
		PreImportBackupBytes: replacement.DataBytes, RollbackReserveBytes: replacement.DataBytes,
		SafetyMarginBytes: margin, RequiredAdditionalBytes: required,
		EstimatedPeakFootprintBytes: peak, AvailableBytes: availableBytes, SpaceOK: availableBytes >= required,
	}, nil
}

func checkedAdd(values ...int64) (int64, error) {
	var result int64
	for _, value := range values {
		if value < 0 || value > math.MaxInt64-result {
			return 0, errors.New("byte count overflows")
		}
		result += value
	}
	return result, nil
}

func diskDetails(value DiskEstimate) map[string]any {
	encoded, _ := json.Marshal(value)
	var details map[string]any
	_ = json.Unmarshal(encoded, &details)
	return details
}

func createStagingGeneration(stageRoot, generationID string) (runtimefs.Generation, error) {
	generation := stagingGeneration(stageRoot, generationID)
	if err := os.Mkdir(generation.Root, 0o700); err != nil {
		return runtimefs.Generation{}, fmt.Errorf("create staging generation: %w", err)
	}
	if err := os.MkdirAll(generation.OriginalsDir, 0o700); err != nil {
		return runtimefs.Generation{}, fmt.Errorf("create staging original directory: %w", err)
	}
	database, err := os.OpenFile(generation.DatabasePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return runtimefs.Generation{}, fmt.Errorf("create staging database: %w", err)
	}
	if err := errors.Join(database.Sync(), database.Close()); err != nil {
		return runtimefs.Generation{}, fmt.Errorf("sync staging database: %w", err)
	}
	for _, directory := range []string{generation.OriginalsDir, generation.MediaRoot, generation.Root} {
		if err := runtimefs.SyncDirectory(directory); err != nil {
			return runtimefs.Generation{}, fmt.Errorf("sync staging generation directory: %w", err)
		}
	}
	return generation, nil
}

func stagingGeneration(stageRoot, generationID string) runtimefs.Generation {
	root := filepath.Join(stageRoot, "generation")
	mediaRoot := filepath.Join(root, "media")
	return runtimefs.Generation{
		ID: generationID, Root: root, DatabasePath: filepath.Join(root, "moment.db"),
		MediaRoot: mediaRoot, OriginalsDir: filepath.Join(mediaRoot, "original"),
	}
}

func rebuildPortableGeneration(ctx context.Context, parent *runtimefs.Runtime, generation runtimefs.Generation, archivePath string, document manifest) (resultErr error) {
	stagingRuntime := &runtimefs.Runtime{Root: parent.Root, Instance: parent.Instance, Current: generation, Locks: parent.Locks}
	database, err := sqlitestore.Open(ctx, stagingRuntime, sqlitestore.Options{})
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, database.Close())
		}
	}()
	importer, err := database.BeginPortableImport(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			resultErr = errors.Join(resultErr, importer.Close())
		}
	}()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, reader.Close()) }()
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}

	_, err = validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at"}, func(value mediaRecord, _ map[string]json.RawMessage) error {
		created, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return err
		}
		id, err := domain.ParseID(value.ID)
		if err != nil {
			return err
		}
		storagePath, err := portableStoragePath(value.SHA256, value.MIMEType)
		if err != nil {
			return err
		}
		metadata := sqlitestore.ArchiveMedia{
			ID: value.ID, SHA256: value.SHA256, StoragePath: storagePath,
			OriginalName: value.OriginalName, MIMEType: value.MIMEType, ByteSize: value.ByteSize,
			Width: value.Width, Height: value.Height, CreatedAtMS: created.UnixMilli(),
		}
		if err := importer.InsertMedia(ctx, metadata); err != nil {
			return err
		}
		member := files[value.ArchivePath]
		if member == nil {
			return errors.New("portable media member is missing")
		}
		return materializePortableOriginal(ctx, member, generation, media.Media{
			ID: id, SHA256: value.SHA256, StoragePath: storagePath, OriginalName: value.OriginalName,
			MIMEType: value.MIMEType, ByteSize: value.ByteSize, Width: value.Width, Height: value.Height, CreatedAt: created,
		})
	})
	if err != nil {
		return err
	}

	_, err = validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], []string{"id", "kind", "content_md", "created_at", "updated_at", "published_at", "media"}, func(value entryRecord, _ map[string]json.RawMessage) error {
		created, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return err
		}
		updated, err := parsePortableTimestamp(value.UpdatedAt)
		if err != nil {
			return err
		}
		published, err := parsePortableTimestamp(value.PublishedAt)
		if err != nil {
			return err
		}
		usages := make([]sqlitestore.ArchiveUsage, len(value.Media))
		for index, usage := range value.Media {
			usages[index] = sqlitestore.ArchiveUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
		}
		return importer.InsertEntry(ctx, sqlitestore.ArchiveEntry{
			ID: value.ID, Kind: value.Kind, ContentMD: value.ContentMD,
			CreatedAtMS: created.UnixMilli(), UpdatedAtMS: updated.UnixMilli(), PublishedAtMS: published.UnixMilli(), Media: usages,
		})
	})
	if err != nil {
		return err
	}

	_, err = validateJSONLines[commentRecord](ctx, files["data/comments.jsonl"], []string{"id", "entry_id", "parent_id", "author_kind", "author_name", "content_md", "created_at"}, func(value commentRecord, _ map[string]json.RawMessage) error {
		created, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return err
		}
		return importer.InsertComment(ctx, sqlitestore.ArchiveComment{
			ID: value.ID, EntryID: value.EntryID, ParentID: value.ParentID, AuthorKind: value.AuthorKind,
			AuthorName: value.AuthorName, ContentMD: value.ContentMD, CreatedAtMS: created.UnixMilli(),
		})
	})
	if err != nil {
		return err
	}

	siteData, err := readSmallZIPMember(ctx, files["data/site.json"], maxPortableRecordBytes)
	if err != nil {
		return err
	}
	var site siteRecord
	if _, err := decodePortableJSONObject(siteData, &site, []string{"title", "bio", "timezone", "public_base_url", "avatar_media_id", "comments_enabled"}); err != nil {
		return err
	}
	if err := validateArchiveSite(sqlitestore.ArchiveSite{
		Title: site.Title, Bio: site.Bio, Timezone: site.Timezone, PublicBaseURL: site.PublicBaseURL,
		AvatarMediaID: site.AvatarMediaID, CommentsEnabled: site.CommentsEnabled,
	}, parent.IsDevelopment()); err != nil {
		return err
	}
	if err := importer.SetSite(ctx, sqlitestore.ArchiveSite{
		Title: site.Title, Bio: site.Bio, Timezone: site.Timezone, PublicBaseURL: site.PublicBaseURL,
		AvatarMediaID: site.AvatarMediaID, CommentsEnabled: site.CommentsEnabled,
	}); err != nil {
		return err
	}
	expected := sqlitestore.ArchiveCounts{Entries: document.Counts.Entries, Comments: document.Counts.Comments, Media: document.Counts.Media}
	if err := importer.Commit(ctx, expected); err != nil {
		return err
	}
	committed = true
	if err := database.Close(); err != nil {
		return err
	}
	closed = true

	verified, err := sqlitestore.Open(ctx, stagingRuntime, sqlitestore.Options{})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, verified.Close()) }()
	if err := verified.ValidatePortableReady(ctx, expected); err != nil {
		return err
	}
	snapshot, err := verified.BeginArchiveSnapshot(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	return snapshot.ValidateIntegrity(ctx)
}

func portableStoragePath(sha, mimeType string) (string, error) {
	extension := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif"}[mimeType]
	if len(sha) != 64 || !isLowerHex(sha) || extension == "" {
		return "", errors.New("portable media storage identity is invalid")
	}
	return "media/original/" + sha[:2] + "/" + sha + extension, nil
}

func materializePortableOriginal(ctx context.Context, member *zip.File, generation runtimefs.Generation, value media.Media) (resultErr error) {
	destination := filepath.Join(generation.Root, filepath.FromSlash(value.StoragePath))
	relative, err := filepath.Rel(generation.Root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("portable media destination escapes staging generation")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	partial, err := os.CreateTemp(filepath.Dir(destination), ".import-original-*")
	if err != nil {
		return err
	}
	partialPath := partial.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(partialPath)
		}
	}()
	source, err := member.Open()
	if err != nil {
		_ = partial.Close()
		return err
	}
	copied, copyErr := io.CopyBuffer(partial, io.LimitReader(contextReader{ctx: ctx, reader: source}, value.ByteSize+1), make([]byte, copyBufferSize))
	closeSourceErr := source.Close()
	if copyErr != nil || closeSourceErr != nil || copied != value.ByteSize {
		_ = partial.Close()
		return errors.Join(copyErr, closeSourceErr, errors.New("portable media copy did not match declared size"))
	}
	if err := partial.Sync(); err != nil {
		_ = partial.Close()
		return err
	}
	if _, err := partial.Seek(0, io.SeekStart); err != nil {
		_ = partial.Close()
		return err
	}
	if err := media.VerifyPortableOriginal(ctx, partial, value); err != nil {
		_ = partial.Close()
		return err
	}
	if err := partial.Close(); err != nil {
		return err
	}
	if err := runtimefs.PublishPreparedFileNoReplace(partialPath, destination); err != nil {
		return err
	}
	published = true
	return nil
}

func (inspector *Inspector) createStage(root string, now time.Time) (string, string, error) {
	parent := filepath.Join(root, "staging", "import")
	for attempts := 0; attempts < 8; attempts++ {
		randomBytes := make([]byte, 8)
		if _, err := io.ReadFull(inspector.random, randomBytes); err != nil {
			return "", "", err
		}
		id := "import-" + now.Format("20060102T150405000Z") + "-" + hex.EncodeToString(randomBytes)
		stageRoot := filepath.Join(parent, id)
		if err := os.Mkdir(stageRoot, 0o700); err == nil {
			if err := runtimefs.SyncDirectory(parent); err != nil {
				return "", "", fmt.Errorf("sync import staging parent: %w", err)
			}
			return id, stageRoot, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("create import staging directory: %w", err)
		}
	}
	return "", "", errors.New("could not allocate a unique import staging generation")
}

func (inspector *Inspector) cleanupAndCheckActive(root string, now time.Time) error {
	parent := filepath.Join(root, "staging", "import")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		stageRoot := filepath.Join(parent, name)
		info, err := os.Lstat(stageRoot)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasPrefix(name, "import-") {
			return importStagingInvalid("import staging contains an unsafe entry", nil)
		}
		metadata, err := readStagingMetadata(stageRoot)
		if errors.Is(err, fs.ErrNotExist) {
			if err := removeStagingDirectory(parent, stageRoot); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return importStagingInvalid("import staging metadata is corrupt", err)
		}
		switch metadata.State {
		case stagingFailed, stagingUsed:
			if err := removeStagingDirectory(parent, stageRoot); err != nil {
				return err
			}
		case stagingReady:
			if metadata.ExpiresAtMS <= now.UnixMilli() {
				if err := removeStagingDirectory(parent, stageRoot); err != nil {
					return err
				}
				continue
			}
			return domain.NewError(domain.CodeImportInProgress, "an unexpired import inspection is already staged", map[string]any{"expires_at": time.UnixMilli(metadata.ExpiresAtMS).UTC()})
		case stagingCommitting:
			return domain.NewError(domain.CodeImportInProgress, "an import staging generation is already claimed", nil)
		default:
			return importStagingInvalid("import staging state is invalid", nil)
		}
	}
	return nil
}

func removeStagingDirectory(parent, stageRoot string) error {
	if err := os.RemoveAll(stageRoot); err != nil {
		return err
	}
	return runtimefs.SyncDirectory(parent)
}

func (inspector *Inspector) loadReady(ctx context.Context, token string, sessionEpoch uint64) (_ stagingMetadata, _ manifest, _ string, lease *runtimefs.Lease, resultErr error) {
	if token == "" || len(token) > 256 || sessionEpoch == 0 {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("staging token or owner epoch is invalid", nil)
	}
	runtimeState, err := runtimefs.OpenContext(ctx, inspector.dataDir)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, err
	}
	heldLease, err := runtimeState.Locks.Acquire(ctx, runtimefs.ImportLock, runtimefs.Exclusive)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			resultErr = errors.Join(resultErr, heldLease.Release())
		}
	}()
	parent := filepath.Join(runtimeState.Root, "staging", "import")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, err
	}
	var stageRoot string
	var state stagingMetadata
	for _, entry := range entries {
		candidate := filepath.Join(parent, entry.Name())
		info, infoErr := os.Lstat(candidate)
		if infoErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), "import-") {
			return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("import staging contains an unsafe entry", infoErr)
		}
		metadata, err := readStagingMetadata(candidate)
		if err != nil {
			return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("import staging metadata is corrupt", err)
		}
		if metadata.State == stagingReady || metadata.State == stagingCommitting || metadata.State == stagingUsed {
			if stageRoot != "" {
				return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("multiple active import staging records exist", nil)
			}
			stageRoot, state = candidate, metadata
		}
	}
	if stageRoot == "" {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("no matching ready import staging exists", nil)
	}
	if state.State != stagingReady {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("import staging token was already claimed or used", nil)
	}
	now := inspector.clock().UTC().Truncate(time.Millisecond)
	if state.ExpiresAtMS <= now.UnixMilli() {
		return stagingMetadata{}, manifest{}, "", nil, domain.NewError(domain.CodeImportStagingExpired, "import staging token has expired", map[string]any{"expired_at": time.UnixMilli(state.ExpiresAtMS).UTC()})
	}
	if state.InstanceID != runtimeState.Instance.ID || state.SessionEpoch != sessionEpoch || state.GenerationID != filepath.Base(stageRoot) {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("import staging binding does not match this instance and owner epoch", nil)
	}
	expectedHash, err := hex.DecodeString(state.TokenHash)
	actualHash := sha256.Sum256([]byte(token))
	if err != nil || len(expectedHash) != sha256.Size || subtle.ConstantTimeCompare(expectedHash, actualHash[:]) != 1 {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("import staging token does not match", nil)
	}
	archivePath := filepath.Join(stageRoot, "archive.zip")
	actualArchiveHash, err := fileSHA256(ctx, archivePath, inspector.limits.MaxArchiveBytes)
	if err != nil || actualArchiveHash != state.ArchiveSHA {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("staged archive hash does not match metadata", err)
	}
	document, err := inspector.preflight(ctx, archivePath)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("staged archive no longer passes preflight", err)
	}
	if err := ValidateFileContext(ctx, archivePath); err != nil {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("staged archive no longer validates", err)
	}
	generation := stagingGeneration(stageRoot, state.GenerationID)
	if err := validateStagingGeneration(ctx, runtimeState, generation, document); err != nil {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("staging generation no longer validates", err)
	}
	succeeded = true
	return state, document, stageRoot, heldLease, nil
}

func validateStagingGeneration(ctx context.Context, parent *runtimefs.Runtime, generation runtimefs.Generation, document manifest) error {
	for _, target := range []struct {
		name string
		dir  bool
	}{{generation.Root, true}, {generation.MediaRoot, true}, {generation.OriginalsDir, true}, {generation.DatabasePath, false}} {
		info, err := os.Lstat(target.name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || target.dir != info.IsDir() || !target.dir && !info.Mode().IsRegular() {
			return errors.Join(errors.New("staging generation structure is invalid"), err)
		}
	}
	runtimeState := &runtimefs.Runtime{Root: parent.Root, Instance: parent.Instance, Current: generation, Locks: parent.Locks}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	return database.ValidatePortableReady(ctx, sqlitestore.ArchiveCounts{
		Entries: document.Counts.Entries, Comments: document.Counts.Comments, Media: document.Counts.Media,
	})
}

func writeStagingMetadata(stageRoot string, value stagingMetadata) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return runtimefs.WritePrivateFileAtomic(stageRoot, "staging.json", data)
}

func readStagingMetadata(stageRoot string) (stagingMetadata, error) {
	metadataPath := filepath.Join(stageRoot, "staging.json")
	info, err := os.Lstat(metadataPath)
	if err != nil {
		return stagingMetadata{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > stagingMetadataLimit {
		return stagingMetadata{}, errors.New("staging metadata file is unsafe")
	}
	file, err := os.Open(metadataPath)
	if err != nil {
		return stagingMetadata{}, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, stagingMetadataLimit+1))
	if err != nil || int64(len(data)) > stagingMetadataLimit || len(data) == 0 || data[len(data)-1] != '\n' {
		return stagingMetadata{}, errors.New("staging metadata bytes are invalid")
	}
	if err := validateJSONObjectTokens(data[:len(data)-1]); err != nil {
		return stagingMetadata{}, errors.New("staging metadata JSON is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value stagingMetadata
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return stagingMetadata{}, errors.New("staging metadata JSON is invalid")
	}
	if value.GenerationID != filepath.Base(stageRoot) || value.InstanceID == "" || value.SessionEpoch == 0 || value.ExpiresAtMS <= 0 ||
		(value.ArchiveSHA != "" && (len(value.ArchiveSHA) != 64 || !isLowerHex(value.ArchiveSHA))) ||
		(value.TokenHash != "" && (len(value.TokenHash) != 64 || !isLowerHex(value.TokenHash))) {
		return stagingMetadata{}, errors.New("staging metadata binding is invalid")
	}
	if value.State != stagingFailed && (len(value.ArchiveSHA) != 64 || len(value.TokenHash) != 64) {
		return stagingMetadata{}, errors.New("active staging metadata is incomplete")
	}
	return value, nil
}

func fileSHA256(ctx context.Context, name string, limit int64) (string, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() < 1 || before.Size() > limit {
		return "", errors.New("staged archive file is unsafe")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return "", errors.Join(errors.New("staged archive changed while opening"), err)
	}
	digest := sha256.New()
	if _, err := io.CopyBuffer(digest, contextReader{ctx: ctx, reader: file}, make([]byte, copyBufferSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func importInvalid(message string, cause error) error {
	return errors.Join(domain.NewError(domain.CodeExportInvalid, message, nil), cause)
}

func importStagingInvalid(message string, cause error) error {
	return errors.Join(domain.NewError(domain.CodeImportStagingInvalid, message, nil), cause)
}
