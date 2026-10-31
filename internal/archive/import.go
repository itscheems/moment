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
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/mediatype"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	defaultMaxArchiveBytes   = int64(32 << 30)
	defaultMaxExpandedBytes  = int64(32 << 30)
	defaultMaxEntries        = int64(100_000)
	defaultMaxComments       = int64(500_000)
	defaultMaxMedia          = int64(100_000)
	defaultMaxUsers          = int64(100_000)
	defaultMaxIdentities     = int64(500_000)
	defaultMaxArticleDrafts  = int64(100_000)
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
	MaxUsers         int64
	MaxIdentities    int64
	// MaxArticleDrafts bounds unfinished articles, which are the one portable
	// record that is not an entry, a comment, a media object or an account and
	// so has no other budget to sit under.
	MaxArticleDrafts int64
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
	ActiveDatabase *sqlitestore.Database
	ImportLockWait time.Duration
}

// ImportCounts reports the complete portable entity graph considered for replacement.
type ImportCounts struct {
	Interactions int64 `json:"interactions,omitempty"`
	Entries      int64 `json:"entries"`
	Comments     int64 `json:"comments"`
	Media        int64 `json:"media"`
	Users        int64 `json:"users"`
	// Articles is a subset of Entries, since every article is one. It is
	// reported separately because a preview that says only "entries" tells
	// nobody how much of what is about to be replaced is long-form.
	Articles int64 `json:"articles"`
	// ArticleDrafts appears nowhere else. A replacement destroys unfinished
	// articles, and a preview that does not name them is under-reporting the
	// least recoverable thing on the site.
	ArticleDrafts int64 `json:"article_drafts"`
	Identities    int64 `json:"identities"`
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

// ImportPlan is the zero-persistence dry-run result. It contains the complete
// replacement and disk preview but deliberately excludes staging identity,
// expiry, and claim credentials.
type ImportPlan struct {
	DryRun        bool              `json:"dry_run"`
	FormatVersion int               `json:"format_version"`
	ArchiveSHA256 string            `json:"archive_sha256"`
	Counts        ImportCounts      `json:"counts"`
	TimeRange     ImportTimeRange   `json:"time_range"`
	MediaBytes    int64             `json:"media_bytes"`
	MissingAlt    int64             `json:"missing_alt"`
	Disk          DiskEstimate      `json:"disk"`
	Replacement   ReplacementImpact `json:"replacement"`
	Warnings      []string          `json:"warnings"`
}

// StagingClaim is the one-time, already verified handoff consumed by import
// commit. Claiming does not switch CURRENT or mutate active authority data.
type StagingClaim struct {
	GenerationID  string
	ArchivePath   string
	Generation    runtimefs.Generation
	MetadataPath  string
	StageRoot     string
	ArchiveSHA    string
	ExpiresAt     time.Time
	Authorization Authorization
	Counts        ImportCounts
	ExpandedBytes int64
	MediaBytes    int64
}

type stagingMetadata struct {
	TokenHash     string        `json:"token_hash"`
	ExpiresAtMS   int64         `json:"expires_at_ms"`
	ArchiveSHA    string        `json:"archive_sha256"`
	InstanceID    string        `json:"instance_id"`
	Authorization Authorization `json:"authorization"`
	GenerationID  string        `json:"generation_id"`
	State         string        `json:"state"`
}

// Inspector validates hostile v2 archives and builds isolated, restartable
// current-schema staging generations.
type Inspector struct {
	dataDir        string
	limits         InspectLimits
	tokenTTL       time.Duration
	clock          func() time.Time
	random         io.Reader
	availableBytes func(string) (uint64, error)
	activeDatabase *sqlitestore.Database
	importLockWait time.Duration
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
	if limits.MaxUsers == 0 {
		limits.MaxUsers = defaultMaxUsers
	}
	if limits.MaxIdentities == 0 {
		limits.MaxIdentities = defaultMaxIdentities
	}
	if limits.MaxArticleDrafts == 0 {
		limits.MaxArticleDrafts = defaultMaxArticleDrafts
	}
	if limits.MaxCompression == 0 {
		limits.MaxCompression = defaultMaxCompression
	}
	if limits.MaxArchiveBytes < 1 || limits.MaxArchiveBytes > defaultMaxArchiveBytes ||
		limits.MaxExpandedBytes < 1 || limits.MaxExpandedBytes > defaultMaxExpandedBytes ||
		limits.MaxEntries < 1 || limits.MaxEntries > defaultMaxEntries ||
		limits.MaxComments < 1 || limits.MaxComments > defaultMaxComments ||
		limits.MaxMedia < 1 || limits.MaxMedia > defaultMaxMedia ||
		limits.MaxUsers < 1 || limits.MaxUsers > defaultMaxUsers ||
		limits.MaxIdentities < 1 || limits.MaxIdentities > defaultMaxIdentities ||
		limits.MaxArticleDrafts < 1 || limits.MaxArticleDrafts > defaultMaxArticleDrafts ||
		limits.MaxCompression < 1 || limits.MaxCompression > defaultMaxCompression {
		return nil, errors.New("import inspection limits must be positive and no greater than product ceilings")
	}
	if options.TokenTTL == 0 {
		options.TokenTTL = defaultInspectionTTL
	}
	if options.TokenTTL <= 0 || options.TokenTTL > defaultInspectionTTL {
		return nil, errors.New("import staging token TTL must be positive and no greater than 30 minutes")
	}
	if options.ImportLockWait < 0 || options.ImportLockWait > 5*time.Second {
		return nil, errors.New("import inspection lock wait must be between zero and 5 seconds")
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
		activeDatabase: options.ActiveDatabase, importLockWait: options.ImportLockWait,
	}, nil
}

// InspectFile securely opens one regular non-symlink local archive.
func (inspector *Inspector) InspectFile(ctx context.Context, archivePath string, subject Authorization) (Inspection, error) {
	file, err := openArchiveInput(archivePath)
	if err != nil {
		return Inspection{}, err
	}
	defer func() { _ = file.Close() }()
	return inspector.Inspect(ctx, file, subject)
}

func openArchiveInput(archivePath string) (*os.File, error) {
	if strings.TrimSpace(archivePath) == "" || strings.ContainsRune(archivePath, 0) {
		return nil, domain.NewError(domain.CodeInvalidArgument, "archive path is required", map[string]any{"field": "archive"})
	}
	before, err := os.Lstat(archivePath)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, domain.NewError(domain.CodeExportInvalid, "archive input must be a regular non-symlink file", nil)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, errors.Join(domain.NewError(domain.CodeExportInvalid, "archive input changed while opening", nil), err)
	}
	return file, nil
}

