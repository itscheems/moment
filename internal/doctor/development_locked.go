package doctor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// runLockedDevelopment never resolves the instance pathname. Every filesystem
// observation is relative to the same root/generation handles that protect the
// unpublished fixture and its CURRENT switch.
func (service *Service) runLockedDevelopment(ctx context.Context, mode Mode) (Report, error) {
	locked := service.options.LockedSnapshot
	builder := newReport(mode, service.options.Progress)
	if locked == nil || locked.Authority == nil || locked.Authority.Validate(locked.Runtime, locked.Held) != nil {
		return Report{}, runtimefs.ErrInsufficientLocks
	}
	root := locked.Authority.RootAuthority()
	generationRoot := locked.Authority.GenerationRoot()
	expected := locked.Authority.GenerationAuthority()

	rootInfo, rootErr := root.Stat(".")
	rootReady := rootErr == nil && rootInfo.IsDir() && rootInfo.Mode()&os.ModeSymlink == 0
	if rootReady {
		builder.set(pass(CheckDataDir, "the data directory is present and accessible", "no action is required", map[string]any{"readable": true, "traversable_permission": true, "writable_permission": true}))
	} else {
		builder.set(failed(CheckDataDir, "the data directory is missing or unsafe", "initialize or restore the selected Moment data directory", map[string]any{"exists": rootErr == nil, "directory": rootErr == nil && rootInfo.IsDir()}, failureIO))
	}
	service.inspectLockedSecrets(builder, root)
	service.inspectLockedCurrentNext(builder, root)
	service.inspectLockedDisk(builder, root)
	service.inspectLockedLocks(builder, root, locked.Held)

	current, currentErr := locked.Runtime.ResolveCurrentDevelopment(root, expected.InstanceRoot)
	authorityErr := locked.Authority.Validate(locked.Runtime, locked.Held)
	currentReady := currentErr == nil && current.ID == expected.ID && authorityErr == nil
	if currentReady {
		builder.set(pass(CheckCurrentGeneration, "CURRENT selects one structurally complete generation", "no action is required", map[string]any{
			"valid": true, "generation_id": current.ID, "generation_readable": true,
			"database_readable_permission": true, "authority_permissions_valid": true,
		}))
	} else {
		builder.set(failed(CheckCurrentGeneration, "CURRENT does not select the pinned complete generation", "restore a valid CURRENT pointer explicitly; never promote CURRENT.next automatically", map[string]any{"valid": false, "resolved": currentErr == nil, "id_matches": current.ID == expected.ID, "authority_matches": authorityErr == nil}, failureIntegrity))
		for _, id := range []string{CheckDatabaseOpen, CheckSchemaVersion, CheckSiteConfiguration, CheckAccountIntegrity} {
			builder.set(skipped(id, "current_generation_failed"))
		}
		if mode == ModeDeep {
			service.skipDatabaseAndMediaDeep(builder, "current_generation_failed")
		}
		return builder.finish(ctx.Err() != nil), errIfCancelled(ctx.Err())
	}

	var database *sqlitestore.DiagnosticDatabase
	var dbErr error
	closeDatabase := false
	if locked.DiagnosticDatabase != nil {
		database = locked.DiagnosticDatabase
	} else {
		database, dbErr = sqlitestore.NewDiagnosticView(locked.Database)
		closeDatabase = dbErr == nil
	}
	if dbErr != nil {
		builder.set(failed(CheckDatabaseOpen, "the selected database cannot be opened read-only", "restore database readability or recover a valid generation", map[string]any{"opened": false, "bounded": false}, failureIntegrity))
		builder.set(skipped(CheckSchemaVersion, "database_not_open"))
		builder.set(skipped(CheckSiteConfiguration, "database_not_open"))
		builder.set(skipped(CheckAccountIntegrity, "database_not_open"))
		if mode == ModeDeep {
			service.skipDatabaseAndMediaDeep(builder, "database_not_open")
		}
		return builder.finish(ctx.Err() != nil), errIfCancelled(ctx.Err())
	}
	if closeDatabase {
		defer func() { _ = database.Close() }()
	}
	builder.set(pass(CheckDatabaseOpen, "the selected database opens in strict read-only mode", "no action is required", map[string]any{"read_only": true, "query_only": true}))
	schema, schemaErr := database.Schema(ctx)
	if schemaErr != nil {
		builder.set(failed(CheckSchemaVersion, "the migration ledger or required schema catalog has drifted", "restore a database with the exact supported migration prefix", map[string]any{"applied_version": schema.AppliedVersion, "expected_version": schema.ExpectedVersion, "exact": false}, failureIntegrity))
	} else {
		builder.set(pass(CheckSchemaVersion, "the schema and migration ledger match this binary", "no action is required", map[string]any{"applied_version": schema.AppliedVersion, "expected_version": schema.ExpectedVersion, "exact": true}))
	}
	service.inspectSite(ctx, builder, database, locked.Runtime.Locks, true)
	if mode == ModeSummary {
		service.skipDatabaseAndMediaDeep(builder, "requires_deep")
	} else {
		service.runLockedDatabaseDeep(ctx, builder, database, schemaErr == nil, locked.Runtime)
		if ctx.Err() == nil {
			service.inspectLockedMedia(ctx, builder, root, generationRoot, database)
		}
		if ctx.Err() == nil {
			service.inspectLockedStaging(builder, root)
		}
	}
	if ctx.Err() != nil {
		return builder.finish(true), ctx.Err()
	}
	after, afterErr := locked.Runtime.ResolveCurrentDevelopment(root, expected.InstanceRoot)
	if afterErr != nil || after.ID != current.ID || locked.Authority.Validate(locked.Runtime, locked.Held) != nil {
		builder.set(failed(CheckCurrentGeneration, "CURRENT changed during the diagnostic snapshot", "retry after import activity stops and inspect the lock matrix", map[string]any{"stable": false}, failureIntegrity))
	}
	return builder.finish(false), nil
}

