// Package archive owns Moment's versioned portability protocol.
package archive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	// FormatVersion is the import-facing Moment export protocol version.
	FormatVersion           = 1
	formatName              = "moment-export"
	copyBufferSize          = 128 << 10
	manifestFixedPointLimit = 32
	// FormatHeaderValue is the fail-closed HTTP/CLI negotiation token for v1.
	FormatHeaderValue = "moment-export-v1"
)

// ErrDestinationUnavailable classifies a safe output target that cannot be
// used without weakening atomic publication or overwrite guarantees.
var ErrDestinationUnavailable = errors.New("archive destination is unavailable")

const readme = "Moment portable export\n\n" +
	"Format: moment-export v1\n" +
	"Portable records are under data/ and immutable originals are under media/.\n" +
	"checksums.sha256 uses: <sha256>\\t<uncompressed-bytes>\\t<path>.\n" +
	"It lists every ZIP member except checksums.sha256 itself.\n"

// Exporter produces one consistent archive from the selected runtime root.
type Exporter struct {
	dataDir    string
	appVersion string
	clock      func() time.Time
	// afterSnapshot is a package-test seam used to force a concurrent commit
	// immediately after SQLite establishes its WAL snapshot.
	afterSnapshot func()
	recordYield   func(string, int64)
}

// Options supplies deterministic, non-secret export metadata.
type Options struct {
	DataDir    string
	AppVersion string
	Clock      func() time.Time
}

// Summary is safe CLI output for both direct and API file exports.
type Summary struct {
	Path          string `json:"path,omitempty"`
	FormatVersion int    `json:"format_version"`
	ArchiveBytes  int64  `json:"archive_bytes"`
	ArchiveSHA256 string `json:"archive_sha256"`
}

// NewExporter constructs a stateless export use case. Each call independently
// resolves CURRENT while holding the required maintenance lease.
func NewExporter(options Options) (*Exporter, error) {
	if err := validateAppVersion(options.AppVersion); err != nil {
		return nil, err
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &Exporter{dataDir: options.DataDir, appVersion: options.AppVersion, clock: options.Clock}, nil
}

// Export streams one v1 ZIP. It holds maintenance shared from CURRENT
// resolution through completion, then media shared and one SQLite read snapshot
// until the last reachable immutable original has been copied.
func (exporter *Exporter) Export(ctx context.Context, destination io.Writer) (summary Summary, exportErr error) {
	if exporter == nil || destination == nil {
		return Summary{}, errors.New("archive exporter and destination are required")
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	runtimeState, maintenance, err := runtimefs.OpenForSnapshotContext(ctx, exporter.dataDir)
	if err != nil {
		return Summary{}, fmt.Errorf("open archive runtime snapshot: %w", err)
	}
	defer func() { exportErr = errors.Join(exportErr, maintenance.Release()) }()
	mediaLease, err := runtimeState.Locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
	if err != nil {
		return Summary{}, fmt.Errorf("acquire archive media lease: %w", err)
	}
	defer func() { exportErr = errors.Join(exportErr, mediaLease.Release()) }()
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return Summary{}, fmt.Errorf("open archive database: %w", err)
	}
	defer func() { exportErr = errors.Join(exportErr, database.Close()) }()
	return exporter.exportResolved(ctx, destination, runtimeState, database)
}

// ExportWithinLocks writes a complete v1 backup from an already-open active
// database while the caller holds maintenance and media exclusion. It avoids
// re-acquiring a shared maintenance lease during import commit.
func (exporter *Exporter) ExportWithinLocks(ctx context.Context, destination io.Writer, runtimeState *runtimefs.Runtime, database *sqlitestore.Database, held *runtimefs.LeaseSet) (Summary, error) {
	if exporter == nil || destination == nil || runtimeState == nil || database == nil || held == nil ||
		!held.BelongsTo(runtimeState.Root) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) || !held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return Summary{}, runtimefs.ErrInsufficientLocks
	}
	current, err := runtimeState.ResolveCurrent()
	if err != nil {
		return Summary{}, err
	}
	if current.ID != runtimeState.Current.ID || filepath.Clean(database.Path()) != filepath.Clean(current.DatabasePath) {
		return Summary{}, errors.New("backup database does not match authoritative CURRENT")
	}
	runtimeState.Current = current
	return exporter.exportResolved(ctx, destination, runtimeState, database)
}