// Inspect streams one archive into an isolated staging directory, validates it
// completely, rebuilds current schema and FTS, and finally publishes a ready
// token hash. Active CURRENT data is never opened for mutation.
func (inspector *Inspector) Inspect(ctx context.Context, source io.Reader, subject Authorization) (result Inspection, resultErr error) {
	if inspector == nil || source == nil {
		return Inspection{}, errors.New("import inspector and archive input are required")
	}
	if !subject.Valid() {
		return Inspection{}, domain.NewError(domain.CodeUnauthorized, "an account and credential are required for import inspection", nil)
	}
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	runtimeState, err := runtimefs.OpenForCoordination(inspector.dataDir)
	if err != nil {
		return Inspection{}, err
	}
	held, err := inspector.acquireImportSet(ctx, runtimeState)
	if err != nil {
		return Inspection{}, fmt.Errorf("acquire import inspection lock: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	return inspector.inspectWithinLocks(ctx, source, subject, runtimeState, held, true)
}

// Plan validates and rebuilds the archive with the same hostile-input path as
// Inspect, then durably removes its private staging directory before return.
// It never cleans or replaces a pre-existing staging operation.
func (inspector *Inspector) Plan(ctx context.Context, source io.Reader, subject Authorization) (result ImportPlan, resultErr error) {
	if inspector == nil || source == nil {
		return ImportPlan{}, errors.New("import inspector and archive input are required")
	}
	runtimeState, err := runtimefs.OpenForCoordination(inspector.dataDir)
	if err != nil {
		return ImportPlan{}, err
	}
	held, err := inspector.acquireImportSet(ctx, runtimeState)
	if err != nil {
		return ImportPlan{}, fmt.Errorf("acquire import plan lock: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	return inspector.planWithinLocks(ctx, source, subject, runtimeState, held)
}

func (inspector *Inspector) acquireImportSet(ctx context.Context, runtimeState *runtimefs.Runtime) (*runtimefs.LeaseSet, error) {
	lockContext := ctx
	cancel := func() {}
	if inspector.importLockWait > 0 {
		lockContext, cancel = context.WithTimeout(ctx, inspector.importLockWait)
	}
	defer cancel()
	held, err := runtimeState.Locks.AcquireSet(lockContext, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil && inspector.importLockWait > 0 && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, domain.NewError(domain.CodeImportInProgress, "another import operation owns the coordinator", nil)
	}
	return held, err
}

func (inspector *Inspector) planWithinLocks(ctx context.Context, source io.Reader, subject Authorization, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet) (result ImportPlan, resultErr error) {
	parent := filepath.Join(runtimeState.Root, "staging", "import")
	before, err := os.ReadDir(parent)
	if err != nil {
		return ImportPlan{}, err
	}
	baseline := make(map[string]struct{}, len(before))
	for _, entry := range before {
		baseline[entry.Name()] = struct{}{}
	}
	// Cleanup is detached from request cancellation and covers every exit after
	// a stage is allocated, including hostile ZIP, disk, rebuild, and timeout
	// failures. Only names absent from the locked baseline are eligible.
	defer func() {
		cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancelCleanup()
		if err := cleanupContext.Err(); err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		after, err := os.ReadDir(parent)
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		for _, entry := range after {
			if _, existed := baseline[entry.Name()]; existed {
				continue
			}
			stageRoot := filepath.Join(parent, entry.Name())
			if err := removeStagingDirectory(parent, stageRoot); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove dry-run import staging: %w", err))
				return
			}
		}
		verified, err := os.ReadDir(parent)
		if err != nil || len(verified) != len(baseline) {
			resultErr = errors.Join(resultErr, errors.New("dry-run import staging cleanup did not restore the original directory set"), err)
			return
		}
		for _, entry := range verified {
			if _, existed := baseline[entry.Name()]; !existed {
				resultErr = errors.Join(resultErr, errors.New("dry-run import staging cleanup left an unexpected entry"))
				return
			}
		}
	}()
	inspection, err := inspector.inspectWithinLocks(ctx, source, subject, runtimeState, held, false)
	if err != nil {
		return ImportPlan{}, err
	}
	return importPlanFromInspection(inspection), nil
}

func importPlanFromInspection(value Inspection) ImportPlan {
	return ImportPlan{
		DryRun: true, FormatVersion: value.FormatVersion, ArchiveSHA256: value.ArchiveSHA256,
		Counts: value.Counts, TimeRange: value.TimeRange, MediaBytes: value.MediaBytes, MissingAlt: value.MissingAlt,
		Disk: value.Disk, Replacement: value.Replacement, Warnings: value.Warnings,
	}
}

func (inspector *Inspector) inspectWithinLocks(ctx context.Context, source io.Reader, subject Authorization, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, cleanupExisting bool) (result Inspection, resultErr error) {
	if inspector == nil || source == nil || runtimeState == nil || held == nil ||
		!held.BelongsTo(runtimeState.Root) || !held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) {
		return Inspection{}, runtimefs.ErrInsufficientLocks
	}
	if !subject.Valid() {
		return Inspection{}, domain.NewError(domain.CodeUnauthorized, "an account and credential are required for import inspection", nil)
	}
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	current, err := runtimeState.ResolveCurrent()
	if err != nil {
		return Inspection{}, err
	}
	runtimeState.Current = current
	now := inspector.clock().UTC().Truncate(time.Millisecond)
	if now.IsZero() {
		return Inspection{}, errors.New("import inspection clock returned zero time")
	}
	if err := inspector.checkActive(runtimeState.Root, now, cleanupExisting); err != nil {
		return Inspection{}, err
	}

	generationID, stageRoot, err := inspector.createStage(runtimeState.Root, now)
	if err != nil {
		return Inspection{}, err
	}
	metadata := stagingMetadata{
		ExpiresAtMS: now.Add(inspector.tokenTTL).UnixMilli(), InstanceID: runtimeState.Instance.ID,
		Authorization: subject, GenerationID: generationID, State: stagingFailed,
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
	replacement, err := inspector.currentReplacementImpact(ctx, runtimeState, stageRoot)
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
	// Ready staging and dry-run plans must pass the exact same full authority
	// validation as commit revalidation. This rejects syntactically valid but
	// unreachable media before a token or green plan can be returned.
	if err := validateStagingGeneration(ctx, runtimeState, generation, archivePath, document); err != nil {
		return Inspection{}, importInvalid("portable archive does not form a complete replacement authority", err)
	}
	warnings := make([]string, 0, 1)
	if portable.missingAlt > 0 {
		warnings = append(warnings, fmt.Sprintf("%d media usages have no image description", portable.missingAlt))
	}
	inspection := Inspection{
		FormatVersion: document.FormatVersion, ArchiveSHA256: archiveSHA, GenerationID: generationID,
		Counts: portable.counts, TimeRange: portable.timeRange, MediaBytes: portable.mediaBytes,
		MissingAlt: portable.missingAlt, Disk: disk, Replacement: replacement, Warnings: warnings,
	}
	if !cleanupExisting {
		// Plan never even creates a usable token. Its failed-state private stage
		// is removed by planWithinLocks before the command can return.
		return inspection, nil
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
	inspection.ExpiresAt = time.UnixMilli(metadata.ExpiresAtMS).UTC()
	inspection.StagingToken = token
	return inspection, nil
}

// Resume validates an unexpired ready token and every persisted binding after
// process restart without consuming the token.
func (inspector *Inspector) Resume(ctx context.Context, token string, subject Authorization) (Inspection, error) {
	state, document, stageRoot, lease, err := inspector.loadReady(ctx, token, subject)
	if err != nil {
		return Inspection{}, err
	}
	defer func() { _ = lease.Release() }()
	portable, err := summarizePortable(ctx, filepath.Join(stageRoot, "archive.zip"), document)
	if err != nil {
		return Inspection{}, importStagingInvalid("staged portable summary is invalid", err)
	}
	return Inspection{
		FormatVersion: document.FormatVersion, ArchiveSHA256: state.ArchiveSHA, GenerationID: state.GenerationID,
		Counts: portable.counts, TimeRange: portable.timeRange, MediaBytes: portable.mediaBytes,
		MissingAlt: portable.missingAlt, ExpiresAt: time.UnixMilli(state.ExpiresAtMS).UTC(),
	}, nil
}

// Claim atomically changes one verified ready token to committing. A second
// claim or any restart attempt against committing/used/failed state is rejected.
func (inspector *Inspector) Claim(ctx context.Context, token string, subject Authorization) (StagingClaim, error) {
	state, document, stageRoot, lease, err := inspector.loadReady(ctx, token, subject)
	if err != nil {
		return StagingClaim{}, err
	}
	defer func() { _ = lease.Release() }()
	state.State = stagingCommitting
	if err := writeStagingMetadata(stageRoot, state); err != nil {
		return StagingClaim{}, err
	}
	return inspector.newClaim(ctx, state, document, stageRoot)
}

// ClaimWithinLocks consumes a ready token while the caller retains one
// continuous import.lock ownership for the complete commit state machine.
func (inspector *Inspector) ClaimWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, token string, subject Authorization, held *runtimefs.LeaseSet) (StagingClaim, error) {
	if inspector == nil || runtimeState == nil || held == nil || !held.BelongsTo(runtimeState.Root) || !held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) {
		return StagingClaim{}, runtimefs.ErrInsufficientLocks
	}
	state, document, stageRoot, err := inspector.loadReadyHeld(ctx, runtimeState, token, subject)
	if err != nil {
		return StagingClaim{}, err
	}
	state.State = stagingCommitting
	if err := writeStagingMetadata(stageRoot, state); err != nil {
		return StagingClaim{}, err
	}
	return inspector.newClaim(ctx, state, document, stageRoot)
}

func (inspector *Inspector) newClaim(ctx context.Context, state stagingMetadata, document manifest, stageRoot string) (StagingClaim, error) {
	portable, err := summarizePortable(ctx, filepath.Join(stageRoot, "archive.zip"), document)
	if err != nil {
		return StagingClaim{}, importStagingInvalid("staged portable summary is invalid", err)
	}
	return StagingClaim{
		GenerationID: state.GenerationID, ArchivePath: filepath.Join(stageRoot, "archive.zip"),
		Generation: stagingGeneration(stageRoot, state.GenerationID), MetadataPath: filepath.Join(stageRoot, "staging.json"),
		StageRoot: stageRoot, ArchiveSHA: state.ArchiveSHA, ExpiresAt: time.UnixMilli(state.ExpiresAtMS).UTC(),
		Authorization: state.Authorization,
		Counts:        portable.counts,
		ExpandedBytes: document.Limits.ExpandedBytes, MediaBytes: portable.mediaBytes,
	}, nil
}

// FinishClaim persists an unreplayable terminal state while import.lock is
// still held. Journal status remains available after staging cleanup.
func (inspector *Inspector) FinishClaim(claim StagingClaim, succeeded bool, held *runtimefs.LeaseSet) error {
	if inspector == nil || held == nil {
		return runtimefs.ErrInsufficientLocks
	}
	root, err := runtimefs.ResolveDataDir(inspector.dataDir)
	if err != nil {
		return err
	}
	expectedRoot := filepath.Join(root, "staging", "import", claim.GenerationID)
	actualRoot, err := filepath.Abs(filepath.Clean(claim.StageRoot))
	if err != nil || actualRoot != expectedRoot || !held.BelongsTo(root) || !held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) {
		return runtimefs.ErrInsufficientLocks
	}
	state, err := readStagingMetadata(claim.StageRoot)
	if err != nil {
		return err
	}
	if state.GenerationID != claim.GenerationID {
		return importStagingInvalid("claimed staging identity changed during commit", nil)
	}
	if (succeeded && state.State == stagingUsed) || (!succeeded && state.State == stagingFailed) {
		return nil
	}
	if state.State != stagingCommitting {
		return importStagingInvalid("claimed staging state changed during commit", nil)
	}
	if succeeded {
		state.State = stagingUsed
	} else {
		state.State = stagingFailed
	}
	return writeStagingMetadata(claim.StageRoot, state)
}

func (inspector *Inspector) discardReadyWithinLocks(runtimeState *runtimefs.Runtime, generationID string, held *runtimefs.LeaseSet) error {
	if inspector == nil || runtimeState == nil || held == nil || !validCommitGenerationID(generationID, true) ||
		!held.BelongsTo(runtimeState.Root) || !held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) {
		return runtimefs.ErrInsufficientLocks
	}
	stageRoot := filepath.Join(runtimeState.Root, "staging", "import", generationID)
	metadata, err := readStagingMetadata(stageRoot)
	if err != nil {
		return err
	}
	if metadata.GenerationID != generationID || metadata.State != stagingReady {
		return nil
	}
	return removeStagingDirectory(filepath.Dir(stageRoot), stageRoot)
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
	// The budget is stated against the largest v2 layout, and the floor
	// against the smallest, so all of them remain readable.
	maxMembers := inspector.limits.MaxMedia + int64(len(articlePortableMembers)+1+len(portableTrailerMembers))
	if int64(len(reader.File)) > maxMembers || len(reader.File) < len(articlePortableMembers)+len(portableTrailerMembers) {
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
	if !supportedSiteFormat(document.FormatVersion) {
		return manifest{}, domain.NewError(domain.CodeExportVersionUnsupported, "archive format version is not supported", map[string]any{"format_version": document.FormatVersion, "supported_version": FormatVersion})
	}
	// Articles are bounded by the entry budget because every article is an
	// entry. Drafts are not entries and therefore need a bound of their own, or
	// a hostile archive could declare a member of any length.
	if document.Counts.Entries < 0 || document.Counts.Entries > inspector.limits.MaxEntries ||
		document.Counts.Comments < 0 || document.Counts.Comments > inspector.limits.MaxComments ||
		document.Counts.Media < 0 || document.Counts.Media > inspector.limits.MaxMedia ||
		document.Counts.Users < 0 || document.Counts.Users > inspector.limits.MaxUsers ||
		document.Counts.Identities < 0 || document.Counts.Identities > inspector.limits.MaxIdentities ||
		document.Counts.Articles < 0 || document.Counts.Articles > document.Counts.Entries ||
		document.Counts.ArticleDrafts < 0 || document.Counts.ArticleDrafts > inspector.limits.MaxArticleDrafts ||
		document.Counts.Interactions < 0 || document.Counts.Interactions > maxPortableInteractions ||
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
	result := portableSummary{counts: portableImportCounts(document)}
	_, err = validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], []string{"id", "kind", "content_md", "created_at", "updated_at", "published_at", "media"}, func(value entryRecord, _ map[string]json.RawMessage) error {
		if _, err := domainLocation(value.Location); err != nil {
			return err
		}
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
	_, err = validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "grants"}, func(value mediaRecord, _ map[string]json.RawMessage) error {
		if value.ByteSize > math.MaxInt64-result.mediaBytes {
			return errors.New("portable media byte count overflows")
		}
		result.mediaBytes += value.ByteSize
		return nil
	})
	return result, err
}

