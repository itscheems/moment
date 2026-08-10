package doctor

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	defaultMaxMediaRecords  = 100_000
	defaultMaxOriginalFiles = 100_000
	defaultMaxStagingItems  = 256
	defaultSnapshotWait     = 500 * time.Millisecond
	defaultMaxDatabaseBytes = int64(64 << 30)
	serveProbeWait          = 40 * time.Millisecond
	lowDiskWarningBytes     = uint64(2 << 30)
)

// Options configures safe resource bounds and transport-owned dependencies.
type Options struct {
	DataDir          string
	Clock            func() time.Time
	Progress         func(Progress)
	MaxMediaRecords  int
	MaxOriginalFiles int
	MaxStagingItems  int
	MaxDatabaseBytes int64
	SnapshotWait     time.Duration
	// ActiveDatabase is supplied only by the running HTTP server. Summary mode
	// borrows its established read pool instead of reopening active SQLite.
	ActiveDatabase *sqlitestore.Database
}

// Service runs the fixed read-only diagnostic registry.
type Service struct{ options Options }

// New validates bounds and constructs a diagnostic service.
func New(options Options) (*Service, error) {
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.MaxMediaRecords == 0 {
		options.MaxMediaRecords = defaultMaxMediaRecords
	}
	if options.MaxOriginalFiles == 0 {
		options.MaxOriginalFiles = defaultMaxOriginalFiles
	}
	if options.MaxStagingItems == 0 {
		options.MaxStagingItems = defaultMaxStagingItems
	}
	if options.SnapshotWait == 0 {
		options.SnapshotWait = defaultSnapshotWait
	}
	if options.MaxDatabaseBytes == 0 {
		options.MaxDatabaseBytes = defaultMaxDatabaseBytes
	}
	if options.MaxMediaRecords < 1 || options.MaxMediaRecords > defaultMaxMediaRecords ||
		options.MaxOriginalFiles < 1 || options.MaxOriginalFiles > defaultMaxOriginalFiles ||
		options.MaxStagingItems < 1 || options.MaxStagingItems > defaultMaxStagingItems || options.SnapshotWait < time.Millisecond ||
		options.MaxDatabaseBytes < 1 || options.MaxDatabaseBytes > defaultMaxDatabaseBytes {
		return nil, errors.New("doctor limits are outside product bounds")
	}
	return &Service{options: options}, nil
}