func (exporter *Exporter) exportResolved(ctx context.Context, destination io.Writer, runtimeState *runtimefs.Runtime, database *sqlitestore.Database) (summary Summary, exportErr error) {
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		return Summary{}, err
	}
	defer func() { exportErr = errors.Join(exportErr, snapshot.Close()) }()
	if err := snapshot.ValidateIntegrity(ctx); err != nil {
		return Summary{}, archiveInvalid("archive relationships are inconsistent", err)
	}
	if exporter.afterSnapshot != nil {
		exporter.afterSnapshot()
	}

	createdAt := exporter.clock().UTC().Truncate(time.Millisecond)
	if createdAt.IsZero() {
		return Summary{}, errors.New("archive clock returned zero time")
	}
	if !isLowerHex(runtimeState.Instance.ID) || len(runtimeState.Instance.ID) != 32 {
		return Summary{}, archiveInvalid("archive instance identity is not canonical lowercase hexadecimal", nil)
	}
	archiveDigest := sha256.New()
	counted := &digestWriter{writer: contextWriter{ctx: ctx, writer: io.MultiWriter(destination, archiveDigest)}}
	zipWriter := zip.NewWriter(counted)
	protocol := &protocolWriter{zip: zipWriter, createdAt: createdAt}
	closed := false
	defer func() {
		if !closed {
			exportErr = errors.Join(exportErr, zipWriter.Close())
		}
	}()

	if err := protocol.bytesMember("README.txt", zip.Deflate, []byte(readme)); err != nil {
		return Summary{}, err
	}
	site, err := snapshot.Site(ctx)
	if err != nil {
		return Summary{}, archiveInvalid("site settings could not be exported", err)
	}
	if err := validateArchiveSite(site, runtimeState.IsDevelopment()); err != nil {
		return Summary{}, archiveInvalid("site settings are not portable", err)
	}
	if err := protocol.jsonMember("data/site.json", siteRecord{
		Title: site.Title, Bio: site.Bio, Timezone: site.Timezone,
		PublicBaseURL: site.PublicBaseURL, AvatarMediaID: site.AvatarMediaID,
		CommentsEnabled: site.CommentsEnabled,
	}); err != nil {
		return Summary{}, err
	}

	entryCount, err := protocol.jsonLines("data/entries.jsonl", func(write func(any) error) error {
		return snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
			if err := validateArchiveEntry(value); err != nil {
				return err
			}
			media := make([]usageRecord, len(value.Media))
			for index, usage := range value.Media {
				media[index] = usageRecord{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
			}
			if exporter.recordYield != nil {
				exporter.recordYield("entry", 1)
			}
			return write(entryRecord{
				ID: value.ID, Kind: value.Kind, ContentMD: value.ContentMD,
				CreatedAt: portableTime(value.CreatedAtMS), UpdatedAt: portableTime(value.UpdatedAtMS),
				PublishedAt: portableTime(value.PublishedAtMS), Media: media,
			})
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("entries could not be exported", err)
	}
	commentCount, err := protocol.jsonLines("data/comments.jsonl", func(write func(any) error) error {
		return snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
			if err := validateArchiveComment(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("comment", 1)
			}
			return write(commentRecord{
				ID: value.ID, EntryID: value.EntryID, ParentID: value.ParentID,
				AuthorKind: value.AuthorKind, AuthorName: value.AuthorName,
				ContentMD: value.ContentMD, CreatedAt: portableTime(value.CreatedAtMS),
			})
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("comments could not be exported", err)
	}
	mediaCount, err := protocol.jsonLines("data/media.jsonl", func(write func(any) error) error {
		return snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
			if err := validateArchiveMedia(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("media", 1)
			}
			archivePath, err := mediaArchivePath(value)
			if err != nil {
				return err
			}
			return write(mediaRecord{
				ID: value.ID, SHA256: value.SHA256, ArchivePath: archivePath,
				OriginalName: value.OriginalName, MIMEType: value.MIMEType,
				ByteSize: value.ByteSize, Width: value.Width, Height: value.Height,
				CreatedAt: portableTime(value.CreatedAtMS),
			})
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("media metadata could not be exported", err)
	}

	buffer := make([]byte, copyBufferSize)
	blobCount := int64(0)
	err = snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateArchiveMedia(value); err != nil {
			return err
		}
		archivePath, err := mediaArchivePath(value)
		if err != nil {
			return err
		}
		source, err := openOriginal(runtimeState.Current.Root, value.StoragePath)
		if err != nil {
			return archiveInvalid("a reachable original is missing or unsafe", err)
		}
		defer func() { _ = source.Close() }()
		mediaID, err := domain.ParseID(value.ID)
		if err != nil {
			return archiveInvalid("reachable media metadata is invalid", err)
		}
		if err := media.VerifyPortableOriginal(ctx, source, media.Media{
			ID: mediaID, SHA256: value.SHA256, StoragePath: value.StoragePath,
			OriginalName: value.OriginalName, MIMEType: value.MIMEType,
			ByteSize: value.ByteSize, Width: value.Width, Height: value.Height,
			CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(),
		}); err != nil {
			return archiveInvalid("a reachable original does not match its metadata", err)
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return archiveInvalid("a reachable original could not be rewound", err)
		}
		member, err := protocol.startMember(archivePath, zip.Store)
		if err != nil {
			return err
		}
		copied, err := io.CopyBuffer(member, contextReader{ctx: ctx, reader: source}, buffer)
		closeErr := source.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if copied != value.ByteSize || member.size != value.ByteSize {
			return archiveInvalid("a reachable original size does not match its metadata", nil)
		}
		actual := hex.EncodeToString(member.digest.Sum(nil))
		if actual != value.SHA256 {
			return domain.NewError(domain.CodeExportChecksumMismatch, "a reachable original failed SHA-256 verification", map[string]any{"media_id": value.ID})
		}
		if err := protocol.finishMember(member); err != nil {
			return err
		}
		blobCount++
		return nil
	})
	if err != nil {
		return Summary{}, err
	}

	counts := snapshot.Counts()
	if entryCount != counts.Entries || commentCount != counts.Comments || mediaCount != counts.Media || blobCount != counts.Media {
		return Summary{}, archiveInvalid("archive record counts changed inside one snapshot", nil)
	}
	manifestBytes, err := canonicalManifest(manifest{
		Format: formatName, FormatVersion: FormatVersion, CreatedAt: createdAt.Format(time.RFC3339Nano),
		Source: sourceManifest{AppVersion: exporter.appVersion, InstanceID: runtimeState.Instance.ID},
		Counts: countManifest{Entries: counts.Entries, Comments: counts.Comments, Media: counts.Media},
	}, protocol.expandedBytes)
	if err != nil {
		return Summary{}, err
	}
	if err := protocol.bytesMember("manifest.json", zip.Deflate, manifestBytes); err != nil {
		return Summary{}, err
	}
	if err := protocol.writeChecksums(); err != nil {
		return Summary{}, err
	}
	if err := zipWriter.Close(); err != nil {
		return Summary{}, fmt.Errorf("close archive ZIP: %w", err)
	}
	closed = true
	return Summary{
		FormatVersion: FormatVersion,
		ArchiveBytes:  counted.size,
		ArchiveSHA256: hex.EncodeToString(archiveDigest.Sum(nil)),
	}, nil
}

// ExportFile writes beside the destination, flushes and validates the ZIP
// central directory, then atomically publishes with explicit overwrite policy.
func (exporter *Exporter) ExportFile(ctx context.Context, destination string, overwrite bool) (Summary, error) {
	return WriteFile(ctx, destination, overwrite, exporter.Export)
}

// WriteFile applies the archive file-publication contract to a streaming
// producer. It is shared by direct exports and API downloads.
func WriteFile(ctx context.Context, destination string, overwrite bool, producer func(context.Context, io.Writer) (Summary, error)) (summary Summary, resultErr error) {
	if producer == nil {
		return Summary{}, errors.New("archive producer is required")
	}
	finalPath, err := validateDestination(destination, overwrite)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: %w", ErrDestinationUnavailable, err)
	}
	partial, err := createPartial(finalPath)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: create prepared output: %w", ErrDestinationUnavailable, err)
	}
	partialPath := partial.Name()
	published := false
	defer func() {
		if !published {
			if removeErr := os.Remove(partialPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("%w: clean prepared archive: %w", ErrDestinationUnavailable, removeErr))
			}
		}
	}()
	summary, err = producer(ctx, preparedOutputWriter{writer: partial})
	if err != nil {
		_ = partial.Close()
		return Summary{}, err
	}
	if err := partial.Sync(); err != nil {
		_ = partial.Close()
		return Summary{}, fmt.Errorf("%w: sync prepared archive: %w", ErrDestinationUnavailable, err)
	}
	if err := partial.Close(); err != nil {
		return Summary{}, fmt.Errorf("%w: close prepared archive: %w", ErrDestinationUnavailable, err)
	}
	if err := ValidateFileContext(ctx, partialPath); err != nil {
		return Summary{}, err
	}
	if overwrite {
		err = runtimefs.PublishPreparedFile(partialPath, finalPath)
	} else {
		err = runtimefs.PublishPreparedFileNoReplace(partialPath, finalPath)
	}
	if err != nil {
		return Summary{}, fmt.Errorf("%w: publish prepared archive: %w", ErrDestinationUnavailable, err)
	}
	published = true
	summary.Path = finalPath
	return summary, nil
}