type archiveSnapshotSource interface {
	BeginArchiveSnapshot(context.Context) (*sqlitestore.ArchiveSnapshot, error)
}

func (inspector *Inspector) currentReplacementImpact(ctx context.Context, runtimeState *runtimefs.Runtime, scratchRoot string) (result ReplacementImpact, resultErr error) {
	maintenance, err := runtimeState.Locks.Acquire(ctx, runtimefs.MaintenanceLock, runtimefs.Exclusive)
	if err != nil {
		return ReplacementImpact{}, fmt.Errorf("freeze replacement impact snapshot: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, maintenance.Release()) }()

	var source archiveSnapshotSource
	if inspector.activeDatabase != nil {
		if filepath.Clean(inspector.activeDatabase.Path()) != filepath.Clean(runtimeState.Current.DatabasePath) {
			return ReplacementImpact{}, errors.New("active database does not match the selected runtime generation")
		}
		source = inspector.activeDatabase
	} else {
		copied, cleanup, err := openActiveDatabaseSnapshot(ctx, runtimeState, scratchRoot)
		if err != nil {
			return ReplacementImpact{}, err
		}
		defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
		source = copied
	}
	snapshot, err := source.BeginArchiveSnapshot(ctx)
	if err != nil {
		return ReplacementImpact{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	impact, err := snapshot.Impact(ctx)
	if err != nil {
		return ReplacementImpact{}, err
	}
	dataBytes, err := regularTreeBytes(ctx, runtimeState.Current.Root)
	if err != nil {
		return ReplacementImpact{}, err
	}
	// These are the rows commit would delete, so the accounts already on this
	// site are counted here rather than the ones arriving in the archive.
	return ReplacementImpact{
		Counts: ImportCounts{
			Entries: impact.Entries, Comments: impact.Comments, Media: impact.Media,
			Users: impact.Users, Identities: impact.Identities,
			Articles: impact.Articles, ArticleDrafts: impact.ArticleDrafts,
			Interactions: impact.Interactions,
		},
		MediaBytes: impact.MediaBytes, DataBytes: dataBytes,
	}, nil
}

// openActiveDatabaseSnapshot materializes SQLite's durable authority into an
// isolated writable directory. A copied WAL (or rollback journal) is recovered
// there, while SQLite is never allowed to create sidecars beside the active DB.
// The caller holds import and maintenance exclusion for the complete copy.
func openActiveDatabaseSnapshot(ctx context.Context, runtimeState *runtimefs.Runtime, scratchRoot string) (*sqlitestore.Database, func() error, error) {
	if runtimeState == nil || runtimeState.Current.DatabasePath == "" || scratchRoot == "" {
		return nil, nil, errors.New("active database snapshot paths are required")
	}
	snapshotRoot, err := os.MkdirTemp(scratchRoot, ".active-sqlite-")
	if err != nil {
		return nil, nil, fmt.Errorf("create active database snapshot: %w", err)
	}
	cleanupRoot := func() error { return os.RemoveAll(snapshotRoot) }
	fail := func(err error) (*sqlitestore.Database, func() error, error) {
		return nil, nil, errors.Join(err, cleanupRoot())
	}
	destination := filepath.Join(snapshotRoot, "moment.db")
	if copied, err := copyStableSQLiteFile(ctx, runtimeState.Current.DatabasePath, destination, false); err != nil || !copied {
		return fail(errors.Join(errors.New("copy active SQLite database"), err))
	}
	// The WAL and rollback journal carry durable committed transactions. SHM is
	// coordination state and is deliberately regenerated inside snapshotRoot.
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := copyStableSQLiteFile(ctx, runtimeState.Current.DatabasePath+suffix, destination+suffix, true); err != nil {
			return fail(fmt.Errorf("copy active SQLite%s: %w", suffix, err))
		}
	}
	snapshotRuntime := &runtimefs.Runtime{
		Root: runtimeState.Root, Instance: runtimeState.Instance, Locks: runtimeState.Locks,
		Current: runtimefs.Generation{ID: "active-snapshot", Root: snapshotRoot, DatabasePath: destination},
	}
	database, err := sqlitestore.Open(ctx, snapshotRuntime, sqlitestore.Options{ReadPoolSize: 1})
	if err != nil {
		return fail(fmt.Errorf("recover active SQLite snapshot: %w", err))
	}
	cleanup := func() error { return errors.Join(database.Close(), cleanupRoot()) }
	return database, cleanup, nil
}

// ErrDiagnosticSnapshotBound reports a snapshot larger than its declared cap.
var ErrDiagnosticSnapshotBound = errors.New("diagnostic SQLite snapshot exceeds its byte bound")

// OpenDiagnosticSnapshot copies the durable SQLite database and WAL/journal
// into a private system temporary directory and opens only that copy in strict
// query-only mode. The caller must hold import shared and maintenance
// exclusive across this call so the stable-file checks describe one authority
// snapshot. SHM is coordination state and is regenerated only beside the copy.
func OpenDiagnosticSnapshot(ctx context.Context, runtimeState *runtimefs.Runtime, maxBytes int64) (*sqlitestore.DiagnosticDatabase, func() error, error) {
	if runtimeState == nil || runtimeState.Current.DatabasePath == "" || maxBytes < 1 {
		return nil, nil, errors.New("diagnostic database snapshot path is required")
	}
	tempBase, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return nil, nil, fmt.Errorf("resolve private diagnostic temporary directory: %w", err)
	}
	authorityRoot, err := filepath.EvalSymlinks(runtimeState.Root)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve diagnostic authority root: %w", err)
	}
	relativeTemp, err := filepath.Rel(authorityRoot, tempBase)
	if err != nil || relativeTemp == "." || (!filepath.IsAbs(relativeTemp) && relativeTemp != ".." && !strings.HasPrefix(relativeTemp, ".."+string(filepath.Separator))) {
		return nil, nil, errors.Join(errors.New("private diagnostic temporary directory must be outside Moment authority"), err)
	}
	snapshotRoot, err := os.MkdirTemp(tempBase, "moment-doctor-sqlite-")
	if err != nil {
		return nil, nil, fmt.Errorf("create private diagnostic snapshot: %w", err)
	}
	cleanupRoot := func() error { return os.RemoveAll(snapshotRoot) }
	fail := func(err error) (*sqlitestore.DiagnosticDatabase, func() error, error) {
		return nil, nil, errors.Join(err, cleanupRoot())
	}
	destination := filepath.Join(snapshotRoot, "moment.db")
	remaining := maxBytes
	copyBounded := func(source, target string, optional bool) (bool, error) {
		info, statErr := os.Lstat(source)
		if optional && errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		if statErr != nil {
			return false, statErr
		}
		if info.Size() < 0 || info.Size() > remaining {
			return false, ErrDiagnosticSnapshotBound
		}
		copied, copyErr := copyStableSQLiteFile(ctx, source, target, optional)
		if copyErr == nil && copied {
			remaining -= info.Size()
		}
		return copied, copyErr
	}
	if copied, err := copyBounded(runtimeState.Current.DatabasePath, destination, false); err != nil || !copied {
		return fail(errors.Join(errors.New("copy diagnostic SQLite database"), err))
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := copyBounded(runtimeState.Current.DatabasePath+suffix, destination+suffix, true); err != nil {
			return fail(fmt.Errorf("copy diagnostic SQLite%s: %w", suffix, err))
		}
	}
	database, err := sqlitestore.OpenDiagnostic(ctx, destination, 0)
	if err != nil {
		return fail(fmt.Errorf("open copied diagnostic SQLite database: %w", err))
	}
	cleanup := func() error { return errors.Join(database.Close(), cleanupRoot()) }
	return database, cleanup, nil
}

func copyStableSQLiteFile(ctx context.Context, source, destination string, optional bool) (copied bool, resultErr error) {
	before, err := os.Lstat(source)
	if optional && errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("SQLite authority file is not a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return false, errors.Join(errors.New("SQLite authority file changed while opening"), err)
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(destination)
		}
	}()
	copyLimit := before.Size()
	if copyLimit < math.MaxInt64 {
		copyLimit++
	}
	copiedBytes, copyErr := io.CopyBuffer(output, io.LimitReader(contextReader{ctx: ctx, reader: input}, copyLimit), make([]byte, copyBufferSize))
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || copiedBytes != before.Size() {
		return false, errors.Join(copyErr, syncErr, closeErr, errors.New("SQLite authority copy did not match source size"))
	}
	after, err := os.Lstat(source)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return false, errors.Join(errors.New("SQLite authority file changed while copying"), err)
	}
	published = true
	return true, nil
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

	_, err = validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "grants"}, func(value mediaRecord, _ map[string]json.RawMessage) error {
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
		unreferenced, err := portableMediaRetention(value.UnreferencedSince)
		if err != nil {
			return err
		}
		metadata := sqlitestore.ArchiveMedia{
			UnreferencedSinceMS: unreferenced, ID: value.ID, SHA256: value.SHA256, StoragePath: storagePath,
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

	site, err := readPortableSite(ctx, files)
	if err != nil {
		return err
	}
	if err := validateArchiveSite(archiveSiteOf(site), parent.IsDevelopment()); err != nil {
		return err
	}
	if err := importPortableAccounts(ctx, importer, files); err != nil {
		return err
	}

	if _, err := validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], portableMediaFields, func(value mediaRecord, _ map[string]json.RawMessage) error {
		grants, err := archiveMediaGrants(value.Grants)
		if err != nil {
			return err
		}
		for _, grant := range grants {
			if err := importer.InsertMediaGrant(ctx, value.ID, grant); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// The bodies are read beside the entries rather than before them: both
	// members are in entry ID order, so one cursor walking alongside the entry
	// stream finds each article's words exactly when the envelope that needs
	// them is being written.
	var bodies *jsonLinesReader[entryArticleRecord]

	bodies, err = openJSONLines[entryArticleRecord](ctx, files["data/entry_articles.jsonl"], portableEntryArticleFields)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, bodies.close()) }()

	_, err = validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], portableEntryFields, func(value entryRecord, _ map[string]json.RawMessage) error {
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
		var editedAtMS *int64
		if value.EditedAt != nil {
			edited, err := parsePortableTimestamp(*value.EditedAt)
			if err != nil {
				return err
			}
			milliseconds := edited.UnixMilli()
			editedAtMS = &milliseconds
		}
		var repostedAtMS *int64
		if value.RepostedAt != nil {
			repostedAt, err := parsePortableTimestamp(*value.RepostedAt)
			if err != nil {
				return err
			}
			millis := repostedAt.UnixMilli()
			repostedAtMS = &millis
		}
		visibility, err := archiveVisibility(value.Visibility)
		if err != nil {
			return err
		}
		usages := make([]sqlitestore.ArchiveUsage, len(value.Media))
		for index, usage := range value.Media {
			usages[index] = sqlitestore.ArchiveUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
		}
		location, err := domainLocation(value.Location)
		if err != nil {
			return err
		}
		linkCards, err := domainLinkCards(value.LinkCards)
		if err != nil {
			return err
		}
		entry := sqlitestore.ArchiveEntry{
			PublicationState: value.PublicationState, AuthorUserID: value.AuthorUserID, ID: value.ID, Kind: value.Kind, ContentMD: value.ContentMD, Visibility: visibility,
			CreatedAtMS: created.UnixMilli(), UpdatedAtMS: updated.UnixMilli(), PublishedAtMS: published.UnixMilli(), RepostedAtMS: repostedAtMS, EditedAtMS: editedAtMS, QuotedEntryID: value.QuotedEntryID, QuotedCommentID: value.QuotedCommentID, QuoteUnavailable: value.QuoteUnavailable, Media: usages, Location: location, MapCard: domain.CloneMapCard(value.MapCard), LinkCards: linkCards,
		}
		if domain.EntryKind(value.Kind) != domain.KindArticle {
			return importer.InsertEntry(ctx, entry)
		}
		if bodies == nil {
			return fmt.Errorf("entry %s is an article and this archive carries no bodies", value.ID)
		}
		body, _, more, err := bodies.next(ctx)
		if err != nil {
			return err
		}
		if !more || body.EntryID != value.ID {
			return fmt.Errorf("entry %s has no body in this archive", value.ID)
		}
		article, err := archiveEntryArticleOf(body)
		if err != nil {
			return err
		}
		return importer.InsertArticleEntry(ctx, entry, article)
	})
	if err != nil {
		return err
	}
	if bodies != nil {
		// Every body the member holds has now been claimed by the entry that
		// sits in front of it. One left over belongs to no post at all.
		if _, _, more, err := bodies.next(ctx); err != nil || more {
			return errors.Join(errors.New("an article body in this archive belongs to no entry"), err)
		}
	}

	if _, err := validateJSONLines[articleDraftRecord](ctx, files["data/article_drafts.jsonl"], portableArticleDraftFields, func(value articleDraftRecord, _ map[string]json.RawMessage) error {
		draft, err := archiveArticleDraftOf(value)
		if err != nil {
			return err
		}
		return importer.InsertArticleDraft(ctx, draft)
	}); err != nil {
		return err
	}

	_, err = validateJSONLines[commentRecord](ctx, files["data/comments.jsonl"], []string{"id", "entry_id", "parent_id", "author_name", "content_md", "created_at"}, func(value commentRecord, fields map[string]json.RawMessage) error {
		created, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return err
		}
		usages, err := portableCommentUsages(value, fields)
		if err != nil {
			return err
		}
		location, err := domainLocation(value.Location)
		if err != nil {
			return err
		}
		return importer.InsertComment(ctx, sqlitestore.ArchiveComment{
			ID: value.ID, EntryID: value.EntryID, ParentID: value.ParentID, AuthorName: value.AuthorName, AuthorUserID: value.AuthorUserID, AuthorSnapshot: value.AuthorSnapshot,
			ReplyToCommentID: value.ReplyToCommentID, ContentMD: value.ContentMD,
			CreatedAtMS: created.UnixMilli(), Media: usages, Location: location, Pinned: value.Pinned,
		})
	})
	if err != nil {
		return err
	}

	if err := importer.SetSite(ctx, archiveSiteOf(site)); err != nil {
		return err
	}
	if _, err := readInteractions(ctx, files, document, func(record sqlitestore.ArchiveInteraction) error { return importer.InsertInteraction(ctx, record) }); err != nil {
		return err
	}
	expected := portableExpectedCounts(document)
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

