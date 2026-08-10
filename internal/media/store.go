package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/storage/sqlite"
)

// Store owns the media authority boundary for one active runtime generation.
type Store struct {
	runtime *runtimefs.Runtime
	db      sqlite.Transactor
	options Options
	workers chan struct{}
	flight  flightGroup
	// generateHook is test-only instrumentation used to prove singleflight.
	generateHook         func()
	beforeOriginalCommit func() error
	beforePreviewCommit  func() error
}

// NewStore binds media operations to the same Runtime that selected the
// current SQLite generation. The caller remains responsible for database life.
func NewStore(runtimeState *runtimefs.Runtime, database sqlite.Transactor, options Options) (*Store, error) {
	if runtimeState == nil || runtimeState.Locks == nil || runtimeState.Current.Root == "" {
		return nil, errors.New("media runtime is required")
	}
	if database == nil {
		return nil, errors.New("media database is required")
	}
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	return &Store{
		runtime: runtimeState,
		db:      database,
		options: normalized,
		workers: make(chan struct{}, normalized.Limits.PreviewWorkers),
	}, nil
}

// Ingest validates and fully renders a preview before publishing an immutable
// original and its row. Filename extensions and claimed MIME are never used.
func (store *Store) Ingest(ctx context.Context, originalName string, input io.Reader) (Media, Preview, error) {
	name, err := normalizeOriginalName(originalName)
	if err != nil {
		return Media{}, Preview{}, err
	}
	if input == nil {
		return Media{}, Preview{}, domain.NewError(domain.CodeInvalidArgument, "media input is required", map[string]any{"field": "file"})
	}
	contents, err := readBounded(ctx, input, store.options.Limits.MaxBytes)
	if err != nil {
		return Media{}, Preview{}, err
	}
	inspection, err := inspectImage(contents, store.options.Limits)
	if err != nil {
		return Media{}, Preview{}, err
	}
	digest := sha256.Sum256(contents)
	sha := hex.EncodeToString(digest[:])
	storagePath := canonicalStoragePath(sha, inspection.format)

	lease, err := store.runtime.Locks.AcquireSet(ctx,
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Shared},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		return Media{}, Preview{}, fmt.Errorf("coordinate media ingest: %w", err)
	}
	defer func() { _ = lease.Release() }()

	if existing, found, lookupErr := store.findBySHA(ctx, sha); lookupErr != nil {
		return Media{}, Preview{}, lookupErr
	} else if found {
		if err := store.verifyOriginal(ctx, existing, contents); err != nil {
			return Media{}, Preview{}, err
		}
		preview, err := store.ensurePreviewBytes(ctx, contents, inspection, sha)
		return existing, preview, err
	}

	preview, err := store.ensurePreviewBytes(ctx, contents, inspection, sha)
	if err != nil {
		return Media{}, Preview{}, err
	}
	createdOriginal, err := store.publishOriginal(ctx, storagePath, contents)
	if err != nil {
		return Media{}, Preview{}, err
	}
	cleanupOriginal := createdOriginal
	defer func() {
		if cleanupOriginal {
			_ = os.Remove(filepath.Join(store.runtime.Current.Root, filepath.FromSlash(storagePath)))
		}
	}()

	now := store.options.Clock().UTC()
	id, err := domain.NewID(now, store.options.Entropy)
	if err != nil {
		return Media{}, Preview{}, err
	}
	width, height := displayDimensions(inspection)
	media := Media{
		ID:                id,
		SHA256:            sha,
		StoragePath:       storagePath,
		OriginalName:      name,
		MIMEType:          inspection.format.mimeType(),
		ByteSize:          int64(len(contents)),
		Width:             width,
		Height:            height,
		CreatedAt:         now,
		UnreferencedSince: &now,
		AnimatedOriginal:  inspection.animated || inspection.format == formatGIF,
	}
	if err := store.insert(ctx, media); err != nil {
		// Another process may have won the content-hash race while both held a
		// platform that cannot share advisory locks. Recover deterministically.
		if existing, found, lookupErr := store.findBySHA(ctx, sha); lookupErr == nil && found {
			cleanupOriginal = false
			return existing, preview, nil
		}
		return Media{}, Preview{}, err
	}
	cleanupOriginal = false
	return media, preview, nil
}

