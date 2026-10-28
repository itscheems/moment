package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// AccountImportOptions always appends into the authenticated/selected account.
// ExpectedSHA binds commit to the archive reviewed by the caller.
type AccountImportOptions struct {
	DryRun      bool
	Offline     bool
	ExpectedSHA string
}

// AccountImportResult describes the reviewed input and the changes actually added.
type AccountImportResult struct {
	DryRun        bool         `json:"dry_run"`
	ArchiveSHA256 string       `json:"archive_sha256"`
	Counts        ImportCounts `json:"counts"`
	Added         ImportCounts `json:"added"`
	Skipped       ImportCounts `json:"skipped"`
	AccountID     string       `json:"account_id"`
}

// ImportAccount shares portable validation and current-schema reconstruction
// with site recovery, then appends only content in one transaction. CURRENT,
// existing rows, account profiles and credentials are never replaced.
func (exporter *Exporter) ImportAccount(ctx context.Context, source io.Reader, accountID string, options AccountImportOptions) (result AccountImportResult, resultErr error) {
	if exporter == nil || source == nil || domain.ValidateUserID(accountID) != nil {
		return result, domain.NewError(domain.CodeInvalidArgument, "personal import requires an account and archive", nil)
	}
	if !options.DryRun && (!isLowerHex(options.ExpectedSHA) || len(options.ExpectedSHA) != 64) {
		return result, domain.NewError(domain.CodeInvalidArgument, "review the archive before importing it", nil)
	}
	state, err := runtimefs.OpenForCoordination(exporter.dataDir)
	if err != nil {
		return result, err
	}
	held, err := state.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	if options.Offline {
		lockCtx, cancel := context.WithTimeout(ctx, directServeLockWait)
		err = state.Locks.ExtendSet(lockCtx, held, runtimefs.LockRequest{Name: runtimefs.ServeLock, Mode: runtimefs.Exclusive})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, domain.NewError(domain.CodeInstanceInUse, "Moment is serving this instance", nil)
		}
	}
	if err = state.Locks.ExtendSet(ctx, held, runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Shared}, runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive}); err != nil {
		return result, err
	}
	state.Current, err = state.ResolveCurrent()
	if err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp(filepath.Join(state.Root, "staging"), "account-import-")
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(stage)) }()
	archivePath := filepath.Join(stage, "account.zip")
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	digest := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(file, digest), io.LimitReader(contextReader{ctx: ctx, reader: source}, defaultMaxArchiveBytes+1), make([]byte, copyBufferSize))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return result, errors.Join(copyErr, closeErr)
	}
	if n > defaultMaxArchiveBytes {
		return result, importInvalid("personal archive exceeds its byte budget", nil)
	}
	result.ArchiveSHA256 = hex.EncodeToString(digest.Sum(nil))
	result.AccountID = accountID
	result.DryRun = options.DryRun
	if !options.DryRun && options.ExpectedSHA != result.ArchiveSHA256 {
		return result, importInvalid("personal archive differs from the reviewed file", nil)
	}
	if err = ValidateAccountFileContext(ctx, archivePath); err != nil {
		return result, err
	}
	budget, err := zip.OpenReader(archivePath)
	if err != nil {
		return result, err
	}
	var expanded uint64
	for _, file := range budget.File {
		expanded += file.UncompressedSize64
	}
	if err = budget.Close(); err != nil {
		return result, err
	}
	available, err := exporter.availableBytes(stage)
	if err != nil {
		return result, err
	}
	if required := expanded*3 + uint64(minimumSafetyMarginBytes); available < required {
		return result, domain.NewError(domain.CodeImportDiskSpaceInsufficient, "insufficient disk space for personal import", map[string]any{"required_additional_bytes": required, "available_bytes": available})
	}
	converted := filepath.Join(stage, "portable.zip")
	document, accountDocument, err := prepareAccountRestore(ctx, archivePath, converted, exporter.appVersion)
	if err != nil {
		return result, err
	}
	result.Counts = portableImportCounts(document)
	result.Counts.Users = 0
	free, err := exporter.availableBytes(stage)
	if err != nil {
		return result, err
	}
	required := uint64(document.Limits.ExpandedBytes)*3 + uint64(minimumSafetyMarginBytes)
	if free < required {
		return result, domain.NewError(domain.CodeImportDiskSpaceInsufficient, "insufficient disk space for personal import", map[string]any{"required_additional_bytes": required, "available_bytes": free})
	}
	if err = ValidateFileContext(ctx, converted); err != nil {
		return result, err
	}
	generation, err := createStagingGeneration(stage, "account-import")
	if err != nil {
		return result, err
	}
	if err = rebuildPortableGeneration(ctx, state, generation, converted, document); err != nil {
		return result, importInvalid("personal archive could not be reconstructed", err)
	}
	stagedState := &runtimefs.Runtime{Root: state.Root, Instance: state.Instance, Current: generation, Locks: state.Locks}
	staged, err := sqlitestore.Open(ctx, stagedState, sqlitestore.Options{})
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, staged.Close()) }()
	snapshot, err := staged.BeginArchiveSnapshot(ctx)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	active, err := sqlitestore.Open(ctx, state, sqlitestore.Options{})
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, active.Close()) }()
	// Verify the target even for a preview. Ordinary imports cannot create or
	// activate accounts and cannot inherit the source's administrator role.
	var activeStatus string
	err = active.ReadTx(ctx, func(q sqlitestore.Queries) error {
		return q.QueryRowContext(ctx, `SELECT status FROM users WHERE id=?`, accountID).Scan(&activeStatus)
	})
	if err != nil {
		return result, err
	}
	if activeStatus != "active" {
		return result, domain.NewError(domain.CodeUnauthorized, "an active account is required for personal import", nil)
	}
	if options.DryRun {
		return result, nil
	}
	created := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, path := range created {
				resultErr = errors.Join(resultErr, os.Remove(path))
			}
		}
	}()
	added, err := active.AppendAccountSnapshot(ctx, snapshot, accountDocument.SourceInstanceID, accountDocument.AccountID, accountID, func(source, output sqlitestore.ArchiveMedia) error {
		destination := filepath.Join(state.Current.Root, filepath.FromSlash(output.StoragePath))
		if _, err := os.Lstat(destination); err == nil {
			input, err := openOriginal(state.Current.Root, output.StoragePath)
			if err != nil {
				return err
			}
			defer func() { _ = input.Close() }()
			id, err := domain.ParseID(output.ID)
			if err != nil {
				return err
			}
			return media.VerifyPortableOriginal(ctx, input, media.Media{ID: id, SHA256: output.SHA256, StoragePath: output.StoragePath, OriginalName: output.OriginalName, MIMEType: output.MIMEType, ByteSize: output.ByteSize, Width: output.Width, Height: output.Height, CreatedAt: time.UnixMilli(output.CreatedAtMS).UTC()})
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		copyErr := copyResolvedOriginal(ctx, generation.Root, source, state.Current, output)
		if _, statErr := os.Lstat(destination); statErr == nil {
			created = append(created, destination)
		}
		return copyErr
	})
	if err != nil {
		return result, err
	}
	committed = true
	result.Added = personalCounts(added.Added)
	result.Skipped = personalCounts(added.Skipped)
	return result, nil
}