// portableUserFields and portableIdentityFields are the keys a reader requires
// of an accounts record. They are shared by validation and rebuild so the two
// cannot disagree about what a well-formed record contains.
var (
	portableUserFields = []string{
		"id", "username", "display_name", "bio", "avatar_media_id", "cover_media_id",
		"role", "status", "created_at", "updated_at",
	}
	portableIdentityFields = []string{
		"provider", "provider_user_id", "user_id", "email", "email_verified",
		"email_deliverable", "display_name", "avatar_url", "created_at", "last_login_at",
	}
)

func readPortableSite(ctx context.Context, files map[string]*zip.File) (siteRecord, error) {
	data, err := readSmallZIPMember(ctx, files["data/site.json"], maxPortableRecordBytes)
	if err != nil {
		return siteRecord{}, err
	}
	var site siteRecord
	if _, err := decodePortableJSONObject(data, &site, []string{"title", "bio", "timezone", "public_base_url", "avatar_media_id", "comments_enabled"}); err != nil {
		return siteRecord{}, err
	}
	return site, nil
}

func portableExpectedCounts(document manifest) sqlitestore.ArchiveCounts {
	counts := sqlitestore.ArchiveCounts{
		Entries: document.Counts.Entries, Comments: document.Counts.Comments, Media: document.Counts.Media,
		Users: document.Counts.Users, Identities: document.Counts.Identities,
		Articles: document.Counts.Articles, ArticleDrafts: document.Counts.ArticleDrafts,
		Interactions: document.Counts.Interactions,
	}
	return counts
}