// Run executes a fixed registry. Summary mode deliberately marks expensive or
// topology-sensitive checks skipped instead of computing a full report and
// attempting to redact it afterward.
func (service *Service) Run(ctx context.Context, mode Mode) (Report, error) {
	if mode != ModeSummary && mode != ModeDeep {
		return Report{}, errors.New("unsupported doctor mode")
	}
	builder := newReport(mode, service.options.Progress)
	if mode == ModeSummary {
		for _, id := range []string{
			CheckSQLiteIntegrity, CheckForeignKeys, CheckFTS5Capability, CheckFTSConsistency,
			CheckMediaDirectory, CheckMissingMedia, CheckOrphanMedia, CheckMediaReferenceConsistency,
			CheckMediaChecksum, CheckPreviewCache, CheckStaleImportStaging, CheckImportRecoveryState,
		} {
			builder.set(skipped(id, "requires_deep"))
		}
	}

	root, err := runtimefs.ResolveDataDir(service.options.DataDir)
	if err != nil {
		builder.set(failed(CheckDataDir, "the data directory could not be resolved", "select an absolute readable Moment data directory", map[string]any{"resolved": false}, failureIO))
		return builder.finish(cancelled(err)), errIfCancelled(err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		builder.set(failed(CheckDataDir, "the data directory is missing or unsafe", "initialize or restore the selected Moment data directory", map[string]any{"exists": err == nil, "directory": err == nil && rootInfo.IsDir()}, failureIO))
		service.inspectDisk(builder, root)
		return builder.finish(cancelled(err)), errIfCancelled(err)
	}
	readable := directoryReadable(root)
	writable := permissionAllowsWrite(rootInfo)
	if !readable || !writable {
		builder.set(failed(CheckDataDir, "the data directory permissions are not usable", "grant the Moment process read, traverse, and owner-write permission", map[string]any{"readable": readable, "writable_permission": writable}, failureIO))
	} else {
		builder.set(pass(CheckDataDir, "the data directory is present and accessible", "no action is required", map[string]any{"readable": true, "writable_permission": true}))
	}
	service.inspectDisk(builder, root)
	service.inspectSecrets(builder, root)
	service.inspectCurrentNext(builder, root)

	instance, instanceErr := runtimefs.ReadInstance(root)
	locker, lockerErr := runtimefs.OpenExistingLocker(root)
	var held *runtimefs.LeaseSet
	if lockerErr == nil {
		lockCtx, cancelLock := context.WithTimeout(ctx, service.options.SnapshotWait)
		maintenanceMode := runtimefs.Exclusive
		if service.options.ActiveDatabase != nil && mode == ModeSummary {
			maintenanceMode = runtimefs.Shared
		}
		held, lockerErr = locker.AcquireSet(lockCtx,
			runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Shared},
			runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: maintenanceMode},
			runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Shared},
		)
		cancelLock()
	}
	if lockerErr != nil {
		if ctx.Err() != nil {
			builder.set(failed(CheckInstanceLocks, "lock inspection was cancelled", "retry with a longer --timeout", map[string]any{"snapshot_acquired": false}, failureCancelled))
			return builder.finish(true), ctx.Err()
		}
		builder.set(failed(CheckInstanceLocks, "the read-only diagnostic snapshot could not be acquired", "finish the active import or repair persistent lock files, then retry", map[string]any{"snapshot_acquired": false}, failureUnavailable))
	} else {
		defer func() { _ = held.Release() }()
		serveState, probeErr := probeServeLock(ctx, locker)
		if probeErr != nil && ctx.Err() != nil {
			builder.set(failed(CheckInstanceLocks, "serve lock inspection was cancelled", "retry with a longer --timeout", map[string]any{"snapshot_acquired": true}, failureCancelled))
			return builder.finish(true), ctx.Err()
		}
		if probeErr != nil {
			builder.set(failed(CheckInstanceLocks, "the serve lock state could not be inspected", "check lock-file permissions and OS advisory-lock support", map[string]any{"snapshot_acquired": true}, failureUnavailable))
		} else {
			missingLocks := make([]string, 0, 3)
			for _, name := range []runtimefs.LockName{runtimefs.ServeLock, runtimefs.MaintenanceLock, runtimefs.MediaLock} {
				if !locker.HasPersistentLock(name) {
					missingLocks = append(missingLocks, string(name))
				}
			}
			order := "import_shared>maintenance_exclusive>media_shared"
			if service.options.ActiveDatabase != nil && mode == ModeSummary {
				order = "import_shared>maintenance_shared>media_shared"
			}
			builder.set(pass(CheckInstanceLocks, "the OS-backed lock matrix is available", "no action is required", map[string]any{
				"snapshot_acquired": true, "snapshot_order": order, "serve_state": serveState, "legacy_uninitialized": missingLocks,
			}))
		}
	}

	current, currentErr := (&runtimefs.Runtime{Root: root}).ResolveCurrent()
	if instanceErr != nil || currentErr != nil {
		builder.set(failed(CheckCurrentGeneration, "CURRENT does not select one complete generation", "restore a valid CURRENT pointer explicitly; never promote CURRENT.next automatically", map[string]any{"instance_valid": instanceErr == nil, "generation_valid": currentErr == nil}, failureIntegrity))
		builder.set(skipped(CheckDatabaseOpen, "current_generation_failed"))
		builder.set(skipped(CheckSchemaVersion, "database_not_open"))
		builder.set(skipped(CheckSiteConfiguration, "database_not_open"))
		if mode == ModeDeep {
			service.skipDatabaseAndMediaDeep(builder, "current_generation_failed")
		}
		return builder.finish(ctx.Err() != nil), errIfCancelled(ctx.Err())
	}
	currentEvidence := map[string]any{"valid": true}
	if mode == ModeDeep {
		currentEvidence["generation_id"] = current.ID
	}
	generationInfo, generationStatErr := os.Lstat(current.Root)
	databaseInfo, databaseStatErr := os.Lstat(current.DatabasePath)
	generationReadable := generationStatErr == nil && generationInfo.IsDir() && directoryReadable(current.Root)
	databaseReadable := databaseStatErr == nil && databaseInfo.Mode().IsRegular() && permissionAllowsRead(databaseInfo)
	currentEvidence["generation_readable"] = generationReadable
	currentEvidence["database_readable_permission"] = databaseReadable
	if !generationReadable || !databaseReadable {
		builder.set(failed(CheckCurrentGeneration, "CURRENT selects a generation with unusable permissions", "grant read and traverse permission to the selected generation and database", currentEvidence, failureIO))
	} else {
		builder.set(pass(CheckCurrentGeneration, "CURRENT selects one structurally complete generation", "no action is required", currentEvidence))
	}
	if held == nil && service.options.ActiveDatabase == nil {
		builder.set(skipped(CheckDatabaseOpen, "diagnostic_snapshot_not_acquired"))
		builder.set(skipped(CheckSchemaVersion, "database_not_open"))
		builder.set(skipped(CheckSiteConfiguration, "database_not_open"))
		if mode == ModeDeep {
			service.skipDatabaseAndMediaDeep(builder, "diagnostic_snapshot_not_acquired")
		}
		return builder.finish(ctx.Err() != nil), errIfCancelled(ctx.Err())
	}

	var database *sqlitestore.DiagnosticDatabase
	var cleanupDatabase func() error
	var dbErr error
	if service.options.ActiveDatabase != nil {
		if filepath.Clean(service.options.ActiveDatabase.Path()) != filepath.Clean(current.DatabasePath) {
			dbErr = errors.New("active diagnostic database does not match CURRENT")
		} else {
			database, dbErr = sqlitestore.NewDiagnosticView(service.options.ActiveDatabase)
			if dbErr == nil {
				cleanupDatabase = database.Close
			}
		}
	} else {
		database, cleanupDatabase, dbErr = archive.OpenDiagnosticSnapshot(ctx, &runtimefs.Runtime{
			Root: root, Instance: instance, Current: current, Locks: locker,
		}, service.options.MaxDatabaseBytes)
		if dbErr == nil && locker.HasPersistentLock(runtimefs.MaintenanceLock) {
			dbErr = held.ReleaseLock(runtimefs.MaintenanceLock)
		}
	}
	if dbErr != nil {
		class := failureIntegrity
		bounded := errors.Is(dbErr, archive.ErrDiagnosticSnapshotBound)
		if bounded {
			class = failureUnavailable
		}
		var pathErr *fs.PathError
		if errors.As(dbErr, &pathErr) {
			class = failureIO
		}
		builder.set(failed(CheckDatabaseOpen, "the selected database cannot be opened read-only", "restore database readability or recover a valid generation", map[string]any{"opened": false, "bounded": bounded}, class))
		builder.set(skipped(CheckSchemaVersion, "database_not_open"))
		builder.set(skipped(CheckSiteConfiguration, "database_not_open"))
		if mode == ModeDeep {
			service.skipDatabaseAndMediaDeep(builder, "database_not_open")
		}
		return builder.finish(ctx.Err() != nil), errIfCancelled(ctx.Err())
	}
	defer func() { _ = cleanupDatabase() }()
	builder.set(pass(CheckDatabaseOpen, "the selected database opens in strict read-only mode", "no action is required", map[string]any{"read_only": true, "query_only": true}))

	schema, schemaErr := database.Schema(ctx)
	if schemaErr != nil {
		builder.set(failed(CheckSchemaVersion, "the migration ledger or required schema catalog has drifted", "restore a database with the exact supported migration prefix", map[string]any{"applied_version": schema.AppliedVersion, "expected_version": schema.ExpectedVersion, "exact": false}, failureIntegrity))
	} else {
		builder.set(pass(CheckSchemaVersion, "the schema and migration ledger match this binary", "no action is required", map[string]any{"applied_version": schema.AppliedVersion, "expected_version": schema.ExpectedVersion, "exact": true}))
	}
	service.inspectSite(ctx, builder, database, locker, instance.Environment == runtimefs.EnvironmentDevelopment)
	if mode == ModeDeep && held != nil {
		service.runDeep(ctx, builder, root, instance, current, database, locker, schemaErr == nil)
	} else if mode == ModeDeep {
		service.skipDatabaseAndMediaDeep(builder, "diagnostic_snapshot_not_acquired")
	}
	cleanupErr := cleanupDatabase()
	cleanupDatabase = func() error { return nil }
	if cleanupErr != nil {
		builder.set(failed(CheckDatabaseOpen, "the private diagnostic database snapshot could not be released", "remove stale private temporary files and retry", map[string]any{"opened": true, "cleanup_complete": false}, failureIO))
	}

	if ctx.Err() != nil {
		return builder.finish(true), ctx.Err()
	}
	after, afterErr := (&runtimefs.Runtime{Root: root}).ResolveCurrent()
	if afterErr != nil || after.ID != current.ID {
		builder.set(failed(CheckCurrentGeneration, "CURRENT changed during the diagnostic snapshot", "retry after import activity stops and inspect the lock matrix", map[string]any{"stable": false}, failureIntegrity))
	}
	return builder.finish(false), nil
}