type preparedOutputWriter struct{ writer io.Writer }

func (writer preparedOutputWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return written, fmt.Errorf("%w: write prepared archive: %w", ErrDestinationUnavailable, err)
	}
	return written, nil
}

type manifest struct {
	Format        string         `json:"format"`
	FormatVersion int            `json:"format_version"`
	CreatedAt     string         `json:"created_at"`
	Source        sourceManifest `json:"source"`
	Counts        countManifest  `json:"counts"`
	Limits        limitManifest  `json:"limits"`
}

type sourceManifest struct {
	AppVersion string `json:"app_version"`
	InstanceID string `json:"instance_id"`
}

type countManifest struct {
	Entries  int64 `json:"entries"`
	Comments int64 `json:"comments"`
	Media    int64 `json:"media"`
}

type limitManifest struct {
	ExpandedBytes int64 `json:"expanded_bytes"`
}

type siteRecord struct {
	Title           string  `json:"title"`
	Bio             string  `json:"bio"`
	Timezone        string  `json:"timezone"`
	PublicBaseURL   *string `json:"public_base_url"`
	AvatarMediaID   *string `json:"avatar_media_id"`
	CommentsEnabled bool    `json:"comments_enabled"`
}

type usageRecord struct {
	MediaID  string  `json:"media_id"`
	Position int     `json:"position"`
	AltText  *string `json:"alt_text"`
}