// portableImportCounts is what a preview must say the archive holds. It is
// derived from portableExpectedCounts so the number shown before the decision
// and the number the rebuilt generation is held to are the same number.
func portableImportCounts(document manifest) ImportCounts {
	expected := portableExpectedCounts(document)
	return ImportCounts{
		Entries: expected.Entries, Comments: expected.Comments, Media: expected.Media,
		Users: expected.Users, Identities: expected.Identities,
		Articles: expected.Articles, ArticleDrafts: expected.ArticleDrafts,
		Interactions: expected.Interactions,
	}
}

func importPortableAccounts(ctx context.Context, importer *sqlitestore.PortableImporter, files map[string]*zip.File) error {
	if !archiveCarriesAccounts(files) {
		return errors.New("portable archives must contain accounts")
	}
	if _, err := validateJSONLines[userRecord](ctx, files["data/users.jsonl"], portableUserFields, func(value userRecord, _ map[string]json.RawMessage) error {
		account, err := archiveUserOf(value)
		if err != nil {
			return err
		}
		return importer.InsertUser(ctx, account)
	}); err != nil {
		return err
	}
	_, err := validateJSONLines[identityRecord](ctx, files["data/identities.jsonl"], portableIdentityFields, func(value identityRecord, _ map[string]json.RawMessage) error {
		identity, err := archiveIdentityOf(value)
		if err != nil {
			return err
		}
		return importer.InsertIdentity(ctx, identity)
	})
	return err
}