func (service *Service) runDeep(ctx context.Context, builder *reportBuilder, root string, instance runtimefs.Instance, current runtimefs.Generation, database *sqlitestore.DiagnosticDatabase, locker *runtimefs.Locker, schemaReady bool) {
	integrity, err := database.Integrity(ctx)
	if err != nil || !integrity.OK {
		builder.set(failed(CheckSQLiteIntegrity, "SQLite integrity verification failed", "restore a verified backup or recover the affected generation", map[string]any{"problems": integrity.Problems, "bounded": integrity.Bounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckSQLiteIntegrity, "SQLite reports no structural integrity errors", "no action is required", map[string]any{"problems": 0, "bounded": false}))
	}
	if ctx.Err() != nil {
		return
	}
	foreign, err := database.ForeignKeys(ctx)
	if err != nil || foreign.Violations != 0 {
		builder.set(failed(CheckForeignKeys, "foreign-key violations were detected", "restore or repair the authoritative relational data before serving writes", map[string]any{"violations": foreign.Violations, "bounded": foreign.Bounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckForeignKeys, "no foreign-key violations were detected", "no action is required", map[string]any{"violations": 0, "bounded": false}))
	}
	if ctx.Err() != nil {
		return
	}
	fts, err := database.FTSCapability(ctx)
	if err != nil || !fts.FTS5 || !fts.Trigram {
		builder.set(failed(CheckFTS5Capability, "FTS5 trigram capability is unavailable or malformed", "use the supported Moment binary and restore the required entry_fts schema", map[string]any{"fts5": fts.FTS5, "trigram": fts.Trigram}, failureIntegrity))
	} else {
		builder.set(pass(CheckFTS5Capability, "FTS5 and trigram tokenization are available", "no action is required", map[string]any{"fts5": true, "trigram": true}))
	}
	if ctx.Err() != nil {
		return
	}
	if schemaReady && locker != nil {
		repository, repositoryErr := sqlitestore.NewSearchRepository(database, locker)
		search, serviceErr := app.NewSearchService(repository)
		if repositoryErr != nil || serviceErr != nil {
			builder.set(failed(CheckFTSConsistency, "the search diagnostic adapter could not be composed", "verify the current schema and retry", map[string]any{"checked": false}, failureInternal))
		} else {
			result, diagnoseErr := search.DiagnoseDerived(ctx, service.options.MaxMediaRecords)
			if diagnoseErr != nil || result.Drifted != 0 || result.OrphanIndexRows != 0 || result.Bounded || result.StoredVersion != result.ExpectedVersion {
				builder.set(failed(CheckFTSConsistency, "derived search rows do not match authoritative Markdown", "run 'moment search rebuild --dry-run --json', then rebuild the derived index explicitly", map[string]any{"total": result.Total, "drifted": result.Drifted, "orphan_rows": result.OrphanIndexRows, "stored_version": result.StoredVersion, "expected_version": result.ExpectedVersion, "bounded": result.Bounded}, failureIntegrity))
			} else {
				builder.set(pass(CheckFTSConsistency, "derived search rows match authoritative Markdown", "no action is required", map[string]any{"total": result.Total, "drifted": 0, "orphan_rows": 0, "bounded": false}))
			}
		}
	} else {
		builder.set(skipped(CheckFTSConsistency, "schema_or_lock_dependency_failed"))
	}
	if ctx.Err() != nil {
		return
	}
	service.inspectMedia(ctx, builder, root, current, database)
	if ctx.Err() != nil {
		return
	}
	staging, recovery, archiveErr := archive.DiagnosePersistentState(ctx, root, instance.ID, current.ID, service.options.Clock(), service.options.MaxStagingItems)
	if archiveErr != nil || staging.Invalid != 0 || staging.Incomplete != 0 || staging.Committing != 0 || staging.Bounded {
		builder.set(failed(CheckStaleImportStaging, "import staging contains invalid, incomplete, or interrupted state", "finish or explicitly discard the diagnosed staging operation before retrying import", map[string]any{"total": staging.Total, "ready": staging.Ready, "expired": staging.Expired, "committing": staging.Committing, "used": staging.Used, "failed": staging.Failed, "invalid": staging.Invalid, "incomplete": staging.Incomplete, "bounded": staging.Bounded}, failureIntegrity))
	} else if staging.Expired != 0 || staging.Failed != 0 || staging.Used != 0 {
		builder.set(warning(CheckStaleImportStaging, "import staging contains terminal or expired cleanup candidates", "allow normal retention cleanup or discard the stale staging explicitly", map[string]any{"total": staging.Total, "ready": staging.Ready, "expired": staging.Expired, "used": staging.Used, "failed": staging.Failed, "invalid": 0, "incomplete": 0, "bounded": false}))
	} else {
		builder.set(pass(CheckStaleImportStaging, "import staging state is valid", "no action is required", map[string]any{"total": staging.Total, "ready": staging.Ready, "expired": 0, "committing": 0, "invalid": 0, "bounded": false}))
	}
	if archiveErr != nil || recovery.Invalid != 0 || recovery.CurrentMismatch != 0 || recovery.NeedsAttention != 0 || recovery.Active != 0 || recovery.Bounded {
		builder.set(failed(CheckImportRecoveryState, "import recovery journals require attention", "run doctor after stopping import activity and restore the journal-selected generation explicitly", map[string]any{"journals": recovery.Journals, "active": recovery.Active, "needs_attention": recovery.NeedsAttention, "current_mismatch": recovery.CurrentMismatch, "invalid": recovery.Invalid, "latest_phase": recovery.LatestPhase, "bounded": recovery.Bounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckImportRecoveryState, "import recovery journals agree with CURRENT", "no action is required", map[string]any{"journals": recovery.Journals, "active": 0, "needs_attention": 0, "current_mismatch": 0, "invalid": 0, "latest_phase": recovery.LatestPhase, "bounded": false}))
	}
}

func (service *Service) inspectMedia(ctx context.Context, builder *reportBuilder, root string, current runtimefs.Generation, database *sqlitestore.DiagnosticDatabase) {
	originalInfo, originalErr := os.Lstat(current.OriginalsDir)
	cacheRoot := filepath.Join(root, "cache", "previews")
	cacheInfo, cacheErr := os.Lstat(cacheRoot)
	originalReady := originalErr == nil && originalInfo.IsDir() && originalInfo.Mode()&os.ModeSymlink == 0 && directoryReadable(current.OriginalsDir) && permissionAllowsWrite(originalInfo)
	cacheReady := cacheErr == nil && cacheInfo.IsDir() && cacheInfo.Mode()&os.ModeSymlink == 0 && directoryReadable(cacheRoot) && permissionAllowsWrite(cacheInfo)
	if !originalReady || !cacheReady {
		builder.set(failed(CheckMediaDirectory, "media authority or preview cache directories are missing, unsafe, or inaccessible", "restore read, traverse, and owner-write permissions on media/original and the preview cache root", map[string]any{"originals_ready": originalReady, "preview_root_ready": cacheReady}, failureIO))
	} else {
		builder.set(pass(CheckMediaDirectory, "media authority and preview cache roots are safe", "no action is required", map[string]any{"originals_ready": true, "preview_root_ready": true}))
	}
	if originalErr != nil || !originalInfo.IsDir() {
		service.skipMediaDetails(builder, "media_directory_failed")
		return
	}
	records, bounded, recordsErr := database.MediaRecords(ctx, service.options.MaxMediaRecords)
	if recordsErr != nil || bounded {
		builder.set(failed(CheckMediaReferenceConsistency, "the bounded media authority catalog could not be read completely", "reduce corruption, verify schema, and rerun doctor", map[string]any{"records": len(records), "bounded": bounded}, failureIntegrity))
		for _, id := range []string{CheckMissingMedia, CheckOrphanMedia, CheckMediaChecksum, CheckPreviewCache} {
			builder.set(skipped(id, "media_catalog_failed"))
		}
		return
	}
	repository, err := sqlitestore.NewMediaReferenceRepository(database)
	var states []app.MediaReferenceState
	if err == nil {
		err = repository.Read(ctx, func(store app.MediaReferenceStore) error {
			var readErr error
			states, readErr = store.MediaReferenceStates(ctx)
			return readErr
		})
	}
	if err != nil {
		builder.set(failed(CheckMediaReferenceConsistency, "the central media reference resolver could not be evaluated", "repair relational media references before running maintenance", map[string]any{"records": len(records), "resolver_ready": false}, failureIntegrity))
		for _, id := range []string{CheckMissingMedia, CheckOrphanMedia, CheckMediaChecksum, CheckPreviewCache} {
			builder.set(skipped(id, "media_resolver_failed"))
		}
		return
	}
	stateByID := make(map[string]app.MediaReferenceState, len(states))
	inconsistent := 0
	for _, state := range states {
		stateByID[state.MediaID.String()] = state
		if state.Referenced == (state.UnreferencedSince != nil) {
			inconsistent++
		}
	}
	if len(states) != len(records) {
		inconsistent += abs(len(states) - len(records))
	}
	if inconsistent != 0 {
		builder.set(failed(CheckMediaReferenceConsistency, "media reachability and unreferenced clocks disagree", "repair references through the shared media resolver before prune or export", map[string]any{"records": len(records), "inconsistent": inconsistent}, failureIntegrity))
	} else {
		builder.set(pass(CheckMediaReferenceConsistency, "media reachability and unreferenced clocks agree", "no action is required", map[string]any{"records": len(records), "inconsistent": 0}))
	}
	missingReferenced, corrupt, readyPreviews, stalePreviews, missingPreviews := 0, 0, 0, 0, 0
	expectedPaths := make(map[string]struct{}, len(records))
	unreferencedRecords := 0
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		value, convertErr := media.DiagnosticMedia(record.ID, record.SHA256, record.StoragePath, record.OriginalName, record.MIMEType, record.ByteSize, record.Width, record.Height, record.CreatedAtMS)
		state := stateByID[record.ID]
		if !state.Referenced {
			unreferencedRecords++
		}
		if convertErr != nil {
			corrupt++
			continue
		}
		expectedPaths[filepath.Clean(filepath.FromSlash(record.StoragePath))] = struct{}{}
		original, verifyErr := media.DiagnoseOriginal(ctx, current.Root, value, true)
		if !original.Present && state.Referenced {
			missingReferenced++
		}
		if verifyErr != nil {
			corrupt++
		}
		previewState, previewErr := media.DiagnosePreview(ctx, root, record.SHA256)
		if previewErr != nil {
			if ctx.Err() != nil {
				return
			}
			stalePreviews++
			continue
		}
		switch previewState {
		case "ready":
			readyPreviews++
		case "stale":
			stalePreviews++
		default:
			missingPreviews++
		}
	}
	if missingReferenced != 0 {
		builder.set(failed(CheckMissingMedia, "referenced immutable originals are missing", "restore the missing originals from a verified backup", map[string]any{"missing_referenced": missingReferenced}, failureIntegrity))
	} else {
		builder.set(pass(CheckMissingMedia, "all referenced immutable originals are present", "no action is required", map[string]any{"missing_referenced": 0}))
	}
	if corrupt != 0 {
		builder.set(failed(CheckMediaChecksum, "media metadata, checksum, type, or dimensions do not match immutable originals", "restore corrupt originals from a verified backup before export", map[string]any{"checked": len(records), "corrupt": corrupt}, failureIntegrity))
	} else {
		builder.set(pass(CheckMediaChecksum, "all media originals match their authoritative metadata", "no action is required", map[string]any{"checked": len(records), "corrupt": 0}))
	}
	orphanBlobs, scanBounded, scanErr := scanOrphanBlobs(ctx, current.Root, current.OriginalsDir, expectedPaths, service.options.MaxOriginalFiles)
	if scanErr != nil || scanBounded {
		builder.set(failed(CheckOrphanMedia, "the original media tree exceeded bounds or could not be scanned safely", "remove unsafe filesystem entries only after preserving a backup", map[string]any{"unreferenced_records": unreferencedRecords, "orphan_blobs": orphanBlobs, "bounded": scanBounded}, failureIO))
	} else if unreferencedRecords != 0 || orphanBlobs != 0 {
		builder.set(warning(CheckOrphanMedia, "unreferenced records or untracked original blobs are present", "inspect 'moment media prune --dry-run --json'; do not delete referenced originals", map[string]any{"unreferenced_records": unreferencedRecords, "orphan_blobs": orphanBlobs, "bounded": false}))
	} else {
		builder.set(pass(CheckOrphanMedia, "no orphan media records or blobs were found", "no action is required", map[string]any{"unreferenced_records": 0, "orphan_blobs": 0, "bounded": false}))
	}
	if stalePreviews != 0 || missingPreviews != 0 {
		builder.set(warning(CheckPreviewCache, "derived previews are missing or stale", "run 'moment media rebuild-previews --dry-run --json' and rebuild if desired", map[string]any{"ready": readyPreviews, "stale": stalePreviews, "missing": missingPreviews, "recipe_version": media.RecipeVersion}))
	} else {
		builder.set(pass(CheckPreviewCache, "all current-recipe previews are ready", "no action is required", map[string]any{"ready": readyPreviews, "stale": 0, "missing": 0, "recipe_version": media.RecipeVersion}))
	}
}

func (service *Service) inspectSite(ctx context.Context, builder *reportBuilder, database *sqlitestore.DiagnosticDatabase, locker *runtimefs.Locker, development bool) {
	if locker == nil {
		builder.set(skipped(CheckSiteConfiguration, "lock_dependency_failed"))
		return
	}
	repository, err := sqlitestore.NewSiteRepository(database, locker)
	if err == nil {
		var site *app.SiteService
		site, err = app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: development})
		if err == nil {
			err = site.Ready(ctx)
		}
	}
	if err != nil {
		builder.set(failed(CheckSiteConfiguration, "stored site settings violate the application contract", "repair timezone, public URL, singleton, or managed avatar configuration", map[string]any{"valid": false}, failureIntegrity))
	} else {
		builder.set(pass(CheckSiteConfiguration, "stored site settings satisfy the application contract", "no action is required", map[string]any{"valid": true}))
	}
}