func (service *Service) runLockedDatabaseDeep(ctx context.Context, builder *reportBuilder, database *sqlitestore.DiagnosticDatabase, schemaReady bool, runtimeState *runtimefs.Runtime) {
	integrity, err := database.Integrity(ctx)
	if err != nil || !integrity.OK {
		builder.set(failed(CheckSQLiteIntegrity, "SQLite integrity verification failed", "restore a verified backup or recover the affected generation", map[string]any{"problems": integrity.Problems, "bounded": integrity.Bounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckSQLiteIntegrity, "SQLite reports no structural integrity errors", "no action is required", map[string]any{"problems": 0, "bounded": false}))
	}
	foreign, err := database.ForeignKeys(ctx)
	if err != nil || foreign.Violations != 0 {
		builder.set(failed(CheckForeignKeys, "foreign-key violations were detected", "restore or repair the authoritative relational data before serving writes", map[string]any{"violations": foreign.Violations, "bounded": foreign.Bounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckForeignKeys, "no foreign-key violations were detected", "no action is required", map[string]any{"violations": 0, "bounded": false}))
	}
	fts, err := database.FTSCapability(ctx)
	if err != nil || !fts.FTS5 || !fts.Trigram {
		builder.set(failed(CheckFTS5Capability, "FTS5 trigram capability is unavailable or malformed", "use the supported Moment binary and restore the required entry_fts schema", map[string]any{"fts5": fts.FTS5, "trigram": fts.Trigram}, failureIntegrity))
	} else {
		builder.set(pass(CheckFTS5Capability, "FTS5 and trigram tokenization are available", "no action is required", map[string]any{"fts5": true, "trigram": true}))
	}
	if !schemaReady {
		builder.set(skipped(CheckFTSConsistency, "schema_or_lock_dependency_failed"))
		return
	}
	repository, repositoryErr := sqlitestore.NewSearchRepository(database, runtimeState.Locks)
	search, serviceErr := app.NewSearchService(repository)
	if repositoryErr != nil || serviceErr != nil {
		builder.set(failed(CheckFTSConsistency, "the search diagnostic adapter could not be composed", "verify the current schema and retry", map[string]any{"checked": false}, failureInternal))
		return
	}
	result, diagnoseErr := search.DiagnoseDerived(ctx, service.options.MaxMediaRecords)
	if diagnoseErr != nil || result.Drifted != 0 || result.OrphanIndexRows != 0 || result.Bounded || result.StoredVersion != result.ExpectedVersion {
		builder.set(failed(CheckFTSConsistency, "derived search rows do not match authoritative Markdown", "run 'moment search rebuild --dry-run --json', then rebuild the derived index explicitly", map[string]any{"total": result.Total, "drifted": result.Drifted, "orphan_rows": result.OrphanIndexRows, "stored_version": result.StoredVersion, "expected_version": result.ExpectedVersion, "bounded": result.Bounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckFTSConsistency, "derived search rows match authoritative Markdown", "no action is required", map[string]any{"total": result.Total, "drifted": 0, "orphan_rows": 0, "bounded": false}))
	}
}

func (service *Service) inspectLockedMedia(ctx context.Context, builder *reportBuilder, root, generationRoot *os.Root, database *sqlitestore.DiagnosticDatabase) {
	originalInfo, originalErr := generationRoot.Lstat(filepath.Join("media", "original"))
	cacheInfo, cacheErr := root.Lstat(filepath.Join("cache", "previews"))
	originalReady := originalErr == nil && originalInfo.IsDir() && originalInfo.Mode()&os.ModeSymlink == 0 && privateMode(originalInfo, true)
	cacheReady := cacheErr == nil && cacheInfo.IsDir() && cacheInfo.Mode()&os.ModeSymlink == 0 && privateMode(cacheInfo, true)
	if originalReady && cacheReady {
		builder.set(pass(CheckMediaDirectory, "media authority and preview cache roots are safe", "no action is required", map[string]any{"originals_ready": true, "preview_root_ready": true}))
	} else {
		builder.set(failed(CheckMediaDirectory, "media authority or preview cache directories are missing, unsafe, or inaccessible", "restore read, traverse, and owner-write permissions on media/original and the preview cache root", map[string]any{"originals_ready": originalReady, "preview_root_ready": cacheReady}, failureIO))
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
		service.skipMediaDetails(builder, "media_resolver_failed")
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
	if inconsistent == 0 {
		builder.set(pass(CheckMediaReferenceConsistency, "media reachability and unreferenced clocks agree", "no action is required", map[string]any{"records": len(records), "inconsistent": 0}))
	} else {
		builder.set(failed(CheckMediaReferenceConsistency, "media reachability and unreferenced clocks disagree", "repair references through the shared media resolver before prune or export", map[string]any{"records": len(records), "inconsistent": inconsistent}, failureIntegrity))
	}
	missing, corrupt, mediaIO, readyPreviews, stalePreviews, missingPreviews, unreferenced := 0, 0, 0, 0, 0, 0, 0
	expectedPaths := make(map[string]struct{}, len(records))
	for _, record := range records {
		value, convertErr := media.DiagnosticMedia(record.ID, record.SHA256, record.StoragePath, record.OriginalName, record.MIMEType, record.ByteSize, record.Width, record.Height, record.CreatedAtMS)
		state := stateByID[record.ID]
		if !state.Referenced {
			unreferenced++
		}
		if convertErr != nil {
			corrupt++
			continue
		}
		expectedPaths[filepath.Clean(filepath.FromSlash(record.StoragePath))] = struct{}{}
		original, verifyErr := media.DiagnoseOriginalWithinRoot(ctx, generationRoot, value, true)
		if !original.Present && state.Referenced {
			missing++
		}
		if errors.Is(verifyErr, media.ErrDiagnosticIO) {
			mediaIO++
		} else if verifyErr != nil {
			corrupt++
		}
		preview, previewErr := media.DiagnosePreviewWithinRoot(ctx, root, record.SHA256)
		if previewErr != nil {
			stalePreviews++
		} else if preview == "ready" {
			readyPreviews++
		} else if preview == "stale" {
			stalePreviews++
		} else {
			missingPreviews++
		}
	}
	if missing == 0 {
		builder.set(pass(CheckMissingMedia, "all referenced immutable originals are present", "no action is required", map[string]any{"missing_referenced": 0}))
	} else {
		builder.set(failed(CheckMissingMedia, "referenced immutable originals are missing", "restore the missing originals from a verified backup", map[string]any{"missing_referenced": missing}, failureIntegrity))
	}
	if mediaIO != 0 || corrupt != 0 {
		builder.set(failed(CheckMediaChecksum, "media metadata or immutable bytes do not match", "restore corrupt originals from a verified backup before export", map[string]any{"checked": len(records), "corrupt": corrupt, "io_failures": mediaIO}, failureIntegrity))
	} else {
		builder.set(pass(CheckMediaChecksum, "all media originals match their authoritative metadata", "no action is required", map[string]any{"checked": len(records), "corrupt": 0, "io_failures": 0}))
	}
	orphans, scanBounded, scanErr := scanRootedOrphans(ctx, generationRoot, expectedPaths, service.options.MaxOriginalFiles)
	if scanErr != nil || scanBounded {
		builder.set(failed(CheckOrphanMedia, "the original media tree exceeded bounds or could not be scanned safely", "remove unsafe filesystem entries only after preserving a backup", map[string]any{"unreferenced_records": unreferenced, "orphan_blobs": orphans, "bounded": scanBounded}, failureIO))
	} else if unreferenced != 0 || orphans != 0 {
		builder.set(warning(CheckOrphanMedia, "unreferenced records or untracked original blobs are present", "inspect 'moment media prune --dry-run --json'; do not delete referenced originals", map[string]any{"unreferenced_records": unreferenced, "orphan_blobs": orphans, "bounded": false}))
	} else {
		builder.set(pass(CheckOrphanMedia, "no orphan media records or blobs were found", "no action is required", map[string]any{"unreferenced_records": 0, "orphan_blobs": 0, "bounded": false}))
	}
	if stalePreviews != 0 || missingPreviews != 0 {
		builder.set(warning(CheckPreviewCache, "derived previews are missing or stale", "run 'moment media rebuild-previews --dry-run --json' and rebuild if desired", map[string]any{"ready": readyPreviews, "stale": stalePreviews, "missing": missingPreviews, "recipe_version": media.RecipeVersion}))
	} else {
		builder.set(pass(CheckPreviewCache, "all current-recipe previews are ready", "no action is required", map[string]any{"ready": readyPreviews, "stale": 0, "missing": 0, "recipe_version": media.RecipeVersion}))
	}
}

func scanRootedOrphans(ctx context.Context, generationRoot *os.Root, expected map[string]struct{}, limit int) (int, bool, error) {
	seen, orphan := 0, 0
	err := fs.WalkDir(generationRoot.FS(), filepath.Join("media", "original"), func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := generationRoot.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.Join(errors.New("unsafe rooted media entry"), err)
		}
		if info.IsDir() {
			return nil
		}
		seen++
		if seen > limit {
			return fs.SkipAll
		}
		if _, ok := expected[filepath.Clean(path)]; !ok {
			orphan++
		}
		return nil
	})
	return orphan, seen > limit, err
}

func (service *Service) inspectLockedSecrets(builder *reportBuilder, root *os.Root) {
	info, err := root.Lstat("secrets.json")
	if errors.Is(err, os.ErrNotExist) {
		builder.set(failed(CheckSecretPermissions, "owner secrets are not initialized", "run 'moment owner init' while the server is stopped", map[string]any{"exists": false, "permissions_valid": false}, failureIntegrity))
	} else if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !privateMode(info, false) {
		builder.set(failed(CheckSecretPermissions, "owner secrets are missing or unsafe", "restore a regular private secrets.json without exposing its contents", map[string]any{"exists": err == nil, "permissions_valid": false}, failureIO))
	} else {
		builder.set(pass(CheckSecretPermissions, "owner secrets exist with private permissions", "no action is required", map[string]any{"exists": true, "permissions_valid": true, "platform": runtime.GOOS}))
	}
}

func (service *Service) inspectLockedCurrentNext(builder *reportBuilder, root *os.Root) {
	info, err := root.Lstat("CURRENT.next")
	if errors.Is(err, os.ErrNotExist) {
		builder.set(pass(CheckCurrentNextState, "no uncommitted CURRENT.next is present", "no action is required", map[string]any{"present": false}))
	} else if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !privateMode(info, false) {
		builder.set(failed(CheckCurrentNextState, "CURRENT.next is unsafe or unreadable", "inspect the interrupted generation switch; never promote CURRENT.next automatically", map[string]any{"present": err == nil, "safe_regular_file": false}, failureIntegrity))
	} else {
		builder.set(warning(CheckCurrentNextState, "an uncommitted CURRENT.next remnant is present", "verify CURRENT and the import journal, then use explicit recovery; never auto-promote the remnant", map[string]any{"present": true, "safe_regular_file": true}))
	}
}

func (service *Service) inspectLockedDisk(builder *reportBuilder, root *os.Root) {
	available, err := runtimefs.AvailableBytesRoot(root)
	if err != nil {
		builder.set(failed(CheckDiskSpace, "available disk space could not be measured", "check filesystem availability and permissions", map[string]any{"measured": false}, failureIO))
		return
	}
	evidence := map[string]any{"available_bytes": available, "warning_threshold_bytes": lowDiskWarningBytes}
	if available < lowDiskWarningBytes {
		builder.set(warning(CheckDiskSpace, "available disk space is below the diagnostic reserve", "free disk space before import, backup, or media growth", evidence))
	} else {
		builder.set(pass(CheckDiskSpace, "available disk space is above the diagnostic reserve", "no action is required", evidence))
	}
}

func (service *Service) inspectLockedLocks(builder *reportBuilder, root *os.Root, held *runtimefs.LeaseSet) {
	valid := held != nil
	for _, name := range []runtimefs.LockName{runtimefs.ImportLock, runtimefs.ServeLock, runtimefs.MaintenanceLock, runtimefs.MediaLock} {
		info, err := root.Lstat(filepath.Join("locks", string(name)+".lock"))
		valid = valid && err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && privateMode(info, false)
	}
	if valid {
		builder.set(pass(CheckInstanceLocks, "the OS-backed lock matrix is available", "no action is required", map[string]any{"snapshot_acquired": true, "permissions_valid": true, "serve_state": "owned_by_development_operation"}))
	} else {
		builder.set(failed(CheckInstanceLocks, "the read-only diagnostic snapshot could not be verified", "repair persistent lock files, then retry", map[string]any{"snapshot_acquired": true, "permissions_valid": false}, failureIO))
	}
}

func (service *Service) inspectLockedStaging(builder *reportBuilder, root *os.Root) {
	imports, importInvalid, importBounded, importErr := inspectRootedStateTree(root, filepath.Join("staging", "import"), service.options.MaxStagingItems)
	statuses, statusInvalid, statusBounded, statusErr := inspectRootedStateTree(root, filepath.Join("staging", "import-status"), service.options.MaxStagingItems)
	if importErr != nil || importInvalid || importBounded {
		builder.set(failed(CheckStaleImportStaging, "import staging contains invalid or incomplete state", "finish or explicitly discard the diagnosed staging operation before retrying import", map[string]any{"total": imports, "invalid": boolInt(importInvalid), "bounded": importBounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckStaleImportStaging, "import staging state is valid", "no action is required", map[string]any{"total": imports, "ready": 0, "expired": 0, "committing": 0, "invalid": 0, "bounded": false}))
	}
	if statusErr != nil || statusInvalid || statusBounded {
		builder.set(failed(CheckImportRecoveryState, "import recovery journals require attention", "restore the journal-selected generation explicitly", map[string]any{"journals": statuses, "active": 0, "needs_attention": 0, "invalid": boolInt(statusInvalid), "bounded": statusBounded}, failureIntegrity))
	} else {
		builder.set(pass(CheckImportRecoveryState, "import recovery journals agree with CURRENT", "no action is required", map[string]any{"journals": statuses, "active": 0, "needs_attention": 0, "current_mismatch": 0, "invalid": 0, "bounded": false}))
	}
}

func inspectRootedStateTree(root *os.Root, start string, limit int) (count int, invalid, bounded bool, resultErr error) {
	resultErr = fs.WalkDir(root.FS(), start, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == start {
			return nil
		}
		count++
		if count > limit {
			bounded = true
			return fs.SkipAll
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || !privateMode(info, info.IsDir()) {
			invalid = true
			if info.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	return count, invalid, bounded, resultErr
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func privateMode(info os.FileInfo, directory bool) bool {
	if info == nil || info.IsDir() != directory {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o077 == 0
}