func portableStoragePath(sha, mimeType string) (string, error) {
	extension, ok := mediatype.Extension(mimeType)
	if len(sha) != 64 || !isLowerHex(sha) || !ok {
		return "", errors.New("portable media storage identity is invalid")
	}
	return "media/original/" + sha[:2] + "/" + sha + "." + extension, nil
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

func (inspector *Inspector) checkActive(root string, now time.Time, cleanup bool) error {
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
			if !cleanup {
				return importStagingInvalid("import staging metadata is missing", err)
			}
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
			if cleanup {
				if err := removeStagingDirectory(parent, stageRoot); err != nil {
					return err
				}
			}
		case stagingReady:
			if metadata.ExpiresAtMS <= now.UnixMilli() {
				if cleanup {
					if err := removeStagingDirectory(parent, stageRoot); err != nil {
						return err
					}
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
	expectedParent, err := filepath.Abs(filepath.Clean(parent))
	if err != nil {
		return err
	}
	actualRoot, err := filepath.Abs(filepath.Clean(stageRoot))
	if err != nil || filepath.Dir(actualRoot) != expectedParent || !strings.HasPrefix(filepath.Base(actualRoot), "import-") {
		return errors.Join(errors.New("refuse unsafe import staging cleanup target"), err)
	}
	info, err := os.Lstat(actualRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse unsafe import staging cleanup entry")
	}
	if err := os.RemoveAll(actualRoot); err != nil {
		return err
	}
	return runtimefs.SyncDirectory(expectedParent)
}

func (inspector *Inspector) loadReady(ctx context.Context, token string, subject Authorization) (_ stagingMetadata, _ manifest, _ string, lease *runtimefs.Lease, resultErr error) {
	if token == "" || len(token) > 256 || !subject.Valid() {
		return stagingMetadata{}, manifest{}, "", nil, importStagingInvalid("staging token or account binding is invalid", nil)
	}
	runtimeState, err := runtimefs.OpenContext(ctx, inspector.dataDir)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, err
	}
	heldLease, err := runtimeState.Locks.Acquire(ctx, runtimefs.ImportLock, runtimefs.Exclusive)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, err
	}
	state, document, stageRoot, err := inspector.loadReadyHeld(ctx, runtimeState, token, subject)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", nil, errors.Join(err, heldLease.Release())
	}
	return state, document, stageRoot, heldLease, nil
}