func (service *Service) inspectSecrets(builder *reportBuilder, root string) {
	path := filepath.Join(root, "secrets.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		builder.set(failed(CheckSecretPermissions, "owner secrets are not initialized", "run 'moment owner init' while the server is stopped", map[string]any{"exists": false, "permissions_valid": false}, failureIntegrity))
		return
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		builder.set(failed(CheckSecretPermissions, "owner secrets are missing or unsafe", "restore a regular private secrets.json without exposing its contents", map[string]any{"exists": err == nil, "permissions_valid": false}, failureIO))
		return
	}
	if err := runtimefs.VerifyPrivateFilePermissions(path); err != nil {
		builder.set(failed(CheckSecretPermissions, "owner secrets permissions are not private", "restrict secrets.json to the supported private-file policy", map[string]any{"exists": true, "permissions_valid": false, "platform": runtime.GOOS}, failureIntegrity))
		return
	}
	builder.set(pass(CheckSecretPermissions, "owner secrets exist with private permissions", "no action is required", map[string]any{"exists": true, "permissions_valid": true, "platform": runtime.GOOS}))
}

func (service *Service) inspectDisk(builder *reportBuilder, root string) {
	available, err := runtimefs.AvailableBytes(root)
	if err != nil {
		builder.set(failed(CheckDiskSpace, "available disk space could not be measured", "check filesystem availability and permissions", map[string]any{"measured": false}, failureIO))
		return
	}
	evidence := map[string]any{"available_bytes": available, "warning_threshold_bytes": lowDiskWarningBytes}
	if available < lowDiskWarningBytes {
		builder.set(warning(CheckDiskSpace, "available disk space is below the diagnostic reserve", "free disk space before import, backup, or media growth", evidence))
		return
	}
	builder.set(pass(CheckDiskSpace, "available disk space is above the diagnostic reserve", "no action is required", evidence))
}