type entryRecord struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	ContentMD   string        `json:"content_md"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
	PublishedAt string        `json:"published_at"`
	Media       []usageRecord `json:"media"`
}

type commentRecord struct {
	ID         string  `json:"id"`
	EntryID    string  `json:"entry_id"`
	ParentID   *string `json:"parent_id"`
	AuthorKind string  `json:"author_kind"`
	AuthorName string  `json:"author_name"`
	ContentMD  string  `json:"content_md"`
	CreatedAt  string  `json:"created_at"`
}

type mediaRecord struct {
	ID           string `json:"id"`
	SHA256       string `json:"sha256"`
	ArchivePath  string `json:"archive_path"`
	OriginalName string `json:"original_name"`
	MIMEType     string `json:"mime_type"`
	ByteSize     int64  `json:"byte_size"`
	Width        uint32 `json:"width"`
	Height       uint32 `json:"height"`
	CreatedAt    string `json:"created_at"`
}

type protocolWriter struct {
	zip           *zip.Writer
	createdAt     time.Time
	members       []memberDigest
	paths         map[string]struct{}
	expandedBytes int64
}

type memberDigest struct {
	path, sha256 string
	size         int64
}

type memberWriter struct {
	writer io.Writer
	digest hash.Hash
	size   int64
	path   string
}

func (writer *memberWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	if written > 0 {
		_, _ = writer.digest.Write(data[:written])
		writer.size += int64(written)
	}
	return written, err
}

func (protocol *protocolWriter) startMember(name string, method uint16) (*memberWriter, error) {
	if protocol.paths == nil {
		protocol.paths = make(map[string]struct{})
	}
	if err := validateMemberName(name); err != nil {
		return nil, err
	}
	if _, exists := protocol.paths[name]; exists {
		return nil, fmt.Errorf("duplicate archive member %q", name)
	}
	header := &zip.FileHeader{Name: name, Method: method, Modified: protocol.createdAt}
	header.SetMode(0o644)
	destination, err := protocol.zip.CreateHeader(header)
	if err != nil {
		return nil, fmt.Errorf("create archive member %s: %w", name, err)
	}
	protocol.paths[name] = struct{}{}
	return &memberWriter{writer: destination, digest: sha256.New(), path: name}, nil
}

func (protocol *protocolWriter) finishMember(member *memberWriter) error {
	if member == nil {
		return errors.New("archive member is required")
	}
	protocol.members = append(protocol.members, memberDigest{path: member.path, sha256: hex.EncodeToString(member.digest.Sum(nil)), size: member.size})
	protocol.expandedBytes += member.size
	return nil
}

func (protocol *protocolWriter) bytesMember(name string, method uint16, data []byte) error {
	member, err := protocol.startMember(name, method)
	if err != nil {
		return err
	}
	if _, err := member.Write(data); err != nil {
		return fmt.Errorf("write archive member %s: %w", name, err)
	}
	return protocol.finishMember(member)
}

func (protocol *protocolWriter) jsonMember(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode archive member %s: %w", name, err)
	}
	data = append(data, '\n')
	return protocol.bytesMember(name, zip.Deflate, data)
}

func (protocol *protocolWriter) jsonLines(name string, stream func(func(any) error) error) (int64, error) {
	member, err := protocol.startMember(name, zip.Deflate)
	if err != nil {
		return 0, err
	}
	var count int64
	err = stream(func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := member.Write(data); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, protocol.finishMember(member)
}

func (protocol *protocolWriter) writeChecksums() error {
	members := append([]memberDigest(nil), protocol.members...)
	sort.Slice(members, func(i, j int) bool { return members[i].path < members[j].path })
	var body strings.Builder
	for _, member := range members {
		body.WriteString(member.sha256)
		body.WriteByte('\t')
		body.WriteString(strconv.FormatInt(member.size, 10))
		body.WriteByte('\t')
		body.WriteString(member.path)
		body.WriteByte('\n')
	}
	member, err := protocol.startMember("checksums.sha256", zip.Deflate)
	if err != nil {
		return err
	}
	_, err = io.WriteString(member, body.String())
	// checksums intentionally excludes itself from protocol.members and
	// expanded_bytes, so future import has no checksum self-reference.
	return err
}

func canonicalManifest(value manifest, otherBytes int64) ([]byte, error) {
	return fixedPointBytes(otherBytes, func(expanded int64) ([]byte, error) {
		value.Limits.ExpandedBytes = expanded
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode archive manifest: %w", err)
		}
		data = append(data, '\n')
		return data, nil
	})
}

func fixedPointBytes(otherBytes int64, render func(int64) ([]byte, error)) ([]byte, error) {
	if otherBytes < 0 || render == nil {
		return nil, errors.New("fixed-point byte inputs are invalid")
	}
	expanded := otherBytes
	for attempt := 0; attempt < manifestFixedPointLimit; attempt++ {
		data, err := render(expanded)
		if err != nil {
			return nil, err
		}
		next := otherBytes + int64(len(data))
		if next == expanded {
			return data, nil
		}
		expanded = next
	}
	return nil, errors.New("archive manifest expanded_bytes did not reach a fixed point")
}

func validateArchiveEntry(value sqlitestore.ArchiveEntry) error {
	id, err := domain.ParseID(value.ID)
	if err != nil {
		return err
	}
	entry := domain.Entry{
		ID: id, Kind: domain.EntryKind(value.Kind), ContentMD: value.ContentMD,
		CreatedAt:   time.UnixMilli(value.CreatedAtMS).UTC(),
		UpdatedAt:   time.UnixMilli(value.UpdatedAtMS).UTC(),
		PublishedAt: time.UnixMilli(value.PublishedAtMS).UTC(),
	}
	if value.CreatedAtMS <= 0 || value.UpdatedAtMS <= 0 || value.PublishedAtMS <= 0 {
		return errors.New("entry timestamps must be positive")
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	usages := make([]domain.EntryMediaUsage, len(value.Media))
	for index, usage := range value.Media {
		mediaID, err := domain.ParseID(usage.MediaID)
		if err != nil {
			return err
		}
		usages[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
	}
	return domain.ValidateEntryMediaUsages(usages)
}

func validateArchiveComment(value sqlitestore.ArchiveComment) error {
	id, err := domain.ParseID(value.ID)
	if err != nil {
		return err
	}
	entryID, err := domain.ParseID(value.EntryID)
	if err != nil {
		return err
	}
	var parentID *domain.ID
	if value.ParentID != nil {
		parsed, err := domain.ParseID(*value.ParentID)
		if err != nil {
			return err
		}
		parentID = &parsed
	}
	if value.CreatedAtMS <= 0 {
		return errors.New("comment timestamp must be positive")
	}
	return (domain.Comment{
		ID: id, EntryID: entryID, ParentID: parentID,
		AuthorKind: domain.CommentAuthorKind(value.AuthorKind), AuthorName: value.AuthorName,
		ContentMD: value.ContentMD, CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(),
	}).Validate()
}

func validateArchiveSite(value sqlitestore.ArchiveSite, development bool) error {
	for _, field := range []string{value.Title, value.Bio, value.Timezone} {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return errors.New("site text is not canonical UTF-8")
		}
	}
	if strings.TrimSpace(value.Title) == "" {
		return errors.New("site title is empty")
	}
	if value.Timezone == "" || value.Timezone == "Local" || strings.HasPrefix(value.Timezone, "/") || strings.Contains(value.Timezone, "..") {
		return errors.New("site timezone is not an IANA location")
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return errors.New("site timezone is not an IANA location")
	}
	if value.AvatarMediaID != nil {
		if _, err := domain.ParseID(*value.AvatarMediaID); err != nil {
			return err
		}
	}
	if value.PublicBaseURL != nil {
		normalized, err := app.NormalizePublicBaseURL(*value.PublicBaseURL, development)
		if err != nil || normalized != *value.PublicBaseURL {
			return errors.New("site public base URL is not canonical HTTP(S)")
		}
	}
	return nil
}

func validateArchiveMedia(value sqlitestore.ArchiveMedia) error {
	if _, err := domain.ParseID(value.ID); err != nil {
		return err
	}
	if !utf8.ValidString(value.OriginalName) || value.OriginalName == "" || strings.ContainsRune(value.OriginalName, 0) {
		return errors.New("media original name is not canonical UTF-8")
	}
	if value.ByteSize <= 0 || value.ByteSize > media.DefaultMaxBytes || value.Width == 0 || value.Height == 0 || value.CreatedAtMS <= 0 ||
		value.Width > media.DefaultMaxDimension || value.Height > media.DefaultMaxDimension || uint64(value.Width)*uint64(value.Height) > media.DefaultMaxPixels {
		return errors.New("media dimensions, size, and creation time must be positive")
	}
	_, err := mediaArchivePath(value)
	return err
}

func mediaArchivePath(value sqlitestore.ArchiveMedia) (string, error) {
	if len(value.SHA256) != sha256.Size*2 {
		return "", errors.New("media SHA-256 is malformed")
	}
	decoded, err := hex.DecodeString(value.SHA256)
	if err != nil || hex.EncodeToString(decoded) != value.SHA256 {
		return "", errors.New("media SHA-256 is not canonical lowercase hex")
	}
	extension := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}[value.MIMEType]
	if extension == "" {
		return "", errors.New("media MIME type is not portable")
	}
	return "media/" + value.SHA256 + "." + extension, nil
}

func openOriginal(generationRoot, storagePath string) (*os.File, error) {
	if storagePath == "" || path.IsAbs(storagePath) || strings.Contains(storagePath, "\\") || path.Clean(storagePath) != storagePath || strings.HasPrefix(storagePath, "../") {
		return nil, errors.New("media storage path is unsafe")
	}
	root, err := filepath.Abs(generationRoot)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(root, filepath.FromSlash(storagePath))
	relative, err := filepath.Rel(root, name)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, errors.New("media path escapes active generation")
	}
	before, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("media original is not a regular non-symlink file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("media original changed while opening")
	}
	return file, nil
}

func portableTime(milliseconds int64) string {
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339Nano)
}

func archiveInvalid(message string, cause error) error {
	return errors.Join(domain.NewError(domain.CodeExportInvalid, message, nil), cause)
}

func validateMemberName(name string) error {
	if name == "" || !utf8.ValidString(name) || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return fmt.Errorf("unsafe archive member name %q", name)
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("unsafe archive member name %q", name)
		}
	}
	for _, character := range name {
		if character < 0x20 || character > 0x7e {
			return fmt.Errorf("archive member name is not controlled ASCII: %q", name)
		}
	}
	return nil
}

func validateDestination(destination string, overwrite bool) (string, error) {
	if strings.TrimSpace(destination) == "" || strings.ContainsRune(destination, 0) {
		return "", errors.New("archive output path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(destination))
	if err != nil {
		return "", err
	}
	if filepath.Base(absolute) == "." || filepath.Base(absolute) == string(os.PathSeparator) {
		return "", fmt.Errorf("%w: output must name a file", ErrDestinationUnavailable)
	}
	parent, err := os.Stat(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	if !parent.IsDir() {
		return "", fmt.Errorf("%w: output parent is not a directory", ErrDestinationUnavailable)
	}
	info, err := os.Lstat(absolute)
	if err == nil {
		if !overwrite {
			return "", fmt.Errorf("archive output already exists: %w", fs.ErrExist)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: --overwrite requires an existing regular non-symlink file", ErrDestinationUnavailable)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return absolute, nil
}

func createPartial(destination string) (*os.File, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	name := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".partial."+hex.EncodeToString(random[:]))
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// ValidateFile independently validates one complete v1 archive using only its
// ZIP members. It performs bounded-memory checksum verification and therefore
// doubles as the future import protocol's fail-closed inspection primitive.
func ValidateFile(name string) error {
	return ValidateFileContext(context.Background(), name)
}

// ValidateFileContext is ValidateFile with cancellation for command and import
// lifecycles that should not continue reading after their caller stops.
func ValidateFileContext(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, err := zip.OpenReader(name)
	if err != nil {
		return archiveInvalid("prepared ZIP central directory is invalid", err)
	}
	defer func() { _ = reader.Close() }()
	if len(reader.File) < 7 {
		return archiveInvalid("prepared ZIP is missing required members", nil)
	}
	seen := make(map[string]struct{}, len(reader.File))
	files := make(map[string]*zip.File, len(reader.File))
	for index, file := range reader.File {
		if err := validateMemberName(file.Name); err != nil {
			return archiveInvalid("prepared ZIP contains an unsafe member name", err)
		}
		if _, exists := seen[file.Name]; exists {
			return archiveInvalid("prepared ZIP contains a duplicate member", nil)
		}
		seen[file.Name] = struct{}{}
		files[file.Name] = file
		if file.FileInfo().IsDir() || file.Mode()&os.ModeSymlink != 0 {
			return archiveInvalid("prepared ZIP contains a non-regular member", nil)
		}
		if index < 5 {
			expected := []string{"README.txt", "data/site.json", "data/entries.jsonl", "data/comments.jsonl", "data/media.jsonl"}[index]
			if file.Name != expected {
				return archiveInvalid("prepared ZIP member order is not canonical", nil)
			}
		}
	}
	for _, required := range []string{"README.txt", "data/site.json", "data/entries.jsonl", "data/comments.jsonl", "data/media.jsonl", "manifest.json", "checksums.sha256"} {
		if _, exists := seen[required]; !exists {
			return archiveInvalid("prepared ZIP is missing a required member", nil)
		}
	}
	if reader.File[len(reader.File)-2].Name != "manifest.json" || reader.File[len(reader.File)-1].Name != "checksums.sha256" {
		return archiveInvalid("prepared ZIP trailer order is not canonical", nil)
	}
	lastMedia := ""
	for _, file := range reader.File[5 : len(reader.File)-2] {
		if !strings.HasPrefix(file.Name, "media/") || file.Name <= lastMedia {
			return archiveInvalid("prepared ZIP media member order is not canonical", nil)
		}
		lastMedia = file.Name
	}
	readmeData, err := readSmallZIPMember(ctx, files["README.txt"], 64<<10)
	if err != nil || !bytes.Equal(readmeData, []byte(readme)) {
		return archiveInvalid("prepared ZIP README is not the fixed v1 payload", err)
	}

	manifestData, err := readSmallZIPMember(ctx, files["manifest.json"], 64<<10)
	if err != nil {
		return archiveInvalid("prepared ZIP manifest is unreadable", err)
	}
	var value manifest
	manifestFields, err := decodePortableJSONObject(manifestData, &value, []string{"format", "format_version", "created_at", "source", "counts", "limits"})
	if err != nil {
		return archiveInvalid("prepared ZIP manifest is invalid", err)
	}
	if err := requireNonNull(manifestFields, "format", "format_version", "created_at", "source", "counts", "limits"); err != nil {
		return archiveInvalid("prepared ZIP manifest is invalid", err)
	}
	if err := requireObjectFields(manifestFields["source"], []string{"app_version", "instance_id"}, []string{"app_version", "instance_id"}); err != nil {
		return archiveInvalid("prepared ZIP manifest source is invalid", err)
	}
	if err := requireObjectFields(manifestFields["counts"], []string{"entries", "comments", "media"}, []string{"entries", "comments", "media"}); err != nil {
		return archiveInvalid("prepared ZIP manifest counts are invalid", err)
	}
	if err := requireObjectFields(manifestFields["limits"], []string{"expanded_bytes"}, []string{"expanded_bytes"}); err != nil {
		return archiveInvalid("prepared ZIP manifest limits are invalid", err)
	}
	if value.Format != formatName || value.FormatVersion != FormatVersion || validateAppVersion(value.Source.AppVersion) != nil ||
		len(value.Source.InstanceID) != 32 || !isLowerHex(value.Source.InstanceID) || value.Counts.Entries < 0 || value.Counts.Comments < 0 || value.Counts.Media < 0 || value.Limits.ExpandedBytes < 1 {
		return archiveInvalid("prepared ZIP manifest contract is invalid", nil)
	}
	_, err = parsePortableTimestamp(value.CreatedAt)
	if err != nil {
		return archiveInvalid("prepared ZIP manifest timestamp is not canonical UTC", err)
	}
	portableMedia := make(map[string]media.Media, len(reader.File)-7)
	if err := validatePortableRecords(ctx, files, reader.File[5:len(reader.File)-2], value, portableMedia); err != nil {
		return archiveInvalid("prepared ZIP portable records are invalid", err)
	}

	checksums, err := readChecksums(ctx, files["checksums.sha256"], len(reader.File)-1)
	if err != nil {
		return archiveInvalid("prepared ZIP checksums are invalid", err)
	}
	if len(checksums) != len(reader.File)-1 {
		return archiveInvalid("prepared ZIP checksums do not cover every payload member", nil)
	}
	buffer := make([]byte, copyBufferSize)
	var expanded int64
	for _, file := range reader.File[:len(reader.File)-1] {
		expected, ok := checksums[file.Name]
		if !ok {
			return archiveInvalid("prepared ZIP checksums omit a payload member", nil)
		}
		if expected.size == math.MaxInt64 || uint64(expected.size) != file.UncompressedSize64 {
			return domain.NewError(domain.CodeExportChecksumMismatch, "a prepared archive member size does not match its ZIP header", map[string]any{"member": file.Name})
		}
		source, err := file.Open()
		if err != nil {
			return archiveInvalid("prepared ZIP payload member is unreadable", err)
		}
		digest := sha256.New()
		copyDestination := io.Writer(digest)
		var mediaContents bytes.Buffer
		mediaMetadata, isMedia := portableMedia[file.Name]
		if isMedia {
			if file.UncompressedSize64 > uint64(media.DefaultMaxBytes) {
				_ = source.Close()
				return archiveInvalid("prepared ZIP media member exceeds the byte budget", nil)
			}
			copyDestination = io.MultiWriter(digest, &mediaContents)
		}
		copySource := io.Reader(io.LimitReader(contextReader{ctx: ctx, reader: source}, expected.size+1))
		size, copyErr := io.CopyBuffer(copyDestination, copySource, buffer)
		closeErr := source.Close()
		if copyErr != nil || closeErr != nil {
			return archiveInvalid("prepared ZIP payload verification failed", errors.Join(copyErr, closeErr))
		}
		if size != expected.size || hex.EncodeToString(digest.Sum(nil)) != expected.sha256 || uint64(size) != file.UncompressedSize64 {
			return domain.NewError(domain.CodeExportChecksumMismatch, "a prepared archive member failed checksum verification", map[string]any{"member": file.Name})
		}
		if isMedia {
			if err := media.VerifyPortableArchiveOriginal(ctx, bytes.NewReader(mediaContents.Bytes()), mediaMetadata); err != nil {
				return archiveInvalid("prepared ZIP media member does not match portable metadata", err)
			}
		}
		if size > int64(^uint64(0)>>1)-expanded {
			return archiveInvalid("prepared ZIP expanded byte count overflows", nil)
		}
		expanded += size
	}
	if expanded != value.Limits.ExpandedBytes {
		return archiveInvalid("prepared ZIP expanded byte count does not match manifest", nil)
	}
	return nil
}

type expectedDigest struct {
	sha256 string
	size   int64
}

func readSmallZIPMember(ctx context.Context, file *zip.File, limit int64) ([]byte, error) {
	if file == nil || file.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("ZIP member exceeds its inspection budget")
	}
	source, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = source.Close() }()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: source}, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.Join(err, errors.New("ZIP member exceeds its inspection budget"))
	}
	return data, nil
}

func readChecksums(ctx context.Context, file *zip.File, maxEntries int) (map[string]expectedDigest, error) {
	if file == nil {
		return nil, errors.New("checksums member is missing")
	}
	if maxEntries < 1 {
		return nil, errors.New("checksum entry budget is invalid")
	}
	source, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = source.Close() }()
	result := make(map[string]expectedDigest)
	tracked := &lastByteReader{reader: contextReader{ctx: ctx, reader: source}}
	scanner := bufio.NewScanner(tracked)
	scanner.Buffer(make([]byte, 1024), 4096)
	previous := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(result) >= maxEntries {
			return nil, errors.New("checksums member has more entries than ZIP payload members")
		}
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 3 || len(parts[0]) != sha256.Size*2 || !isLowerHex(parts[0]) || parts[2] == "checksums.sha256" || validateMemberName(parts[2]) != nil || parts[2] <= previous {
			return nil, errors.New("checksum line is not canonical")
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 || strconv.FormatInt(size, 10) != parts[1] {
			return nil, errors.New("checksum size is not canonical")
		}
		if _, exists := result[parts[2]]; exists {
			return nil, errors.New("checksum path is duplicated")
		}
		result[parts[2]] = expectedDigest{sha256: parts[0], size: size}
		previous = parts[2]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if tracked.total == 0 || tracked.last != '\n' {
		return nil, errors.New("checksums member must end with LF")
	}
	return result, nil
}

const maxPortableRecordBytes = 50 << 20

func validatePortableRecords(ctx context.Context, files map[string]*zip.File, mediaMembers []*zip.File, document manifest, portableMedia map[string]media.Media) error {
	siteData, err := readSmallZIPMember(ctx, files["data/site.json"], maxPortableRecordBytes)
	if err != nil {
		return err
	}
	var site siteRecord
	siteFields, err := decodePortableJSONObject(siteData, &site, []string{"title", "bio", "timezone", "public_base_url", "avatar_media_id", "comments_enabled"})
	if err != nil {
		return err
	}
	if err := requireNonNull(siteFields, "title", "bio", "timezone", "comments_enabled"); err != nil {
		return err
	}
	if err := validateArchiveSite(sqlitestore.ArchiveSite{
		Title: site.Title, Bio: site.Bio, Timezone: site.Timezone,
		PublicBaseURL: site.PublicBaseURL, AvatarMediaID: site.AvatarMediaID,
		CommentsEnabled: site.CommentsEnabled,
	}, true); err != nil {
		return err
	}

	entryIDs := make(map[string]struct{})
	usageMediaIDs := make(map[string]struct{})
	lastEntryID := ""
	entries, err := validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], []string{"id", "kind", "content_md", "created_at", "updated_at", "published_at", "media"}, func(value entryRecord, fields map[string]json.RawMessage) error {
		if int64(len(entryIDs)) >= document.Counts.Entries {
			return errors.New("entry records exceed the manifest count")
		}
		if err := requireNonNull(fields, "id", "kind", "content_md", "created_at", "updated_at", "published_at", "media"); err != nil {
			return err
		}
		if value.Media == nil {
			return errors.New("entry media must be an array, not null")
		}
		var rawUsages []json.RawMessage
		if err := json.Unmarshal(fields["media"], &rawUsages); err != nil || rawUsages == nil || len(rawUsages) != len(value.Media) {
			return errors.New("entry media must be a complete array")
		}
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
			if err := requireObjectFields(rawUsages[index], []string{"media_id", "position", "alt_text"}, []string{"media_id", "position"}); err != nil {
				return err
			}
			usages[index] = sqlitestore.ArchiveUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
			usageMediaIDs[usage.MediaID] = struct{}{}
		}
		if value.ID <= lastEntryID {
			return errors.New("entry records are not in strict ID order")
		}
		if _, exists := entryIDs[value.ID]; exists {
			return errors.New("entry ID is duplicated")
		}
		if err := validateArchiveEntry(sqlitestore.ArchiveEntry{
			ID: value.ID, Kind: value.Kind, ContentMD: value.ContentMD,
			CreatedAtMS: created.UnixMilli(), UpdatedAtMS: updated.UnixMilli(), PublishedAtMS: published.UnixMilli(), Media: usages,
		}); err != nil {
			return err
		}
		entryIDs[value.ID] = struct{}{}
		lastEntryID = value.ID
		return nil
	})
	if err != nil {
		return err
	}
	type commentReference struct {
		entryID string
		parent  *string
	}
	commentIDs := make(map[string]commentReference)
	var previousComment *commentOrder
	comments, err := validateJSONLines[commentRecord](ctx, files["data/comments.jsonl"], []string{"id", "entry_id", "parent_id", "author_kind", "author_name", "content_md", "created_at"}, func(value commentRecord, fields map[string]json.RawMessage) error {
		if int64(len(commentIDs)) >= document.Counts.Comments {
			return errors.New("comment records exceed the manifest count")
		}
		if err := requireNonNull(fields, "id", "entry_id", "author_kind", "author_name", "content_md", "created_at"); err != nil {
			return err
		}
		created, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return err
		}
		if _, exists := entryIDs[value.EntryID]; !exists {
			return errors.New("comment refers to an entry absent from the archive")
		}
		if _, exists := commentIDs[value.ID]; exists {
			return errors.New("comment ID is duplicated")
		}
		group := value.ID
		rank := 0
		if value.ParentID != nil {
			group = *value.ParentID
			rank = 1
			parent, exists := commentIDs[*value.ParentID]
			if !exists || parent.parent != nil || parent.entryID != value.EntryID {
				return errors.New("comment reply parent is absent, not a root, or belongs to another entry")
			}
		}
		order := commentOrder{entryID: value.EntryID, group: group, rank: rank, createdAtMS: created.UnixMilli(), id: value.ID}
		if previousComment != nil && compareCommentOrder(*previousComment, order) >= 0 {
			return errors.New("comment records are not in canonical thread order")
		}
		if err := validateArchiveComment(sqlitestore.ArchiveComment{
			ID: value.ID, EntryID: value.EntryID, ParentID: value.ParentID,
			AuthorKind: value.AuthorKind, AuthorName: value.AuthorName,
			ContentMD: value.ContentMD, CreatedAtMS: created.UnixMilli(),
		}); err != nil {
			return err
		}
		commentIDs[value.ID] = commentReference{entryID: value.EntryID, parent: value.ParentID}
		previousComment = &order
		return nil
	})
	if err != nil {
		return err
	}
	expectedMedia := make(map[string]struct{}, len(mediaMembers))
	mediaIDs := make(map[string]struct{})
	lastMediaSHA, lastMediaID := "", ""
	mediaCount, err := validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at"}, func(value mediaRecord, fields map[string]json.RawMessage) error {
		if int64(len(mediaIDs)) >= document.Counts.Media {
			return errors.New("media records exceed the manifest count")
		}
		if err := requireNonNull(fields, "id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at"); err != nil {
			return err
		}
		created, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return err
		}
		metadata := sqlitestore.ArchiveMedia{
			ID: value.ID, SHA256: value.SHA256, OriginalName: value.OriginalName,
			MIMEType: value.MIMEType, ByteSize: value.ByteSize, Width: value.Width,
			Height: value.Height, CreatedAtMS: created.UnixMilli(),
		}
		if err := validateArchiveMedia(metadata); err != nil {
			return err
		}
		portablePath, err := mediaArchivePath(metadata)
		if err != nil || portablePath != value.ArchivePath {
			return errors.New("media archive path does not match metadata")
		}
		if _, exists := expectedMedia[portablePath]; exists {
			return errors.New("media archive path is duplicated")
		}
		if value.SHA256 < lastMediaSHA || value.SHA256 == lastMediaSHA && value.ID <= lastMediaID {
			return errors.New("media records are not in strict SHA-256 and ID order")
		}
		if _, exists := mediaIDs[value.ID]; exists {
			return errors.New("media ID is duplicated")
		}
		mediaID, err := domain.ParseID(value.ID)
		if err != nil {
			return err
		}
		expectedMedia[portablePath] = struct{}{}
		mediaIDs[value.ID] = struct{}{}
		portableMedia[portablePath] = media.Media{
			ID: mediaID, SHA256: value.SHA256, OriginalName: value.OriginalName,
			MIMEType: value.MIMEType, ByteSize: value.ByteSize, Width: value.Width,
			Height: value.Height, CreatedAt: created,
		}
		lastMediaSHA, lastMediaID = value.SHA256, value.ID
		return nil
	})
	if err != nil {
		return err
	}
	if entries != document.Counts.Entries || comments != document.Counts.Comments || mediaCount != document.Counts.Media || int64(len(mediaMembers)) != mediaCount {
		return errors.New("manifest record counts do not match portable records")
	}
	for _, member := range mediaMembers {
		if _, exists := expectedMedia[member.Name]; !exists {
			return errors.New("media records and original members are not one-to-one")
		}
	}
	for mediaID := range usageMediaIDs {
		if _, exists := mediaIDs[mediaID]; !exists {
			return errors.New("entry media usage refers to media absent from the archive")
		}
	}
	if site.AvatarMediaID != nil {
		if _, exists := mediaIDs[*site.AvatarMediaID]; !exists {
			return errors.New("site avatar refers to media absent from the archive")
		}
	}
	return nil
}

type commentOrder struct {
	entryID     string
	group       string
	rank        int
	createdAtMS int64
	id          string
}

func compareCommentOrder(left, right commentOrder) int {
	for _, pair := range [][2]string{{left.entryID, right.entryID}, {left.group, right.group}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.rank != right.rank {
		if left.rank < right.rank {
			return -1
		}
		return 1
	}
	if left.rank == 1 && left.createdAtMS != right.createdAtMS {
		if left.createdAtMS < right.createdAtMS {
			return -1
		}
		return 1
	}
	return strings.Compare(left.id, right.id)
}

func validateJSONLines[T any](ctx context.Context, file *zip.File, required []string, validate func(T, map[string]json.RawMessage) error) (int64, error) {
	if file == nil {
		return 0, errors.New("JSONL member is missing")
	}
	source, err := file.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = source.Close() }()
	tracked := &lastByteReader{reader: contextReader{ctx: ctx, reader: source}}
	scanner := bufio.NewScanner(tracked)
	scanner.Buffer(make([]byte, 64<<10), maxPortableRecordBytes)
	var count int64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if len(scanner.Bytes()) == 0 || !utf8.Valid(scanner.Bytes()) {
			return 0, errors.New("JSONL record is empty or not UTF-8")
		}
		line := append(append([]byte(nil), scanner.Bytes()...), '\n')
		var value T
		fields, err := decodePortableJSONObject(line, &value, required)
		if err != nil {
			return 0, err
		}
		if err := validate(value, fields); err != nil {
			return 0, err
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if tracked.total > 0 && tracked.last != '\n' {
		return 0, errors.New("JSONL member must end with LF")
	}
	return count, nil
}

func decodePortableJSONObject(data []byte, destination any, required []string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) || len(data) == 0 || data[len(data)-1] != '\n' || len(data) > 1 && data[len(data)-2] == '\r' {
		return nil, errors.New("JSON member is not canonical UTF-8 with LF")
	}
	payload := data[:len(data)-1]
	if err := validateJSONObjectTokens(payload); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return nil, errors.New("JSON record must be one object")
	}
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return nil, fmt.Errorf("required JSON field %q is missing", name)
		}
	}
	if destination != nil {
		if err := json.Unmarshal(payload, destination); err != nil {
			return nil, err
		}
	}
	return fields, nil
}

func requireObjectFields(data json.RawMessage, required, nonNull []string) error {
	withLF := append(append([]byte(nil), data...), '\n')
	fields, err := decodePortableJSONObject(withLF, nil, required)
	if err != nil {
		return err
	}
	return requireNonNull(fields, nonNull...)
}

func requireNonNull(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("required JSON field %q must not be null", name)
		}
	}
	return nil
}

func validateJSONObjectTokens(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("JSON record must be one object")
	}
	if err := consumeJSONObject(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("JSON member contains trailing values")
		}
		return err
	}
	return nil
}

func consumeJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("JSON object key is not a string")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("JSON object field %q is duplicated", name)
		}
		seen[name] = struct{}{}
		if err := consumeJSONValue(decoder); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("JSON object is not terminated")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return consumeJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing, ok := end.(json.Delim); !ok || closing != ']' {
			return errors.New("JSON array is not terminated")
		}
		return nil
	default:
		return errors.New("unexpected JSON closing delimiter")
	}
}

func parsePortableTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value || parsed.UnixMilli() <= 0 ||
		!parsed.Equal(time.UnixMilli(parsed.UnixMilli()).UTC()) {
		return time.Time{}, errors.New("portable timestamp is not canonical UTC")
	}
	return parsed, nil
}

type lastByteReader struct {
	reader io.Reader
	last   byte
	total  int64
}

func (reader *lastByteReader) Read(data []byte) (int, error) {
	read, err := reader.reader.Read(data)
	if read > 0 {
		reader.last = data[read-1]
		reader.total += int64(read)
	}
	return read, err
}

func isLowerHex(value string) bool {
	return value != "" && strings.IndexFunc(value, func(character rune) bool {
		return (character < '0' || character > '9') && (character < 'a' || character > 'f')
	}) < 0
}

func validateAppVersion(value string) error {
	if strings.TrimSpace(value) != value || value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.IndexFunc(value, func(character rune) bool {
		return character < 0x20 || character == 0x7f
	}) >= 0 {
		return errors.New("archive app version must be canonical printable UTF-8 no longer than 128 bytes")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer contextWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	return writer.writer.Write(data)
}

type digestWriter struct {
	writer io.Writer
	size   int64
}

func (writer *digestWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	writer.size += int64(written)
	return written, err
}