func (inspector *Inspector) loadReadyHeld(ctx context.Context, runtimeState *runtimefs.Runtime, token string, subject Authorization) (stagingMetadata, manifest, string, error) {
	if token == "" || len(token) > 256 || !subject.Valid() || runtimeState == nil {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("staging token or account binding is invalid", nil)
	}
	parent := filepath.Join(runtimeState.Root, "staging", "import")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", err
	}
	var stageRoot string
	var state stagingMetadata
	for _, entry := range entries {
		candidate := filepath.Join(parent, entry.Name())
		info, infoErr := os.Lstat(candidate)
		if infoErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), "import-") {
			return stagingMetadata{}, manifest{}, "", importStagingInvalid("import staging contains an unsafe entry", infoErr)
		}
		metadata, err := readStagingMetadata(candidate)
		if err != nil {
			return stagingMetadata{}, manifest{}, "", importStagingInvalid("import staging metadata is corrupt", err)
		}
		if metadata.State == stagingReady || metadata.State == stagingCommitting || metadata.State == stagingUsed {
			if stageRoot != "" {
				return stagingMetadata{}, manifest{}, "", importStagingInvalid("multiple active import staging records exist", nil)
			}
			stageRoot, state = candidate, metadata
		}
	}
	if stageRoot == "" {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("no matching ready import staging exists", nil)
	}
	if state.State != stagingReady {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("import staging token was already claimed or used", nil)
	}
	now := inspector.clock().UTC().Truncate(time.Millisecond)
	if state.ExpiresAtMS <= now.UnixMilli() {
		return stagingMetadata{}, manifest{}, "", domain.NewError(domain.CodeImportStagingExpired, "import staging token has expired", map[string]any{"expired_at": time.UnixMilli(state.ExpiresAtMS).UTC()})
	}
	if state.InstanceID != runtimeState.Instance.ID || state.Authorization != subject || state.GenerationID != filepath.Base(stageRoot) {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("import staging binding does not match this instance and initiating account credential", nil)
	}
	expectedHash, err := hex.DecodeString(state.TokenHash)
	actualHash := sha256.Sum256([]byte(token))
	if err != nil || len(expectedHash) != sha256.Size || subtle.ConstantTimeCompare(expectedHash, actualHash[:]) != 1 {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("import staging token does not match", nil)
	}
	archivePath := filepath.Join(stageRoot, "archive.zip")
	actualArchiveHash, err := fileSHA256(ctx, archivePath, inspector.limits.MaxArchiveBytes)
	if err != nil || actualArchiveHash != state.ArchiveSHA {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("staged archive hash does not match metadata", err)
	}
	document, err := inspector.preflight(ctx, archivePath)
	if err != nil {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("staged archive no longer passes preflight", err)
	}
	if err := ValidateFileContext(ctx, archivePath); err != nil {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("staged archive no longer validates", err)
	}
	generation := stagingGeneration(stageRoot, state.GenerationID)
	if err := validateStagingGeneration(ctx, runtimeState, generation, archivePath, document); err != nil {
		return stagingMetadata{}, manifest{}, "", importStagingInvalid("staging generation no longer validates", err)
	}
	return state, document, stageRoot, nil
}

func validateStagingGeneration(ctx context.Context, parent *runtimefs.Runtime, generation runtimefs.Generation, archivePath string, document manifest) error {
	if err := validatePortableGenerationTree(ctx, generation, archivePath); err != nil {
		return err
	}
	runtimeState := &runtimefs.Runtime{Root: parent.Root, Instance: parent.Instance, Current: generation, Locks: parent.Locks}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	return validateStagingAuthority(ctx, generation, archivePath, document, database)
}

type stagingAuthorityDatabase interface {
	ValidatePortableReady(context.Context, sqlitestore.ArchiveCounts) error
	BeginArchiveSnapshot(context.Context) (*sqlitestore.ArchiveSnapshot, error)
}

func validateStagingAuthority(ctx context.Context, generation runtimefs.Generation, archivePath string, document manifest, database stagingAuthorityDatabase) error {
	if err := requireArchiveLayout(archivePath); err != nil {
		return err
	}
	if err := database.ValidatePortableReady(ctx, portableExpectedCounts(document)); err != nil {
		return err
	}
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = snapshot.Close() }()
	if err := snapshot.ValidateIntegrity(ctx); err != nil {
		return err
	}
	if err := validatePortableAuthority(ctx, snapshot, archivePath); err != nil {
		return err
	}
	return snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
		file, err := openOriginal(generation.Root, value.StoragePath)
		if err != nil {
			return err
		}
		id, parseErr := domain.ParseID(value.ID)
		if parseErr != nil {
			_ = file.Close()
			return parseErr
		}
		verifyErr := media.VerifyPortableOriginal(ctx, file, media.Media{
			ID: id, SHA256: value.SHA256, StoragePath: value.StoragePath,
			OriginalName: value.OriginalName, MIMEType: value.MIMEType, ByteSize: value.ByteSize,
			Width: value.Width, Height: value.Height, CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(),
		})
		return errors.Join(verifyErr, file.Close())
	})
}

func validatePortableGenerationTree(ctx context.Context, generation runtimefs.Generation, archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	mediaRecords := findZIPMember(reader.File, "data/media.jsonl")
	expected := map[string]bool{
		".": true, "moment.db": false, "media": true, "media/original": true,
	}
	_, err = validateJSONLines[mediaRecord](ctx, mediaRecords, []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "grants"}, func(value mediaRecord, _ map[string]json.RawMessage) error {
		storagePath, err := portableStoragePath(value.SHA256, value.MIMEType)
		if err != nil {
			return err
		}
		expected[path.Dir(storagePath)] = true
		expected[storagePath] = false
		return nil
	})
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(expected))
	err = filepath.WalkDir(generation.Root, func(candidate string, entry fs.DirEntry, walkErr error) error {
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
			return errors.New("staging generation contains a symlink")
		}
		relative, err := filepath.Rel(generation.Root, candidate)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		directory, ok := expected[relative]
		if !ok || directory != info.IsDir() || !directory && !info.Mode().IsRegular() {
			return fmt.Errorf("staging generation contains unexpected path %q", relative)
		}
		if _, duplicate := seen[relative]; duplicate {
			return fmt.Errorf("staging generation path %q is duplicated", relative)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("staging generation is missing an expected canonical path")
	}
	return nil
}

func requireArchiveLayout(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	seen := make(map[string]struct{}, len(reader.File))
	for _, file := range reader.File {
		seen[file.Name] = struct{}{}
	}
	_, err = portableLeadingMembers(seen)
	return err
}

// validatePortableAuthority proves a rebuilt generation says exactly what the
// archive it was built from says.
//
// Both sides of every comparison are produced by the same conversion function.
// That is the whole point: this check used to decode the archive side whole and
// rebuild the database side field by field, and the two hand-written halves
// were free to drift. When they did, the message was "staged X do not match the
// portable archive" with no field named, and it only appeared once real data
// used the forgotten field -- which is how a lost pinned flag made every backup
// containing a pinned comment unrestorable while every test stayed green.
func validatePortableAuthority(ctx context.Context, snapshot *sqlitestore.ArchiveSnapshot, archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	expectedSite, err := readPortableSite(ctx, files)
	if err != nil {
		return err
	}
	actualSite, err := snapshot.Site(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expectedSite, portableSiteRecord(actualSite)) {
		return errors.New("staged site settings do not match the portable archive")
	}
	if err := validateStagedAccounts(ctx, snapshot, files); err != nil {
		return err
	}
	if err := validateStagedArticles(ctx, snapshot, files); err != nil {
		return err
	}
	expectedEntries := make([]entryRecord, 0)
	if _, err := validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], portableEntryFields, func(value entryRecord, _ map[string]json.RawMessage) error {
		expectedEntries = append(expectedEntries, value)
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(expectedEntries, func(i, j int) bool { return expectedEntries[i].ID < expectedEntries[j].ID })
	actualEntries := make([]entryRecord, 0, len(expectedEntries))
	entriesErr := snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
		actualEntries = append(actualEntries, portableEntryRecord(value))
		return nil
	})
	sort.Slice(actualEntries, func(i, j int) bool { return actualEntries[i].ID < actualEntries[j].ID })
	if entriesErr != nil || !reflect.DeepEqual(expectedEntries, actualEntries) {
		return errors.Join(errors.New("staged entries do not match the portable archive"), entriesErr)
	}
	expectedComments := make([]commentRecord, 0)
	if _, err := validateJSONLines[commentRecord](ctx, files["data/comments.jsonl"], []string{"id", "entry_id", "parent_id", "author_name", "content_md", "created_at"}, func(value commentRecord, fields map[string]json.RawMessage) error {
		usages, err := portableCommentUsages(value, fields)
		if err != nil {
			return err
		}
		value.Media = portableUsageRecords(usages)
		expectedComments = append(expectedComments, value)
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(expectedComments, func(i, j int) bool { return expectedComments[i].ID < expectedComments[j].ID })
	actualComments := make([]commentRecord, 0, len(expectedComments))
	commentsErr := snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
		actualComments = append(actualComments, portableCommentRecord(value))
		return nil
	})
	sort.Slice(actualComments, func(i, j int) bool { return actualComments[i].ID < actualComments[j].ID })
	if commentsErr != nil || !reflect.DeepEqual(expectedComments, actualComments) {
		return errors.Join(errors.New("staged comments do not match the portable archive"), commentsErr)
	}
	expectedMedia := make([]mediaRecord, 0)
	if _, err := validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "grants"}, func(value mediaRecord, _ map[string]json.RawMessage) error {
		expectedMedia = append(expectedMedia, value)
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(expectedMedia, func(i, j int) bool { return expectedMedia[i].ID < expectedMedia[j].ID })
	actualMedia := make([]mediaRecord, 0, len(expectedMedia))
	mediaErr := snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
		record, err := portableMediaRecord(value)
		if err != nil {
			return err
		}
		actualMedia = append(actualMedia, record)
		return nil
	})
	sort.Slice(actualMedia, func(i, j int) bool { return actualMedia[i].ID < actualMedia[j].ID })
	if mediaErr != nil || !reflect.DeepEqual(expectedMedia, actualMedia) {
		return errors.Join(errors.New("staged media metadata do not match the portable archive"), mediaErr)
	}
	return nil
}