func (service *Service) inspectCurrentNext(builder *reportBuilder, root string) {
	info, err := os.Lstat(filepath.Join(root, "CURRENT.next"))
	if errors.Is(err, os.ErrNotExist) {
		builder.set(pass(CheckCurrentNextState, "no uncommitted CURRENT.next is present", "no action is required", map[string]any{"present": false}))
		return
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		builder.set(failed(CheckCurrentNextState, "CURRENT.next is unsafe or unreadable", "inspect the interrupted generation switch; never promote CURRENT.next automatically", map[string]any{"present": err == nil, "safe_regular_file": false}, failureIntegrity))
		return
	}
	builder.set(warning(CheckCurrentNextState, "an uncommitted CURRENT.next remnant is present", "verify CURRENT and the import journal, then use explicit recovery; never auto-promote the remnant", map[string]any{"present": true, "safe_regular_file": true}))
}

func (service *Service) skipDatabaseAndMediaDeep(builder *reportBuilder, reason string) {
	for _, id := range []string{CheckSQLiteIntegrity, CheckForeignKeys, CheckFTS5Capability, CheckFTSConsistency, CheckMediaDirectory, CheckMissingMedia, CheckOrphanMedia, CheckMediaReferenceConsistency, CheckMediaChecksum, CheckPreviewCache, CheckStaleImportStaging, CheckImportRecoveryState} {
		builder.set(skipped(id, reason))
	}
}