// prepareAccountRestore changes only the staging envelope. The synthetic
// administrator exists in the disposable validation database, never the target.
// This lets the existing archive validator/rebuilder own the complete graph,
// article grammar, media originals, checksums, foreign keys and search index.
func prepareAccountRestore(ctx context.Context, source, destination, appVersion string) (document manifest, personal accountManifest, resultErr error) {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return document, personal, err
	}
	defer func() { resultErr = errors.Join(resultErr, reader.Close()) }()
	files := map[string]*zip.File{}
	for _, file := range reader.File {
		files[file.Name] = file
	}
	raw, err := readSmallZIPMember(ctx, files["manifest.json"], 1<<20)
	if err != nil {
		return document, personal, err
	}
	if _, err = decodePortableJSONObject(raw, &personal, []string{"format", "format_version", "account_id", "counts", "created_at"}); err != nil {
		return document, personal, err
	}
	if personal.FormatVersion != 3 {
		return document, personal, domain.NewError(domain.CodeExportVersionUnsupported, "only personal export format 3 supports restore; export again with a current version", nil)
	}
	if domain.ValidateUserID(personal.SourceInstanceID) != nil || domain.ValidateUserID(personal.AccountID) != nil {
		return document, personal, importInvalid("personal archive source identity is invalid", nil)
	}
	c := personal.Counts
	if c.Entries < 0 || c.Entries > defaultMaxEntries || c.Comments < 0 || c.Comments > defaultMaxComments || c.Media < 0 || c.Media > defaultMaxMedia || c.ArticleDrafts < 0 || c.ArticleDrafts > defaultMaxArticleDrafts || c.Articles < 0 || c.Articles > c.Entries {
		return document, personal, importInvalid("personal archive record counts exceed their budgets", nil)
	}
	raw, err = readSmallZIPMember(ctx, files["data/account.json"], 1<<20)
	if err != nil {
		return document, personal, err
	}
	var account userRecord
	if _, err = decodePortableJSONObject(raw, &account, portableUserFields); err != nil {
		return document, personal, err
	}
	if account.ID != personal.AccountID {
		return document, personal, importInvalid("personal archive account differs from manifest", nil)
	}
	account.Role = "admin"
	account.Status = "active"
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return document, personal, err
	}
	defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
	writer := zip.NewWriter(output)
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, writer.Close())
		}
	}()
	when, err := parsePortableTimestamp(personal.CreatedAt)
	if err != nil {
		return document, personal, err
	}
	protocol := &protocolWriter{zip: writer, createdAt: when}
	if err = protocol.bytesMember("README.txt", zip.Deflate, []byte(readme)); err != nil {
		return document, personal, err
	}
	if err = protocol.jsonMember("data/site.json", siteRecord{ProfileUserID: account.ID, Username: account.Username, DisplayName: account.DisplayName, Title: "Personal restore", Bio: account.Bio, Timezone: "UTC", AvatarMediaID: account.AvatarMediaID, CoverMediaID: account.CoverMediaID, CommentsEnabled: true}); err != nil {
		return document, personal, err
	}
	if _, err = protocol.jsonLines("data/users.jsonl", func(write func(any) error) error { return write(account) }); err != nil {
		return document, personal, err
	}
	if _, err = protocol.jsonLines("data/identities.jsonl", func(func(any) error) error { return nil }); err != nil {
		return document, personal, err
	}
	// Reject out-of-scope authors before handing the graph to the site validator.
	if _, err = validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], portableEntryFields, func(v entryRecord, _ map[string]json.RawMessage) error {
		if v.AuthorUserID != account.ID {
			return importInvalid("personal archive contains another author's entry", nil)
		}
		return nil
	}); err != nil {
		return document, personal, err
	}
	if _, err = validateJSONLines[articleDraftRecord](ctx, files["data/article_drafts.jsonl"], portableArticleDraftFields, func(v articleDraftRecord, _ map[string]json.RawMessage) error {
		if v.AuthorUserID != account.ID {
			return importInvalid("personal archive contains another author's draft", nil)
		}
		return nil
	}); err != nil {
		return document, personal, err
	}
	if _, err = validateJSONLines[commentRecord](ctx, files["data/comments.jsonl"], []string{"entry_id", "author_name"}, func(v commentRecord, _ map[string]json.RawMessage) error {
		if v.AuthorUserID != nil && *v.AuthorUserID != account.ID {
			return importInvalid("external comment author must be a display snapshot", nil)
		}
		return nil
	}); err != nil {
		return document, personal, err
	}
	if _, err = validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], portableMediaFields, func(v mediaRecord, _ map[string]json.RawMessage) error {
		if len(v.Grants) > 0 {
			return importInvalid("personal archive cannot grant source account permissions", nil)
		}
		return nil
	}); err != nil {
		return document, personal, err
	}
	for _, file := range reader.File {
		if file.Name == "README.txt" || file.Name == "manifest.json" || file.Name == "checksums.sha256" || file.Name == "data/account.json" || file.Name == "data/identities.jsonl" {
			continue
		}
		input, err := file.Open()
		if err != nil {
			return document, personal, err
		}
		member, err := protocol.startMember(file.Name, file.Method)
		if err != nil {
			_ = input.Close()
			return document, personal, err
		}
		_, copyErr := io.CopyBuffer(member, contextReader{ctx: ctx, reader: input}, make([]byte, copyBufferSize))
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil {
			return document, personal, errors.Join(copyErr, closeErr)
		}
		if err = protocol.finishMember(member); err != nil {
			return document, personal, err
		}
	}
	c.Users = 1
	c.Identities = 0
	document = manifest{Format: formatName, FormatVersion: FormatVersion, CreatedAt: personal.CreatedAt, Source: sourceManifest{AppVersion: appVersion, InstanceID: personal.SourceInstanceID}, Counts: c}
	encoded, err := canonicalManifest(document, protocol.expandedBytes)
	if err != nil {
		return document, personal, err
	}
	if err = json.Unmarshal(encoded, &document); err != nil {
		return document, personal, err
	}
	if err = protocol.bytesMember("manifest.json", zip.Deflate, encoded); err != nil {
		return document, personal, err
	}
	if err = protocol.writeChecksums(); err != nil {
		return document, personal, err
	}
	err = writer.Close()
	closed = true
	return document, personal, err
}

func personalCounts(c sqlitestore.ArchiveCounts) ImportCounts {
	return ImportCounts{Entries: c.Entries, Comments: c.Comments, Media: c.Media, Users: c.Users, Identities: c.Identities, Articles: c.Articles, ArticleDrafts: c.ArticleDrafts}
}