// EnsurePreview returns a complete recipe artifact, regenerating a missing
// cache entry under a shared media lock. Concurrent callers share one build.
func (store *Store) EnsurePreview(ctx context.Context, id domain.ID) (Preview, error) {
	if _, err := domain.ParseID(id.String()); err != nil {
		return Preview{}, err
	}
	value, err := store.flight.Do(ctx, id.String(), func() (Preview, error) {
		lease, err := store.runtime.Locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
		if err != nil {
			return Preview{}, fmt.Errorf("coordinate preview generation: %w", err)
		}
		defer func() { _ = lease.Release() }()
		media, found, err := store.findByID(ctx, id)
		if err != nil {
			return Preview{}, err
		}
		if !found {
			return Preview{}, domain.NewError(domain.CodeMediaCorrupt, "media record does not exist", map[string]any{"media_id": id.String()})
		}
		if preview, ok := store.existingPreview(media.SHA256); ok {
			preview.AnimatedOriginal = media.MIMEType == formatGIF.mimeType()
			return preview, nil
		}
		contents, err := store.readOriginal(ctx, media)
		if err != nil {
			return Preview{}, err
		}
		inspection, err := inspectImage(contents, store.options.Limits)
		if err != nil {
			return Preview{}, domain.NewError(domain.CodeMediaCorrupt, "stored original is corrupt", map[string]any{"media_id": id.String()})
		}
		if inspection.format.mimeType() != media.MIMEType {
			return Preview{}, domain.NewError(domain.CodeMediaCorrupt, "stored original type does not match metadata", map[string]any{"media_id": id.String()})
		}
		return store.ensurePreviewBytes(ctx, contents, inspection, media.SHA256)
	})
	return value, err
}

func normalizeOriginalName(value string) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", domain.NewError(domain.CodeInvalidArgument, "original filename is invalid", map[string]any{"field": "filename"})
	}
	value = filepath.Base(strings.TrimSpace(value))
	if value == "" || value == "." || len([]byte(value)) > 255 {
		return "", domain.NewError(domain.CodeInvalidArgument, "original filename is required and must not exceed 255 bytes", map[string]any{"field": "filename"})
	}
	return value, nil
}

func readBounded(ctx context.Context, input io.Reader, maxBytes int64) ([]byte, error) {
	reader := &contextReader{ctx: ctx, reader: input}
	contents, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, domain.NewError(domain.CodeMediaCorrupt, "could not read image", nil)
	}
	if int64(len(contents)) > maxBytes {
		return nil, domain.NewError(domain.CodeMediaTooLarge, "image exceeds the configured byte budget", map[string]any{"max_bytes": maxBytes})
	}
	if len(contents) == 0 {
		return nil, corrupt("image is empty")
	}
	return contents, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func displayDimensions(inspection inspected) (uint32, uint32) {
	if inspection.orientation >= 5 && inspection.orientation <= 8 {
		return inspection.height, inspection.width
	}
	return inspection.width, inspection.height
}

func canonicalStoragePath(sha string, imageFormat format) string {
	return "media/original/" + sha[:2] + "/" + sha + "." + imageFormat.extension()
}

func expectedStoragePath(sha, mimeType string) (string, bool) {
	var imageFormat format
	switch mimeType {
	case "image/jpeg":
		imageFormat = formatJPEG
	case "image/png":
		imageFormat = formatPNG
	case "image/webp":
		imageFormat = formatWebP
	case "image/gif":
		imageFormat = formatGIF
	default:
		return "", false
	}
	if len(sha) != sha256.Size*2 || strings.IndexFunc(sha, func(value rune) bool {
		isDigit := value >= '0' && value <= '9'
		isLowerHex := value >= 'a' && value <= 'f'
		return !isDigit && !isLowerHex
	}) >= 0 {
		return "", false
	}
	return canonicalStoragePath(sha, imageFormat), true
}

func (store *Store) resolveOriginal(media Media) (string, error) {
	expected, ok := expectedStoragePath(media.SHA256, media.MIMEType)
	if !ok || media.StoragePath != expected {
		return "", domain.NewError(domain.CodeMediaCorrupt, "media storage path is not canonical", map[string]any{"media_id": media.ID.String()})
	}
	path := filepath.Join(store.runtime.Current.Root, filepath.FromSlash(media.StoragePath))
	relative, err := filepath.Rel(store.runtime.Current.Root, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", domain.NewError(domain.CodeMediaCorrupt, "media storage path escapes the current generation", map[string]any{"media_id": media.ID.String()})
	}
	return path, nil
}

func (store *Store) readOriginal(ctx context.Context, media Media) ([]byte, error) {
	path, err := store.resolveOriginal(media)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != media.ByteSize {
		return nil, domain.NewError(domain.CodeMediaCorrupt, "immutable original is missing or invalid", map[string]any{"media_id": media.ID.String()})
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, domain.NewError(domain.CodeMediaCorrupt, "immutable original cannot be opened", map[string]any{"media_id": media.ID.String()})
	}
	defer func() { _ = file.Close() }()
	contents, err := readBounded(ctx, file, store.options.Limits.MaxBytes)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, domain.NewError(domain.CodeMediaCorrupt, "immutable original cannot be read", map[string]any{"media_id": media.ID.String()})
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != media.SHA256 {
		return nil, domain.NewError(domain.CodeMediaCorrupt, "immutable original checksum does not match", map[string]any{"media_id": media.ID.String()})
	}
	return contents, nil
}