func (service *Service) skipMediaDetails(builder *reportBuilder, reason string) {
	for _, id := range []string{CheckMissingMedia, CheckOrphanMedia, CheckMediaReferenceConsistency, CheckMediaChecksum, CheckPreviewCache} {
		builder.set(skipped(id, reason))
	}
}

func probeServeLock(ctx context.Context, locker *runtimefs.Locker) (string, error) {
	if !locker.HasPersistentLock(runtimefs.ServeLock) {
		return "not_initialized", nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, serveProbeWait)
	lease, err := locker.Acquire(probeCtx, runtimefs.ServeLock, runtimefs.Exclusive)
	cancel()
	if err == nil {
		return "available", lease.Release()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "held", nil
	}
	return "", err
}

func directoryReadable(path string) bool {
	directory, err := os.Open(path)
	if err != nil {
		return false
	}
	_, readErr := directory.ReadDir(1)
	closeErr := directory.Close()
	return (readErr == nil || errors.Is(readErr, io.EOF)) && closeErr == nil
}

func permissionAllowsWrite(info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o200 != 0
}

func permissionAllowsRead(info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o400 != 0
}

func scanOrphanBlobs(ctx context.Context, generationRoot, originalsRoot string, expected map[string]struct{}, limit int) (int, bool, error) {
	entriesSeen, orphan := 0, 0
	directories := []string{originalsRoot}
	for len(directories) != 0 {
		if err := ctx.Err(); err != nil {
			return orphan, false, err
		}
		last := len(directories) - 1
		directoryPath := directories[last]
		directories = directories[:last]
		before, err := os.Lstat(directoryPath)
		if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return orphan, false, errors.Join(errors.New("unsafe directory in original media tree"), err)
		}
		directory, err := os.Open(directoryPath)
		if err != nil {
			return orphan, false, err
		}
		opened, statErr := directory.Stat()
		if statErr != nil || !os.SameFile(before, opened) {
			_ = directory.Close()
			return orphan, false, errors.Join(errors.New("original media directory changed while opening"), statErr)
		}
		for {
			entries, readErr := directory.ReadDir(128)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					_ = directory.Close()
					return orphan, false, err
				}
				entriesSeen++
				if entriesSeen > limit {
					_ = directory.Close()
					return 0, true, nil
				}
				path := filepath.Join(directoryPath, entry.Name())
				info, inspectErr := os.Lstat(path)
				if inspectErr != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
					_ = directory.Close()
					return orphan, false, errors.Join(errors.New("unsafe entry in original media tree"), inspectErr)
				}
				if info.IsDir() {
					directories = append(directories, path)
					continue
				}
				relative, relativeErr := filepath.Rel(generationRoot, path)
				if relativeErr != nil {
					_ = directory.Close()
					return orphan, false, relativeErr
				}
				if _, ok := expected[filepath.Clean(relative)]; !ok {
					orphan++
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = directory.Close()
				return orphan, false, readErr
			}
		}
		if err := directory.Close(); err != nil {
			return orphan, false, err
		}
	}
	return orphan, false, nil
}

func errIfCancelled(err error) error {
	if cancelled(err) {
		return err
	}
	return nil
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