// validateStagedArticles compares the staged articles and drafts against the
// archive, body bytes included.
//
// Both sides are produced by the same conversion function, and that function
// re-serializes the parsed document rather than copying whatever bytes it was
// given, so the comparison is byte for byte over the canonical form. A body
// that came back spelled differently is a body that came back changed.
func validateStagedArticles(ctx context.Context, snapshot *sqlitestore.ArchiveSnapshot, files map[string]*zip.File) error {
	expectedArticles := make([]entryArticleRecord, 0)
	expectedDrafts := make([]articleDraftRecord, 0)

	if _, err := validateJSONLines[entryArticleRecord](ctx, files["data/entry_articles.jsonl"], portableEntryArticleFields, func(value entryArticleRecord, _ map[string]json.RawMessage) error {
		expectedArticles = append(expectedArticles, value)
		return nil
	}); err != nil {
		return err
	}
	if _, err := validateJSONLines[articleDraftRecord](ctx, files["data/article_drafts.jsonl"], portableArticleDraftFields, func(value articleDraftRecord, _ map[string]json.RawMessage) error {
		expectedDrafts = append(expectedDrafts, value)
		return nil
	}); err != nil {
		return err
	}

	sort.Slice(expectedArticles, func(i, j int) bool { return expectedArticles[i].EntryID < expectedArticles[j].EntryID })
	actualArticles := make([]entryArticleRecord, 0, len(expectedArticles))
	articlesErr := snapshot.EntryArticles(ctx, func(value sqlitestore.ArchiveEntryArticle) error {
		record, err := portableEntryArticleRecord(value)
		if err != nil {
			return err
		}
		actualArticles = append(actualArticles, record)
		return nil
	})
	sort.Slice(actualArticles, func(i, j int) bool { return actualArticles[i].EntryID < actualArticles[j].EntryID })
	if articlesErr != nil || !reflect.DeepEqual(expectedArticles, actualArticles) {
		return errors.Join(errors.New("staged articles do not match the portable archive"), articlesErr)
	}
	sort.Slice(expectedDrafts, func(i, j int) bool { return expectedDrafts[i].ID < expectedDrafts[j].ID })
	actualDrafts := make([]articleDraftRecord, 0, len(expectedDrafts))
	draftsErr := snapshot.ArticleDrafts(ctx, func(value sqlitestore.ArchiveArticleDraft) error {
		record, err := portableArticleDraftRecord(value)
		if err != nil {
			return err
		}
		actualDrafts = append(actualDrafts, record)
		return nil
	})
	sort.Slice(actualDrafts, func(i, j int) bool { return actualDrafts[i].ID < actualDrafts[j].ID })
	if draftsErr != nil || !reflect.DeepEqual(expectedDrafts, actualDrafts) {
		return errors.Join(errors.New("staged article drafts do not match the portable archive"), draftsErr)
	}
	return nil
}

func validateStagedAccounts(ctx context.Context, snapshot *sqlitestore.ArchiveSnapshot, files map[string]*zip.File) error {
	expectedUsers := make([]userRecord, 0)
	expectedIdentities := make([]identityRecord, 0)
	if !archiveCarriesAccounts(files) {
		return errors.New("portable archives must contain accounts")
	}
	if _, err := validateJSONLines[userRecord](ctx, files["data/users.jsonl"], portableUserFields, func(value userRecord, _ map[string]json.RawMessage) error {
		expectedUsers = append(expectedUsers, value)
		return nil
	}); err != nil {
		return err
	}
	if _, err := validateJSONLines[identityRecord](ctx, files["data/identities.jsonl"], portableIdentityFields, func(value identityRecord, _ map[string]json.RawMessage) error {
		expectedIdentities = append(expectedIdentities, value)
		return nil
	}); err != nil {
		return err
	}

	sort.Slice(expectedUsers, func(i, j int) bool { return expectedUsers[i].ID < expectedUsers[j].ID })
	actualUsers := make([]userRecord, 0, len(expectedUsers))
	usersErr := snapshot.Users(ctx, func(value sqlitestore.ArchiveUser) error {
		actualUsers = append(actualUsers, portableUserRecord(value))
		return nil
	})
	sort.Slice(actualUsers, func(i, j int) bool { return actualUsers[i].ID < actualUsers[j].ID })
	if usersErr != nil || !reflect.DeepEqual(expectedUsers, actualUsers) {
		return errors.Join(errors.New("staged accounts do not match the portable archive"), usersErr)
	}
	identityOrder := func(values []identityRecord) func(int, int) bool {
		return func(i, j int) bool {
			if values[i].Provider != values[j].Provider {
				return values[i].Provider < values[j].Provider
			}
			return values[i].ProviderUserID < values[j].ProviderUserID
		}
	}
	sort.Slice(expectedIdentities, identityOrder(expectedIdentities))
	actualIdentities := make([]identityRecord, 0, len(expectedIdentities))
	identitiesErr := snapshot.Identities(ctx, func(value sqlitestore.ArchiveIdentity) error {
		actualIdentities = append(actualIdentities, portableIdentityRecord(value))
		return nil
	})
	sort.Slice(actualIdentities, identityOrder(actualIdentities))
	if identitiesErr != nil || !reflect.DeepEqual(expectedIdentities, actualIdentities) {
		return errors.Join(errors.New("staged account identities do not match the portable archive"), identitiesErr)
	}
	return nil
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
	if value.GenerationID != filepath.Base(stageRoot) || value.InstanceID == "" || !value.Authorization.Valid() || value.ExpiresAtMS <= 0 ||
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