func (store *Store) verifyOriginal(ctx context.Context, media Media, expected []byte) error {
	contents, err := store.readOriginal(ctx, media)
	if err != nil {
		return err
	}
	if !bytes.Equal(contents, expected) {
		return domain.NewError(domain.CodeMediaCorrupt, "content hash collision or corrupted original", map[string]any{"media_id": media.ID.String()})
	}
	return nil
}

func (store *Store) publishOriginal(ctx context.Context, storagePath string, contents []byte) (bool, error) {
	final := filepath.Join(store.runtime.Current.Root, filepath.FromSlash(storagePath))
	directory := filepath.Dir(final)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false, fmt.Errorf("create original directory: %w", err)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("original directory is not a safe directory")
	}
	if info, err := os.Lstat(final); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("original destination is not a regular file")
		}
		existing, readErr := os.ReadFile(final)
		if readErr != nil || !bytes.Equal(existing, contents) {
			return false, domain.NewError(domain.CodeMediaCorrupt, "existing immutable original does not match its content hash", nil)
		}
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	partial, err := os.CreateTemp(directory, ".partial-*")
	if err != nil {
		return false, fmt.Errorf("create original partial: %w", err)
	}
	partialPath := partial.Name()
	removePartial := true
	defer func() {
		_ = partial.Close()
		if removePartial {
			_ = os.Remove(partialPath)
		}
	}()
	if err := partial.Chmod(0o600); err != nil {
		return false, err
	}
	if _, err := partial.Write(contents); err != nil {
		return false, fmt.Errorf("write original partial: %w", err)
	}
	if err := partial.Sync(); err != nil {
		return false, fmt.Errorf("sync original partial: %w", err)
	}
	if err := partial.Close(); err != nil {
		return false, fmt.Errorf("close original partial: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if store.beforeOriginalCommit != nil {
		if err := store.beforeOriginalCommit(); err != nil {
			return false, err
		}
	}
	if err := os.Rename(partialPath, final); err != nil {
		return false, fmt.Errorf("publish immutable original: %w", err)
	}
	removePartial = false
	if err := syncDirectory(directory); err != nil {
		_ = os.Remove(final)
		_ = syncDirectory(directory)
		return false, fmt.Errorf("sync original directory: %w", err)
	}
	return true, nil
}

func (store *Store) insert(ctx context.Context, media Media) error {
	return store.db.WriteTx(ctx, func(queries sqlite.Queries) error {
		_, err := queries.ExecContext(ctx, `
INSERT INTO media (
    id, sha256, storage_path, original_name, mime_type, byte_size,
    width, height, created_at_ms, unreferenced_since_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			media.ID.String(), media.SHA256, media.StoragePath, media.OriginalName,
			media.MIMEType, media.ByteSize, media.Width, media.Height,
			media.CreatedAt.UnixMilli(), optionalUnixMilli(media.UnreferencedSince))
		if err != nil {
			return fmt.Errorf("insert media metadata: %w", err)
		}
		return nil
	})
}

func (store *Store) findBySHA(ctx context.Context, sha string) (Media, bool, error) {
	var result Media
	found := true
	err := store.db.ReadTx(ctx, func(queries sqlite.Queries) error {
		row := queries.QueryRowContext(ctx, `
SELECT id, sha256, storage_path, original_name, mime_type, byte_size,
       width, height, created_at_ms, unreferenced_since_ms
FROM media WHERE sha256 = ?`, sha)
		var id string
		var width, height int64
		var createdAt int64
		var unreferenced sql.NullInt64
		if err := row.Scan(&id, &result.SHA256, &result.StoragePath, &result.OriginalName,
			&result.MIMEType, &result.ByteSize, &width, &height, &createdAt, &unreferenced); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				found = false
				return nil
			}
			return err
		}
		parsed, err := domain.ParseID(id)
		if err != nil {
			return err
		}
		result.ID = parsed
		result.Width, result.Height = uint32(width), uint32(height)
		result.CreatedAt = time.UnixMilli(createdAt).UTC()
		if unreferenced.Valid {
			value := time.UnixMilli(unreferenced.Int64).UTC()
			result.UnreferencedSince = &value
		}
		result.AnimatedOriginal = result.MIMEType == formatGIF.mimeType()
		return nil
	})
	return result, found, err
}

func optionalUnixMilli(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().UnixMilli()
}

func (store *Store) findByID(ctx context.Context, id domain.ID) (Media, bool, error) {
	var sha string
	err := store.db.ReadTx(ctx, func(queries sqlite.Queries) error {
		return queries.QueryRowContext(ctx, `SELECT sha256 FROM media WHERE id = ?`, id.String()).Scan(&sha)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, false, nil
	}
	if err != nil {
		return Media{}, false, err
	}
	return store.findBySHA(ctx, sha)
}
