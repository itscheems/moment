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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/mediatype"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	// FormatVersion is the import-facing Moment export protocol version.
	FormatVersion           = 3
	formatName              = "moment-export"
	copyBufferSize          = 128 << 10
	manifestFixedPointLimit = 32
	// FormatHeaderValue is the fail-closed HTTP/CLI negotiation token for v3.
	FormatHeaderValue = "moment-export-v3"
)

// ErrDestinationUnavailable classifies a safe output target that cannot be
// used without weakening atomic publication or overwrite guarantees.
var ErrDestinationUnavailable = errors.New("archive destination is unavailable")

const readme = "Moment portable export\n\n" +
	"Format: moment-export v3\n" +
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

// Export streams one v3 ZIP. It holds maintenance shared from CURRENT
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
	return exporter.exportResolved(ctx, destination, runtimeState, database, nil)
}

// ExportWithinLocks writes a complete v3 backup from an already-open active
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
	databaseInfo, databaseErr := os.Stat(database.Path())
	currentInfo, currentStatErr := os.Stat(current.DatabasePath)
	if current.ID != runtimeState.Current.ID || databaseErr != nil || currentStatErr != nil || !os.SameFile(databaseInfo, currentInfo) {
		return Summary{}, errors.New("backup database does not match authoritative CURRENT")
	}
	runtimeState.Current = current
	return exporter.exportResolved(ctx, destination, runtimeState, database, nil)
}

// ExportDevelopmentWithinLocks exports a pinned development database whose
// descriptor path may not compare textually or via path stat to CURRENT (for
// example Darwin /dev/fd). The complete destructive lock set and marked
// development runtime are mandatory.
func (exporter *Exporter) ExportDevelopmentWithinLocks(ctx context.Context, destination io.Writer, runtimeState *runtimefs.Runtime, database *sqlitestore.Database, held *runtimefs.LeaseSet, authority *runtimefs.DevelopmentAuthority) (Summary, error) {
	if exporter == nil || destination == nil || database == nil || authority == nil || authority.Validate(runtimeState, held) != nil {
		return Summary{}, runtimefs.ErrInsufficientLocks
	}
	if runtimeState.Current.ID == "" {
		return Summary{}, errors.New("development export runtime has no current generation")
	}
	return exporter.exportResolved(ctx, destination, runtimeState, database, authority)
}

// ExportDevelopmentDiagnosticWithinLocks exports one already-published
// development generation through a descriptor-bound read-only database. It is
// used by the isolated archive roundtrip verifier after the formal importer
// has closed its writable bundle.
func (exporter *Exporter) ExportDevelopmentDiagnosticWithinLocks(ctx context.Context, destination io.Writer, runtimeState *runtimefs.Runtime, database *sqlitestore.DiagnosticDatabase, held *runtimefs.LeaseSet, authority *runtimefs.DevelopmentAuthority) (Summary, error) {
	if exporter == nil || destination == nil || database == nil || authority == nil || authority.Validate(runtimeState, held) != nil {
		return Summary{}, runtimefs.ErrInsufficientLocks
	}
	return exporter.exportResolved(ctx, destination, runtimeState, database, authority)
}

type archiveSnapshotDatabase interface {
	BeginArchiveSnapshot(context.Context) (*sqlitestore.ArchiveSnapshot, error)
}

func (exporter *Exporter) exportResolved(ctx context.Context, destination io.Writer, runtimeState *runtimefs.Runtime, database archiveSnapshotDatabase, authority *runtimefs.DevelopmentAuthority) (summary Summary, exportErr error) {
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
	if err := protocol.jsonMember("data/site.json", portableSiteRecord(site)); err != nil {
		return Summary{}, err
	}

	// Accounts precede content because content refers to them: a comment names
	// the account that wrote it, and reading the archive top to bottom should
	// never meet a reference before the thing it refers to.
	userCount, err := protocol.jsonLines("data/users.jsonl", func(write func(any) error) error {
		return snapshot.Users(ctx, func(value sqlitestore.ArchiveUser) error {
			if err := validateArchiveUser(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("user", 1)
			}
			return write(portableUserRecord(value))
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("accounts could not be exported", err)
	}
	identityCount, err := protocol.jsonLines("data/identities.jsonl", func(write func(any) error) error {
		return snapshot.Identities(ctx, func(value sqlitestore.ArchiveIdentity) error {
			if err := validateArchiveIdentity(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("identity", 1)
			}
			return write(portableIdentityRecord(value))
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("account identities could not be exported", err)
	}

	entryCount, err := protocol.jsonLines("data/entries.jsonl", func(write func(any) error) error {
		return snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
			if err := validateArchiveEntry(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("entry", 1)
			}
			return write(portableEntryRecord(value))
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("entries could not be exported", err)
	}
	// An article's body follows its envelope, and a draft follows the article
	// it may be the working copy of, for the same reason accounts precede
	// content: reading the archive top to bottom should never meet a reference
	// before the thing it refers to.
	articleCount, err := protocol.jsonLines("data/entry_articles.jsonl", func(write func(any) error) error {
		return snapshot.EntryArticles(ctx, func(value sqlitestore.ArchiveEntryArticle) error {
			if _, err := validateArchiveEntryArticle(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("article", 1)
			}
			record, err := portableEntryArticleRecord(value)
			if err != nil {
				return err
			}
			return write(record)
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("articles could not be exported", err)
	}
	draftCount, err := protocol.jsonLines("data/article_drafts.jsonl", func(write func(any) error) error {
		return snapshot.ArticleDrafts(ctx, func(value sqlitestore.ArchiveArticleDraft) error {
			if err := validateArchiveArticleDraft(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("article_draft", 1)
			}
			record, err := portableArticleDraftRecord(value)
			if err != nil {
				return err
			}
			return write(record)
		})
	})
	if err != nil {
		return Summary{}, archiveInvalid("article drafts could not be exported", err)
	}
	commentCount, err := protocol.jsonLines("data/comments.jsonl", func(write func(any) error) error {
		return snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
			if err := validateArchiveComment(value); err != nil {
				return err
			}
			if exporter.recordYield != nil {
				exporter.recordYield("comment", 1)
			}
			return write(portableCommentRecord(value))
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
			record, err := portableMediaRecord(value)
			if err != nil {
				return err
			}
			return write(record)
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
		var source *os.File
		if authority != nil {
			source, err = openOriginalWithinRoot(authority.GenerationRoot(), value.StoragePath)
		} else {
			source, err = openOriginal(runtimeState.Current.Root, value.StoragePath)
		}
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
	if entryCount != counts.Entries || commentCount != counts.Comments || mediaCount != counts.Media || blobCount != counts.Media ||
		userCount != counts.Users || identityCount != counts.Identities ||
		articleCount != counts.Articles || draftCount != counts.ArticleDrafts {
		return Summary{}, archiveInvalid("archive record counts changed inside one snapshot", nil)
	}
	manifestBytes, err := canonicalManifest(manifest{
		Format: formatName, FormatVersion: FormatVersion, CreatedAt: createdAt.Format(time.RFC3339Nano),
		Source: sourceManifest{AppVersion: exporter.appVersion, InstanceID: runtimeState.Instance.ID},
		Counts: countManifest{
			Entries: counts.Entries, Comments: counts.Comments, Media: counts.Media,
			Users: counts.Users, Identities: counts.Identities,
			Articles: counts.Articles, ArticleDrafts: counts.ArticleDrafts,
		},
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

func openOriginalWithinRoot(generationRoot *os.Root, storagePath string) (*os.File, error) {
	if generationRoot == nil || storagePath == "" || path.IsAbs(storagePath) || strings.Contains(storagePath, "\\") ||
		path.Clean(storagePath) != storagePath || strings.HasPrefix(storagePath, "../") {
		return nil, errors.New("media storage path is unsafe")
	}
	name := filepath.Clean(filepath.FromSlash(storagePath))
	before, err := generationRoot.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(errors.New("media original is not a regular non-symlink file"), err)
	}
	file, err := generationRoot.Open(name)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, errors.Join(errors.New("media original changed while opening"), err)
	}
	return file, nil
}

// ExportFile writes beside the destination, flushes and validates the ZIP
// central directory, then atomically publishes with explicit overwrite policy.
func (exporter *Exporter) ExportFile(ctx context.Context, destination string, overwrite bool) (Summary, error) {
	return WriteFile(ctx, destination, overwrite, exporter.Export)
}

// WriteFile applies the archive file-publication contract to a streaming
// producer. It is shared by direct exports and API downloads.
func WriteFile(ctx context.Context, destination string, overwrite bool, producer func(context.Context, io.Writer) (Summary, error)) (Summary, error) {
	return writeFile(ctx, destination, overwrite, producer, ValidateFileContext)
}

func writeFile(ctx context.Context, destination string, overwrite bool, producer func(context.Context, io.Writer) (Summary, error), validate func(context.Context, string) error) (summary Summary, resultErr error) {
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
	if err := validate(ctx, partialPath); err != nil {
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
	Entries    int64 `json:"entries"`
	Comments   int64 `json:"comments"`
	Media      int64 `json:"media"`
	Users      int64 `json:"users,omitempty"`
	Identities int64 `json:"identities,omitempty"`
	// Articles and ArticleDrafts are additive on the same terms. Articles
	// counts bodies rather than article entries: the two are the same number
	// in a consistent archive, so stating the bodies is what makes an envelope
	// whose body was left behind a rejection instead of a blank post.
	Articles      int64 `json:"articles,omitempty"`
	ArticleDrafts int64 `json:"article_drafts,omitempty"`
}

type limitManifest struct {
	ExpandedBytes int64 `json:"expanded_bytes"`
}

type siteRecord struct {
	ProfileUserID   string  `json:"profile_user_id,omitempty"`
	Username        string  `json:"username,omitempty"`
	DisplayName     string  `json:"display_name,omitempty"`
	Title           string  `json:"title"`
	Bio             string  `json:"bio"`
	Timezone        string  `json:"timezone"`
	PublicBaseURL   *string `json:"public_base_url"`
	AvatarMediaID   *string `json:"avatar_media_id"`
	CoverMediaID    *string `json:"cover_media_id"`
	PinnedPostID    *string `json:"pinned_post_id"`
	CommentsEnabled bool    `json:"comments_enabled"`
}

// userRecord is the v2 wire shape of one account.
//
// It duplicates five fields siteRecord already carries -- username, display
// name, bio, avatar and cover -- because siteRecord cannot lose them without
// breaking archives written before this member existed. The exporter reads both
// from the same account row and the importer writes both from the same record,
// so the duplication cannot drift; validation rejects an archive where the two
// disagree rather than picking a winner.
type userRecord struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"display_name"`
	Bio           string  `json:"bio"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	Role          string  `json:"role"`
	Status        string  `json:"status"`
	// UsernameSetAt is absent while the handle is still the generated one, so
	// null and missing mean the same thing here.
	UsernameSetAt *string `json:"username_set_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// identityRecord is the v2 wire shape of one external credential.
//
// It carries no secret: a provider and the opaque account id that provider
// issued are what let a returning person be recognised, and nothing here can be
// replayed as a sign-in. Sessions are excluded from the archive entirely.
type identityRecord struct {
	Provider         string  `json:"provider"`
	ProviderUserID   string  `json:"provider_user_id"`
	UserID           string  `json:"user_id"`
	Email            *string `json:"email"`
	EmailVerified    bool    `json:"email_verified"`
	EmailDeliverable bool    `json:"email_deliverable"`
	DisplayName      string  `json:"display_name"`
	AvatarURL        *string `json:"avatar_url"`
	CreatedAt        string  `json:"created_at"`
	LastLoginAt      string  `json:"last_login_at"`
}

type usageRecord struct {
	MediaID  string  `json:"media_id"`
	Position int     `json:"position"`
	AltText  *string `json:"alt_text"`
}

type entryRecord struct {
	PublicationState string `json:"publication_state,omitempty"`
	AuthorUserID     string `json:"author_user_id,omitempty"`
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	// ContentMD is null for an article, whose body travels in its own member.
	ContentMD   *string `json:"content_md"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	PublishedAt string  `json:"published_at"`
	RepostedAt  *string `json:"reposted_at,omitempty"`
	EditedAt    *string `json:"edited_at,omitempty"`
	// Visibility is written only when it is not the default, so an archive
	// of a site that never hid anything is byte-for-byte what earlier
	// versions wrote -- and stays importable by them, which is the rollback
	// path. An absent key reads as everyone.
	Visibility       string           `json:"visibility,omitempty"`
	QuotedEntryID    *string          `json:"quoted_entry_id,omitempty"`
	QuoteUnavailable bool             `json:"quote_unavailable,omitempty"`
	Media            []usageRecord    `json:"media"`
	Location         *locationRecord  `json:"location,omitempty"`
	MapCard          *domain.MapCard  `json:"map_card,omitempty"`
	LinkCards        []linkCardRecord `json:"link_cards,omitempty"`
}

// entryArticleRecord is the v2 wire shape of one published article body.
//
// It travels in its own member rather than inside the entry record because an
// entry line is small and read by everything, while a body is up to half a
// megabyte and read by one thing. The entry it belongs to is the line in
// data/entries.jsonl with the same id and a null content_md; neither half is
// meaningful without the other, and a reader rejects an archive carrying one
// without the other.
//
// The excerpt and the indexed text are absent on purpose. Both are derived
// from the title and the body by one function each, and a second stored answer
// would be free to disagree with the words it claims to summarize.
type entryArticleRecord struct {
	EntryID string `json:"entry_id"`
	Title   string `json:"title"`
	// Body is the canonical serialization of the document, embedded as JSON
	// rather than as a quoted string: a reader that wants to look inside
	// should not have to unescape a document first. The bytes are what
	// domain.MarshalArticleDocument produces, which is also what the column
	// holds, so a round trip compares byte for byte.
	Body json.RawMessage `json:"body"`
	// CoverMediaID is the one article picture that does not travel through
	// entry_media, so it is the one an archive has to name itself.
	CoverMediaID *string `json:"cover_media_id"`
}

// articleDraftRecord is the v2 wire shape of one unfinished article.
//
// A draft is writing that exists nowhere else, so a backup that skipped it
// would lose work rather than lose a copy. It is not an entry: drafts live
// outside the timeline precisely so an unfinished article cannot be published
// by a query that forgot to filter.
type articleDraftRecord struct {
	AuthorUserID string          `json:"author_user_id,omitempty"`
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Body         json.RawMessage `json:"body"`
	// CoverMediaID and PublishedEntryID are both optional: a draft may have no
	// cover, and it names an entry only when it is the working copy of an
	// already published article.
	CoverMediaID     *string       `json:"cover_media_id"`
	PublishedEntryID *string       `json:"published_entry_id"`
	Media            []usageRecord `json:"media"`
	CreatedAt        string        `json:"created_at"`
	UpdatedAt        string        `json:"updated_at"`
}

// linkCardRecord is the portable card snapshot. The thumbnail travels by
// content hash rather than by media ID because media identifiers are reassigned
// on import while the hash is the stable anchor across instances.
type linkCardRecord struct {
	Position        int     `json:"position"`
	SourceURL       string  `json:"source_url"`
	CanonicalURL    string  `json:"canonical_url"`
	Provider        string  `json:"provider"`
	CardKind        string  `json:"card_kind"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	SiteName        string  `json:"site_name"`
	AuthorName      string  `json:"author_name"`
	DurationSeconds *int    `json:"duration_seconds"`
	ThumbnailSHA256 *string `json:"thumbnail_sha256"`
	FaviconSHA256   *string `json:"favicon_sha256,omitempty"`
	EmbedURL        string  `json:"embed_url"`
	CapturedAt      string  `json:"captured_at"`
	ViewCount       *int64  `json:"view_count,omitempty"`
	CommentCount    *int64  `json:"comment_count,omitempty"`
	LikeCount       *int64  `json:"like_count,omitempty"`
}

func portableOptionalTime(value *int64) *string {
	if value == nil {
		return nil
	}
	wire := portableTime(*value)
	return &wire
}

type locationRecord struct {
	Name             string  `json:"name"`
	DisplayName      string  `json:"display_name"`
	CountryCode      string  `json:"country_code"`
	Country          string  `json:"country"`
	Region           string  `json:"region"`
	City             string  `json:"city"`
	District         string  `json:"district"`
	Category         string  `json:"category"`
	Provider         string  `json:"provider"`
	ProviderPlaceID  string  `json:"provider_place_id"`
	LatitudeE7       *int64  `json:"latitude_e7"`
	LongitudeE7      *int64  `json:"longitude_e7"`
	CoordinateSystem *string `json:"coordinate_system"`
}

// commentRecord is the v2 wire shape of one comment. Fields added after v2
// shipped are omitempty and never required on read, because the format version
// is compared with strict equality and an archive written before the field
// existed must still import.
type commentRecord struct {
	ID               string          `json:"id"`
	EntryID          string          `json:"entry_id"`
	ParentID         *string         `json:"parent_id"`
	AuthorName       string          `json:"author_name"`
	AuthorUserID     *string         `json:"author_user_id,omitempty"`
	ReplyToCommentID *string         `json:"reply_to_comment_id,omitempty"`
	ContentMD        string          `json:"content_md"`
	CreatedAt        string          `json:"created_at"`
	Media            []usageRecord   `json:"media"`
	Location         *locationRecord `json:"location,omitempty"`
	Pinned           bool            `json:"pinned,omitempty"`
}

type mediaGrantRecord struct {
	UserID    string `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

type mediaRecord struct {
	UnreferencedSince *string            `json:"unreferenced_since,omitempty"`
	Grants            []mediaGrantRecord `json:"grants"`
	ID                string             `json:"id"`
	SHA256            string             `json:"sha256"`
	ArchivePath       string             `json:"archive_path"`
	OriginalName      string             `json:"original_name"`
	MIMEType          string             `json:"mime_type"`
	ByteSize          int64              `json:"byte_size"`
	Width             uint32             `json:"width"`
	Height            uint32             `json:"height"`
	CreatedAt         string             `json:"created_at"`
}

// The functions below are the single definition of how one authority row
// becomes one wire record, and back.
//
// They exist because there used to be two independent hand-written copies of
// every mapping: one in the exporter, and one in the staged-authority check
// that compares a rebuilt database back against the archive it was built from.
// That check decodes the archive side whole -- every field, automatically --
// and rebuilt the database side field by field, then compared the two with
// reflect.DeepEqual. A field the exporter wrote and the rebuild forgot produced
// "staged comments do not match the portable archive": no field named, and only
// when real data actually used the field, so tests and most archives stayed
// green. That is exactly how the pinned flag was lost, and with it every backup
// containing a pinned comment.
//
// With one definition per direction, a forgotten field is a compile error.
// Nothing outside these functions may build a wire record from an authority row.

func portableSiteRecord(value sqlitestore.ArchiveSite) siteRecord {
	return siteRecord{
		ProfileUserID: value.ProfileUserID, Username: value.Username, DisplayName: value.DisplayName,
		Title: value.Title, Bio: value.Bio, Timezone: value.Timezone,
		PublicBaseURL: value.PublicBaseURL, AvatarMediaID: value.AvatarMediaID,
		CoverMediaID: value.CoverMediaID, PinnedPostID: value.PinnedPostID,
		CommentsEnabled: value.CommentsEnabled,
	}
}

func archiveSiteOf(value siteRecord) sqlitestore.ArchiveSite {
	return sqlitestore.ArchiveSite{
		ProfileUserID: value.ProfileUserID, Username: value.Username, DisplayName: value.DisplayName,
		Title: value.Title, Bio: value.Bio, Timezone: value.Timezone,
		PublicBaseURL: value.PublicBaseURL, AvatarMediaID: value.AvatarMediaID,
		CoverMediaID: value.CoverMediaID, PinnedPostID: value.PinnedPostID,
		CommentsEnabled: value.CommentsEnabled,
	}
}

func portableUserRecord(value sqlitestore.ArchiveUser) userRecord {
	return userRecord{
		ID: value.ID, Username: value.Username, DisplayName: value.DisplayName, Bio: value.Bio,
		AvatarMediaID: value.AvatarMediaID, CoverMediaID: value.CoverMediaID,
		Role: value.Role, Status: value.Status,
		UsernameSetAt: portableOptionalTime(value.UsernameSetAtMS),
		CreatedAt:     portableTime(value.CreatedAtMS), UpdatedAt: portableTime(value.UpdatedAtMS),
	}
}

func archiveUserOf(value userRecord) (sqlitestore.ArchiveUser, error) {
	created, err := parsePortableTimestamp(value.CreatedAt)
	if err != nil {
		return sqlitestore.ArchiveUser{}, err
	}
	updated, err := parsePortableTimestamp(value.UpdatedAt)
	if err != nil {
		return sqlitestore.ArchiveUser{}, err
	}
	result := sqlitestore.ArchiveUser{
		ID: value.ID, Username: value.Username, DisplayName: value.DisplayName, Bio: value.Bio,
		AvatarMediaID: value.AvatarMediaID, CoverMediaID: value.CoverMediaID,
		Role: value.Role, Status: value.Status,
		CreatedAtMS: created.UnixMilli(), UpdatedAtMS: updated.UnixMilli(),
	}
	if value.UsernameSetAt != nil {
		moment, err := parsePortableTimestamp(*value.UsernameSetAt)
		if err != nil {
			return sqlitestore.ArchiveUser{}, err
		}
		millis := moment.UnixMilli()
		result.UsernameSetAtMS = &millis
	}
	return result, nil
}

func portableIdentityRecord(value sqlitestore.ArchiveIdentity) identityRecord {
	return identityRecord{
		Provider: value.Provider, ProviderUserID: value.ProviderUserID, UserID: value.UserID,
		Email: value.Email, EmailVerified: value.EmailVerified, EmailDeliverable: value.EmailDeliverable,
		DisplayName: value.DisplayName, AvatarURL: value.AvatarURL,
		CreatedAt: portableTime(value.CreatedAtMS), LastLoginAt: portableTime(value.LastLoginAtMS),
	}
}

func archiveIdentityOf(value identityRecord) (sqlitestore.ArchiveIdentity, error) {
	created, err := parsePortableTimestamp(value.CreatedAt)
	if err != nil {
		return sqlitestore.ArchiveIdentity{}, err
	}
	lastLogin, err := parsePortableTimestamp(value.LastLoginAt)
	if err != nil {
		return sqlitestore.ArchiveIdentity{}, err
	}
	return sqlitestore.ArchiveIdentity{
		Provider: value.Provider, ProviderUserID: value.ProviderUserID, UserID: value.UserID,
		Email: value.Email, EmailVerified: value.EmailVerified, EmailDeliverable: value.EmailDeliverable,
		DisplayName: value.DisplayName, AvatarURL: value.AvatarURL,
		CreatedAtMS: created.UnixMilli(), LastLoginAtMS: lastLogin.UnixMilli(),
	}, nil
}

func portableUsageRecords(values []sqlitestore.ArchiveUsage) []usageRecord {
	records := make([]usageRecord, len(values))
	for index, usage := range values {
		records[index] = usageRecord{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
	}
	return records
}

func portableEntryRecord(value sqlitestore.ArchiveEntry) entryRecord {
	return entryRecord{
		PublicationState: portablePublicationState(value.PublicationState), AuthorUserID: value.AuthorUserID, ID: value.ID, Kind: value.Kind, ContentMD: value.ContentMD,
		CreatedAt: portableTime(value.CreatedAtMS), UpdatedAt: portableTime(value.UpdatedAtMS),
		PublishedAt: portableTime(value.PublishedAtMS), RepostedAt: portableOptionalTime(value.RepostedAtMS), EditedAt: portableOptionalTime(value.EditedAtMS),
		Visibility:    portableVisibility(value.Visibility),
		QuotedEntryID: value.QuotedEntryID, QuoteUnavailable: value.QuoteUnavailable,
		Media: portableUsageRecords(value.Media), Location: portableLocation(value.Location), MapCard: domain.CloneMapCard(value.MapCard),
		LinkCards: portableLinkCards(value.LinkCards),
	}
}

// portableEntryArticleRecord canonicalizes one stored body on the way out.
//
// The bytes are re-derived from the parsed document rather than copied from the
// column, so what the archive carries is exactly what the grammar accepted. It
// is also what makes the staged-authority comparison a byte comparison: both
// sides pass through here.
func portableEntryArticleRecord(value sqlitestore.ArchiveEntryArticle) (entryArticleRecord, error) {
	body, err := domain.ParseArticleDocument([]byte(value.BodyJSON))
	if err != nil {
		return entryArticleRecord{}, err
	}
	serialized, err := domain.MarshalArticleDocument(body)
	if err != nil {
		return entryArticleRecord{}, err
	}
	return entryArticleRecord{
		EntryID: value.EntryID, Title: value.Title,
		Body: json.RawMessage(serialized), CoverMediaID: value.CoverMediaID,
	}, nil
}

func archiveEntryArticleOf(value entryArticleRecord) (sqlitestore.ArchiveEntryArticle, error) {
	body, err := domain.ParseArticleDocument(value.Body)
	if err != nil {
		return sqlitestore.ArchiveEntryArticle{}, err
	}
	return sqlitestore.ArchiveEntryArticle{
		EntryID: value.EntryID, Title: value.Title, BodyJSON: string(value.Body),
		SchemaVersion: body.SchemaVersion, CoverMediaID: value.CoverMediaID,
	}, nil
}

// portableArticleDraftRecord is the same canonicalization for a draft, read
// through the grammar that lets a draft say nothing yet.
func portableArticleDraftRecord(value sqlitestore.ArchiveArticleDraft) (articleDraftRecord, error) {
	body, err := domain.ParseArticleDraftDocument([]byte(value.BodyJSON))
	if err != nil {
		return articleDraftRecord{}, err
	}
	serialized, err := domain.MarshalArticleDocument(body)
	if err != nil {
		return articleDraftRecord{}, err
	}
	return articleDraftRecord{
		AuthorUserID: value.AuthorUserID, ID: value.ID, Title: value.Title, Body: json.RawMessage(serialized),
		CoverMediaID: value.CoverMediaID, PublishedEntryID: value.PublishedEntryID,
		Media:     portableUsageRecords(value.Media),
		CreatedAt: portableTime(value.CreatedAtMS), UpdatedAt: portableTime(value.UpdatedAtMS),
	}, nil
}

func archiveArticleDraftOf(value articleDraftRecord) (sqlitestore.ArchiveArticleDraft, error) {
	body, err := domain.ParseArticleDraftDocument(value.Body)
	if err != nil {
		return sqlitestore.ArchiveArticleDraft{}, err
	}
	created, err := parsePortableTimestamp(value.CreatedAt)
	if err != nil {
		return sqlitestore.ArchiveArticleDraft{}, err
	}
	updated, err := parsePortableTimestamp(value.UpdatedAt)
	if err != nil {
		return sqlitestore.ArchiveArticleDraft{}, err
	}
	usages := make([]sqlitestore.ArchiveUsage, len(value.Media))
	for index, usage := range value.Media {
		usages[index] = sqlitestore.ArchiveUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
	}
	return sqlitestore.ArchiveArticleDraft{
		AuthorUserID: value.AuthorUserID, ID: value.ID, Title: value.Title, BodyJSON: string(value.Body),
		SchemaVersion: body.SchemaVersion, CoverMediaID: value.CoverMediaID,
		PublishedEntryID: value.PublishedEntryID, Media: usages,
		CreatedAtMS: created.UnixMilli(), UpdatedAtMS: updated.UnixMilli(),
	}, nil
}

// portableVisibility spells the audience for the archive: nothing for the
// default, the wire value otherwise.
func portableVisibility(value domain.Visibility) string {
	if value == domain.VisibilityEveryone {
		return ""
	}
	return string(value)
}

// archiveVisibility reads the audience back. Absent means everyone; present
// has to be a value this build knows, because a value it does not know is a
// post it cannot promise to hide.
func archiveVisibility(value string) (domain.Visibility, error) {
	if value == "" {
		return domain.VisibilityEveryone, nil
	}
	visibility, err := domain.ParseVisibility(value)
	if err != nil {
		return "", errors.New("entry visibility must be everyone or only_me")
	}
	return visibility, nil
}

func portableCommentRecord(value sqlitestore.ArchiveComment) commentRecord {
	return commentRecord{
		ID: value.ID, EntryID: value.EntryID, ParentID: value.ParentID,
		AuthorName: value.AuthorName, AuthorUserID: value.AuthorUserID,
		ReplyToCommentID: value.ReplyToCommentID, ContentMD: value.ContentMD,
		CreatedAt: portableTime(value.CreatedAtMS), Media: portableUsageRecords(value.Media),
		Location: portableLocation(value.Location), Pinned: value.Pinned,
	}
}

func portableMediaRecord(value sqlitestore.ArchiveMedia) (mediaRecord, error) {
	archivePath, err := mediaArchivePath(value)
	if err != nil {
		return mediaRecord{}, err
	}
	grants := make([]mediaGrantRecord, 0, len(value.Grants))
	for _, grant := range value.Grants {
		grants = append(grants, mediaGrantRecord{UserID: grant.UserID, CreatedAt: portableTime(grant.CreatedAtMS)})
	}
	return mediaRecord{Grants: grants, UnreferencedSince: portableOptionalTime(value.UnreferencedSinceMS),
		ID: value.ID, SHA256: value.SHA256, ArchivePath: archivePath,
		OriginalName: value.OriginalName, MIMEType: value.MIMEType,
		ByteSize: value.ByteSize, Width: value.Width, Height: value.Height,
		CreatedAt: portableTime(value.CreatedAtMS),
	}, nil
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
		PublicationState: value.PublicationState, AuthorUserID: value.AuthorUserID,
		ID: id, MapCard: domain.CloneMapCard(value.MapCard), Kind: domain.EntryKind(value.Kind), ContentMD: value.ContentMD, Visibility: value.Visibility,
		CreatedAt:   time.UnixMilli(value.CreatedAtMS).UTC(),
		UpdatedAt:   time.UnixMilli(value.UpdatedAtMS).UTC(),
		PublishedAt: time.UnixMilli(value.PublishedAtMS).UTC(),
	}
	if value.EditedAtMS != nil {
		edited := time.UnixMilli(*value.EditedAtMS).UTC()
		entry.EditedAt = &edited
	}
	if value.RepostedAtMS != nil {
		repostedAt := time.UnixMilli(*value.RepostedAtMS).UTC()
		entry.RepostedAt = &repostedAt
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
	// An article body places up to fifty pictures where a moment's grid holds
	// nine, so the usage contract is chosen by kind rather than one limit
	// standing in for both.
	if domain.EntryKind(value.Kind) == domain.KindArticle {
		if err := domain.ValidateArticleMediaUsages(usages); err != nil {
			return err
		}
	} else if err := domain.ValidateEntryMediaUsages(usages); err != nil {
		return err
	}
	body, isMoment := domain.Entry{Kind: domain.EntryKind(value.Kind), ContentMD: value.ContentMD}.Markdown()
	if isMoment && value.MapCard == nil {
		if err := domain.ValidatePostMarkdown(body, len(usages)); err != nil {
			return err
		}
	}
	if value.QuotedEntryID != nil {
		if _, err := domain.ParseID(*value.QuotedEntryID); err != nil || *value.QuotedEntryID == value.ID {
			return errors.New("entry quote is invalid")
		}
	}
	if value.QuoteUnavailable && value.QuotedEntryID != nil {
		return errors.New("entry quote cannot be available and unavailable")
	}
	if value.Location != nil {
		return value.Location.Validate()
	}
	return nil
}

// validateArchiveEntryArticle reads one body through the grammar that let it
// in and returns the aggregate, so a caller holding the entry beside it can
// check that the pictures the two describe are the same pictures.
func validateArchiveEntryArticle(value sqlitestore.ArchiveEntryArticle) (domain.Article, error) {
	if _, err := domain.ParseID(value.EntryID); err != nil {
		return domain.Article{}, err
	}
	body, err := domain.ParseArticleDocument([]byte(value.BodyJSON))
	if err != nil {
		return domain.Article{}, err
	}
	coverID, err := archiveCoverID(value.CoverMediaID)
	if err != nil {
		return domain.Article{}, err
	}
	article, err := domain.NewArticle(value.Title, body, coverID)
	if err != nil {
		return domain.Article{}, err
	}
	// The column holds the trimmed spelling as a check constraint. A title the
	// constructor rewrites is one the archive and the restored database would
	// disagree about, so it is refused rather than quietly corrected.
	if article.Title != value.Title {
		return domain.Article{}, errors.New("article title is not the spelling the database stores")
	}
	return article, nil
}

// validateArchiveArticleDraft applies the same rules a draft is actually held
// to, which are fewer: it may have no title and it may say nothing yet.
func validateArchiveArticleDraft(value sqlitestore.ArchiveArticleDraft) error {
	if value.AuthorUserID != "" {
		if err := domain.ValidateUserID(value.AuthorUserID); err != nil {
			return err
		}
	}
	if _, err := domain.ParseID(value.ID); err != nil {
		return err
	}
	if value.CreatedAtMS <= 0 || value.UpdatedAtMS < value.CreatedAtMS {
		return errors.New("article draft timestamps must be positive and ordered")
	}
	body, err := domain.ParseArticleDraftDocument([]byte(value.BodyJSON))
	if err != nil {
		return err
	}
	coverID, err := archiveCoverID(value.CoverMediaID)
	if err != nil {
		return err
	}
	draft, err := domain.NewArticleDraft(value.Title, body, coverID)
	if err != nil {
		return err
	}
	if draft.Title != value.Title {
		return errors.New("article draft title is not the spelling the database stores")
	}
	if value.PublishedEntryID != nil {
		if _, err := domain.ParseID(*value.PublishedEntryID); err != nil {
			return err
		}
	}
	usages := make([]domain.EntryMediaUsage, len(value.Media))
	for index, usage := range value.Media {
		mediaID, err := domain.ParseID(usage.MediaID)
		if err != nil {
			return err
		}
		usages[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
	}
	if err := domain.ValidateArticleMediaUsages(usages); err != nil {
		return err
	}
	return sameArchiveUsages(draft.InlineMedia(), usages)
}

func archiveCoverID(value *string) (*domain.ID, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := domain.ParseID(*value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// sameArchiveUsages holds a stated picture list to the one the body places.
//
// The rows are a projection of the document: the body is the only place a
// picture's position is recorded, and the rows exist so reference counting and
// media visibility stay one SQL predicate. A record free to state a different
// list is free to keep a file alive the article no longer shows, or to publish
// one it does not.
func sameArchiveUsages(placed, stated []domain.EntryMediaUsage) error {
	if len(placed) != len(stated) {
		return fmt.Errorf("the body places %d pictures and the record lists %d", len(placed), len(stated))
	}
	for index := range placed {
		left, right := placed[index], stated[index]
		if left.MediaID != right.MediaID || left.Position != right.Position {
			return fmt.Errorf("picture %d is not the one the body places", index)
		}
		if (left.AltText == nil) != (right.AltText == nil) || left.AltText != nil && *left.AltText != *right.AltText {
			return fmt.Errorf("picture %d does not carry the description the body gives it", index)
		}
	}
	return nil
}

func portableLinkCards(values []sqlitestore.ArchiveLinkCard) []linkCardRecord {
	if len(values) == 0 {
		return nil
	}
	records := make([]linkCardRecord, 0, len(values))
	for _, value := range values {
		record := linkCardRecord{
			Position: value.Card.Position, SourceURL: value.Card.SourceURL,
			CanonicalURL: value.Card.CanonicalURL, Provider: string(value.Card.Provider),
			CardKind: string(value.Card.Kind), Title: value.Card.Title,
			Description: value.Card.Description, SiteName: value.Card.SiteName,
			AuthorName: value.Card.AuthorName, EmbedURL: value.Card.EmbedURL,
			CapturedAt: portableTime(value.Card.CapturedAt),
		}
		if value.Card.DurationSeconds != nil {
			seconds := *value.Card.DurationSeconds
			record.DurationSeconds = &seconds
		}
		record.ViewCount = portableCount(value.Card.ViewCount)
		record.CommentCount = portableCount(value.Card.CommentCount)
		record.LikeCount = portableCount(value.Card.LikeCount)
		if value.ThumbnailSHA256 != "" {
			sha := value.ThumbnailSHA256
			record.ThumbnailSHA256 = &sha
		}
		if value.FaviconSHA256 != "" {
			sha := value.FaviconSHA256
			record.FaviconSHA256 = &sha
		}
		records = append(records, record)
	}
	return records
}

func domainLinkCards(values []linkCardRecord) ([]sqlitestore.ArchiveLinkCard, error) {
	if len(values) == 0 {
		return nil, nil
	}
	cards := make([]sqlitestore.ArchiveLinkCard, 0, len(values))
	for index, value := range values {
		instant, err := parsePortableTimestamp(value.CapturedAt)
		if err != nil {
			return nil, errors.New("link card captured_at must be an RFC 3339 UTC instant")
		}
		capturedAt := instant.UnixMilli()
		card := domain.LinkCard{
			Position: index, SourceURL: value.SourceURL, CanonicalURL: value.CanonicalURL,
			Provider: domain.LinkCardProvider(value.Provider), Kind: domain.LinkCardKind(value.CardKind),
			Title: value.Title, Description: value.Description, SiteName: value.SiteName,
			AuthorName: value.AuthorName, EmbedURL: value.EmbedURL, CapturedAt: capturedAt,
		}
		if value.DurationSeconds != nil {
			seconds := *value.DurationSeconds
			card.DurationSeconds = &seconds
		}
		card.ViewCount = portableCount(value.ViewCount)
		card.CommentCount = portableCount(value.CommentCount)
		card.LikeCount = portableCount(value.LikeCount)
		if err := card.Validate(); err != nil {
			return nil, err
		}
		entry := sqlitestore.ArchiveLinkCard{Card: card}
		if value.ThumbnailSHA256 != nil {
			entry.ThumbnailSHA256 = *value.ThumbnailSHA256
		}
		if value.FaviconSHA256 != nil {
			entry.FaviconSHA256 = *value.FaviconSHA256
		}
		cards = append(cards, entry)
	}
	if err := domain.ValidateLinkCards(sqlitestore.ArchiveLinkCardsOf(cards)); err != nil {
		return nil, err
	}
	return cards, nil
}

func portableCount(value *int64) *int64 {
	if value == nil {
		return nil
	}
	count := *value
	return &count
}

func portableLocation(value *domain.Location) *locationRecord {
	if value == nil {
		return nil
	}
	record := &locationRecord{
		Name: value.Name, DisplayName: value.DisplayName, CountryCode: value.CountryCode,
		Country: value.Country, Region: value.Region, City: value.City, District: value.District,
		Category: value.Category, Provider: value.Provider, ProviderPlaceID: value.ProviderPlaceID,
	}
	if value.Point != nil {
		latitude, longitude, system := value.Point.LatitudeE7, value.Point.LongitudeE7, string(value.Point.System)
		record.LatitudeE7, record.LongitudeE7, record.CoordinateSystem = &latitude, &longitude, &system
	}
	return record
}

func domainLocation(value *locationRecord) (*domain.Location, error) {
	if value == nil {
		return nil, nil
	}
	location := &domain.Location{
		Name: value.Name, DisplayName: value.DisplayName, CountryCode: value.CountryCode,
		Country: value.Country, Region: value.Region, City: value.City, District: value.District,
		Category: value.Category, Provider: value.Provider, ProviderPlaceID: value.ProviderPlaceID,
	}
	if value.LatitudeE7 != nil || value.LongitudeE7 != nil || value.CoordinateSystem != nil {
		if value.LatitudeE7 == nil || value.LongitudeE7 == nil || value.CoordinateSystem == nil {
			return nil, errors.New("location coordinates must be complete or null")
		}
		location.Point = &domain.GeoPoint{
			LatitudeE7: *value.LatitudeE7, LongitudeE7: *value.LongitudeE7,
			System: domain.CoordinateSystem(*value.CoordinateSystem),
		}
	}
	if err := location.Validate(); err != nil {
		return nil, err
	}
	return location, nil
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
	var replyToID *domain.ID
	if value.ReplyToCommentID != nil {
		parsed, err := domain.ParseID(*value.ReplyToCommentID)
		if err != nil {
			return err
		}
		replyToID = &parsed
	}
	if value.CreatedAtMS <= 0 {
		return errors.New("comment timestamp must be positive")
	}
	if value.Pinned && value.ParentID != nil {
		return errors.New("only a top-level comment can be pinned")
	}
	if err := (domain.Comment{
		ID: id, EntryID: entryID, ParentID: parentID,
		AuthorName:   value.AuthorName,
		AuthorUserID: value.AuthorUserID, ReplyToCommentID: replyToID, ContentMD: value.ContentMD,
		Location: domain.CloneLocation(value.Location), CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(),
	}).Validate(); err != nil {
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
	return domain.ValidateCommentMediaUsages(usages)
}

func validateArchiveSite(value sqlitestore.ArchiveSite, development bool) error {
	for _, field := range []string{value.ProfileUserID, value.Username, value.DisplayName, value.Title, value.Bio, value.Timezone} {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return errors.New("site text is not canonical UTF-8")
		}
	}
	if value.ProfileUserID != "" {
		if len(value.ProfileUserID) != 32 || strings.Trim(value.ProfileUserID, "0123456789abcdef") != "" ||
			len(value.Username) < 3 || len(value.Username) > 30 || value.Username != strings.ToLower(value.Username) ||
			value.Username[0] < 'a' || value.Username[0] > 'z' || strings.Trim(value.Username, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" ||
			strings.TrimSpace(value.DisplayName) != value.DisplayName || value.DisplayName == "" || len([]rune(value.DisplayName)) > 80 {
			return errors.New("site profile identity is invalid")
		}
	} else if value.Username != "" || value.DisplayName != "" {
		return errors.New("site profile identity is incomplete")
	}
	if strings.TrimSpace(value.Title) == "" || len([]rune(value.Title)) > 120 {
		return errors.New("site title is empty")
	}
	if len([]rune(value.Bio)) > 500 {
		return errors.New("site bio exceeds the portable limit")
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
	if value.CoverMediaID != nil {
		if _, err := domain.ParseID(*value.CoverMediaID); err != nil {
			return err
		}
	}
	if value.PinnedPostID != nil {
		if _, err := domain.ParseID(*value.PinnedPostID); err != nil {
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

// maxPortableBioRunes is the bound the portable site record has always applied
// to a bio, and therefore the bound an account's bio is held to as well. The
// domain's own limit is lower, and deliberately not reused: it applies to what
// a person may write next, not to what an older instance already stored, and
// enforcing the smaller number here would make those backups unimportable.
const maxPortableBioRunes = 500

func validateArchiveUser(value sqlitestore.ArchiveUser) error {
	for _, field := range []string{value.ID, value.Username, value.DisplayName, value.Bio, value.Role, value.Status} {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return errors.New("account text is not canonical UTF-8")
		}
	}
	if err := domain.ValidateUserID(value.ID); err != nil {
		return err
	}
	// Shape only. The reserved-handle list is a write-time rule; applying it on
	// read would invalidate an account every time the list grows.
	if err := domain.ValidateStoredUsername(value.Username); err != nil {
		return err
	}
	if strings.TrimSpace(value.DisplayName) != value.DisplayName || value.DisplayName == "" || len([]rune(value.DisplayName)) > domain.MaxDisplayNameLength {
		return errors.New("account display name is invalid")
	}
	if len([]rune(value.Bio)) > maxPortableBioRunes {
		return errors.New("account bio exceeds the portable limit")
	}
	if err := domain.ValidateUserRole(domain.UserRole(value.Role)); err != nil {
		return err
	}
	if err := domain.ValidateAccountStatus(domain.AccountStatus(value.Status)); err != nil {
		return err
	}
	for _, reference := range []*string{value.AvatarMediaID, value.CoverMediaID} {
		if reference == nil {
			continue
		}
		if _, err := domain.ParseID(*reference); err != nil {
			return err
		}
	}
	if value.CreatedAtMS <= 0 || value.UpdatedAtMS <= 0 || (value.UsernameSetAtMS != nil && *value.UsernameSetAtMS <= 0) {
		return errors.New("account timestamps must be positive")
	}
	return nil
}

func validateArchiveIdentity(value sqlitestore.ArchiveIdentity) error {
	fields := []string{value.Provider, value.ProviderUserID, value.UserID, value.DisplayName}
	if value.Email != nil {
		fields = append(fields, *value.Email)
	}
	if value.AvatarURL != nil {
		fields = append(fields, *value.AvatarURL)
	}
	for _, field := range fields {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return errors.New("identity text is not canonical UTF-8")
		}
	}
	if err := domain.ValidateUserID(value.UserID); err != nil {
		return err
	}
	// The provider name is an open set on purpose: rejecting a value some
	// earlier version wrote is what would make that version's backups
	// unimportable. Only the shape is checked -- lowercase, bounded, and
	// without the colon that joins it to a provider account id when the pair
	// has to travel as one string.
	if value.Provider == "" || len(value.Provider) > 64 ||
		strings.Trim(value.Provider, "abcdefghijklmnopqrstuvwxyz0123456789._-") != "" {
		return errors.New("identity provider is not a canonical lowercase name")
	}
	if value.ProviderUserID == "" || len(value.ProviderUserID) > 255 {
		return errors.New("identity provider account id is invalid")
	}
	if len([]rune(value.DisplayName)) > domain.MaxDisplayNameLength {
		return errors.New("identity display name exceeds the portable limit")
	}
	if value.Email != nil && (*value.Email == "" || len(*value.Email) > 320) {
		return errors.New("identity email is invalid")
	}
	if value.AvatarURL != nil && (*value.AvatarURL == "" || len(*value.AvatarURL) > 2048) {
		return errors.New("identity avatar URL is invalid")
	}
	if value.CreatedAtMS <= 0 || value.LastLoginAtMS <= 0 {
		return errors.New("identity timestamps must be positive")
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
	maxBytes, supported := mediatype.MaxBytes(value.MIMEType)
	if !supported || value.ByteSize <= 0 || value.ByteSize > maxBytes || value.Width == 0 || value.Height == 0 || value.CreatedAtMS <= 0 ||
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
	extension, ok := mediatype.Extension(value.MIMEType)
	if !ok {
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

// Version 2 always contains real accounts and the complete article graph.
var (
	articlePortableMembers = []string{"README.txt", "data/site.json", "data/users.jsonl", "data/identities.jsonl", "data/entries.jsonl", "data/entry_articles.jsonl", "data/article_drafts.jsonl", "data/comments.jsonl", "data/media.jsonl"}
	portableTrailerMembers = []string{"manifest.json", "checksums.sha256"}
)

func portableLeadingMembers(seen map[string]struct{}) ([]string, error) {
	for _, required := range append(append([]string{}, articlePortableMembers...), portableTrailerMembers...) {
		if _, exists := seen[required]; !exists {
			return nil, fmt.Errorf("required member %q is missing", required)
		}
	}
	return articlePortableMembers, nil
}

// archiveCarriesAccounts reports whether a ZIP holds the accounts members.
func archiveCarriesAccounts(files map[string]*zip.File) bool {
	_, users := files["data/users.jsonl"]
	_, identities := files["data/identities.jsonl"]
	return users && identities
}

// archiveCarriesArticles reports whether a ZIP holds the article members. An
// archive without them carries no article and no draft, which is what every
// backup written before articles existed says, and it is true.
func archiveCarriesArticles(files map[string]*zip.File) bool {
	_, articles := files["data/entry_articles.jsonl"]
	_, drafts := files["data/article_drafts.jsonl"]
	return articles && drafts
}

// ValidateFile independently validates one complete v2 archive using only its
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
	if len(reader.File) < len(articlePortableMembers)+len(portableTrailerMembers) {
		return archiveInvalid("prepared ZIP is missing required members", nil)
	}
	seen := make(map[string]struct{}, len(reader.File))
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
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
	}
	leading, err := portableLeadingMembers(seen)
	if err != nil {
		return archiveInvalid("prepared ZIP portable member set is not one known layout", err)
	}
	if len(reader.File) < len(leading)+len(portableTrailerMembers) {
		return archiveInvalid("prepared ZIP is missing required members", nil)
	}
	for index, expected := range leading {
		if reader.File[index].Name != expected {
			return archiveInvalid("prepared ZIP member order is not canonical", nil)
		}
	}
	if reader.File[len(reader.File)-2].Name != "manifest.json" || reader.File[len(reader.File)-1].Name != "checksums.sha256" {
		return archiveInvalid("prepared ZIP trailer order is not canonical", nil)
	}
	lastMedia := ""
	for _, file := range reader.File[len(leading) : len(reader.File)-2] {
		if !strings.HasPrefix(file.Name, "media/") || file.Name <= lastMedia {
			return archiveInvalid("prepared ZIP media member order is not canonical", nil)
		}
		lastMedia = file.Name
	}
	readmeData, err := readSmallZIPMember(ctx, files["README.txt"], 64<<10)
	if err != nil || (!bytes.Equal(readmeData, []byte(readme)) && !bytes.Equal(readmeData, []byte(strings.Replace(readme, "v3", "v2", 1)))) {
		return archiveInvalid("prepared ZIP README is not a supported payload", err)
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
	if value.Format != formatName || (value.FormatVersion != 2 && value.FormatVersion != FormatVersion) || validateAppVersion(value.Source.AppVersion) != nil ||
		len(value.Source.InstanceID) != 32 || !isLowerHex(value.Source.InstanceID) || value.Counts.Entries < 0 || value.Counts.Comments < 0 || value.Counts.Media < 0 || value.Limits.ExpandedBytes < 1 {
		return archiveInvalid("prepared ZIP manifest contract is invalid", nil)
	}
	_, err = parsePortableTimestamp(value.CreatedAt)
	if err != nil {
		return archiveInvalid("prepared ZIP manifest timestamp is not canonical UTC", err)
	}
	portableMedia := make(map[string]media.Media, len(reader.File)-len(leading)-len(portableTrailerMembers))
	if err := validatePortableRecords(ctx, files, reader.File[len(leading):len(reader.File)-2], value, portableMedia); err != nil {
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
			maxBytes, supported := mediatype.MaxBytes(mediaMetadata.MIMEType)
			if !supported || file.UncompressedSize64 > uint64(maxBytes) {
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
	if err := validateArchiveSite(archiveSiteOf(site), true); err != nil {
		return err
	}
	accountMediaIDs, err := validatePortableAccounts(ctx, files, document, site)
	if err != nil {
		return err
	}

	entryIDs := make(map[string]struct{})
	quoteRefs := make(map[string]string)
	usageMediaIDs := make(map[string]struct{})
	thumbnailSHAs := make(map[string]struct{})
	// The pictures an article entry projects, kept until its body arrives, so
	// the two can be held to describing the same pictures.
	articleEntryUsages := make(map[string][]sqlitestore.ArchiveUsage)
	lastEntryID := ""
	entries, err := validateJSONLines[entryRecord](ctx, files["data/entries.jsonl"], portableEntryFields, func(value entryRecord, fields map[string]json.RawMessage) error {
		if document.FormatVersion < 3 {
			if _, present := fields["map_card"]; present {
				return errors.New("map_card requires archive format 3")
			}
		}
		if int64(len(entryIDs)) >= document.Counts.Entries {
			return errors.New("entry records exceed the manifest count")
		}
		// content_md is required to be present and allowed to be null, but
		// only for an article: a moment with no Markdown and an article with
		// some are both entries that disagree with themselves about what they
		// are. validateArchiveEntry below is what states that rule; requiring
		// the key non-null here would refuse every article before it got
		// there.
		if err := requireNonNull(fields, "id", "kind", "created_at", "updated_at", "published_at", "media"); err != nil {
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
			if err := requireObjectFields(rawUsages[index], []string{"media_id", "position", "alt_text"}, []string{"media_id", "position"}); err != nil {
				return err
			}
			usages[index] = sqlitestore.ArchiveUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
			usageMediaIDs[usage.MediaID] = struct{}{}
		}
		if raw, exists := fields["location"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if err := requireObjectFields(raw, []string{
				"name", "display_name", "country_code", "country", "region", "city", "district", "category",
				"provider", "provider_place_id", "latitude_e7", "longitude_e7", "coordinate_system",
			}, []string{"name", "display_name", "provider"}); err != nil {
				return err
			}
		}
		location, err := domainLocation(value.Location)
		if err != nil {
			return err
		}
		if value.ID <= lastEntryID {
			return errors.New("entry records are not in strict ID order")
		}
		if _, exists := entryIDs[value.ID]; exists {
			return errors.New("entry ID is duplicated")
		}
		linkCards, err := domainLinkCards(value.LinkCards)
		if err != nil {
			return err
		}
		for _, card := range linkCards {
			for _, sha := range []string{card.ThumbnailSHA256, card.FaviconSHA256} {
				if sha != "" {
					thumbnailSHAs[sha] = struct{}{}
				}
			}
		}
		if err := validateArchiveEntry(sqlitestore.ArchiveEntry{
			PublicationState: portablePublicationState(value.PublicationState), AuthorUserID: value.AuthorUserID, ID: value.ID, Kind: value.Kind, ContentMD: value.ContentMD, Visibility: visibility,
			CreatedAtMS: created.UnixMilli(), UpdatedAtMS: updated.UnixMilli(), PublishedAtMS: published.UnixMilli(), RepostedAtMS: repostedAtMS, EditedAtMS: editedAtMS, QuotedEntryID: value.QuotedEntryID, QuoteUnavailable: value.QuoteUnavailable, Media: usages, Location: location, MapCard: domain.CloneMapCard(value.MapCard), LinkCards: linkCards,
		}); err != nil {
			return err
		}
		entryIDs[value.ID] = struct{}{}
		if domain.EntryKind(value.Kind) == domain.KindArticle {
			articleEntryUsages[value.ID] = usages
		}
		if value.QuotedEntryID != nil {
			quoteRefs[value.ID] = *value.QuotedEntryID
		}
		lastEntryID = value.ID
		return nil
	})
	if err != nil {
		return err
	}
	for id, quotedID := range quoteRefs {
		if _, exists := entryIDs[quotedID]; !exists {
			return fmt.Errorf("entry %s quotes missing entry %s", id, quotedID)
		}
	}
	articleMediaIDs, err := validatePortableArticles(ctx, files, document, entryIDs, articleEntryUsages)
	if err != nil {
		return err
	}
	type commentReference struct {
		entryID string
		parent  *string
	}
	commentIDs := make(map[string]commentReference)
	// A reply may answer a sibling that appears later in canonical order, so
	// the targets are collected here and resolved once every ID is known.
	replyTargets := make(map[string]string)
	pinnedEntries := make(map[string]struct{})
	var previousComment *commentOrder
	comments, err := validateJSONLines[commentRecord](ctx, files["data/comments.jsonl"], []string{"id", "entry_id", "parent_id", "author_name", "content_md", "created_at"}, func(value commentRecord, fields map[string]json.RawMessage) error {
		if int64(len(commentIDs)) >= document.Counts.Comments {
			return errors.New("comment records exceed the manifest count")
		}
		if err := requireNonNull(fields, "id", "entry_id", "author_name", "content_md", "created_at"); err != nil {
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
		usages, err := portableCommentUsages(value, fields)
		if err != nil {
			return err
		}
		for _, usage := range usages {
			usageMediaIDs[usage.MediaID] = struct{}{}
		}
		location, err := domainLocation(value.Location)
		if err != nil {
			return err
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
		if value.Pinned {
			if _, exists := pinnedEntries[value.EntryID]; exists {
				return errors.New("an entry contains multiple pinned comments")
			}
			pinnedEntries[value.EntryID] = struct{}{}
		}
		order := commentOrder{entryID: value.EntryID, group: group, rank: rank, createdAtMS: created.UnixMilli(), id: value.ID}
		if previousComment != nil && compareCommentOrder(*previousComment, order) >= 0 {
			return errors.New("comment records are not in canonical thread order")
		}
		if err := validateArchiveComment(sqlitestore.ArchiveComment{
			ID: value.ID, EntryID: value.EntryID, ParentID: value.ParentID,
			AuthorName: value.AuthorName, AuthorUserID: value.AuthorUserID,
			ReplyToCommentID: value.ReplyToCommentID, ContentMD: value.ContentMD,
			CreatedAtMS: created.UnixMilli(), Media: usages, Location: location, Pinned: value.Pinned,
		}); err != nil {
			return err
		}
		commentIDs[value.ID] = commentReference{entryID: value.EntryID, parent: value.ParentID}
		if value.ReplyToCommentID != nil {
			replyTargets[value.ID] = *value.ReplyToCommentID
		}
		previousComment = &order
		return nil
	})
	if err != nil {
		return err
	}
	// The same rule the database enforces: a reply may only answer its own root
	// or another reply under that root. Checking it here turns a hostile archive
	// into a rejection rather than a constraint failure during staging.
	for id, targetID := range replyTargets {
		source := commentIDs[id]
		target, exists := commentIDs[targetID]
		if !exists || source.parent == nil || target.entryID != source.entryID ||
			(targetID != *source.parent && (target.parent == nil || *target.parent != *source.parent)) {
			return fmt.Errorf("comment %s answers comment %s outside its conversation", id, targetID)
		}
	}
	expectedMedia := make(map[string]struct{}, len(mediaMembers))
	mediaIDs := make(map[string]struct{})
	mediaSHAs := make(map[string]struct{})
	lastMediaSHA, lastMediaID := "", ""
	mediaCount, err := validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "grants"}, func(value mediaRecord, fields map[string]json.RawMessage) error {
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
		unreferenced, err := portableMediaRetention(value.UnreferencedSince)
		if err != nil {
			return err
		}
		metadata := sqlitestore.ArchiveMedia{
			UnreferencedSinceMS: unreferenced, ID: value.ID, SHA256: value.SHA256, OriginalName: value.OriginalName,
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
		mediaSHAs[value.SHA256] = struct{}{}
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
	// A thumbnail travels by content hash, so reachability is checked against
	// the packaged hashes rather than against reassigned identifiers.
	for sha := range thumbnailSHAs {
		if _, exists := mediaSHAs[sha]; !exists {
			return errors.New("link card image refers to media absent from the archive")
		}
	}
	if site.AvatarMediaID != nil {
		if _, exists := mediaIDs[*site.AvatarMediaID]; !exists {
			return errors.New("site avatar refers to media absent from the archive")
		}
	}
	if site.CoverMediaID != nil {
		if _, exists := mediaIDs[*site.CoverMediaID]; !exists {
			return errors.New("site cover refers to media absent from the archive")
		}
	}
	if site.PinnedPostID != nil {
		if _, exists := entryIDs[*site.PinnedPostID]; !exists {
			return errors.New("site pin refers to an entry absent from the archive")
		}
	}
	for id := range accountMediaIDs {
		if _, exists := mediaIDs[id]; !exists {
			return errors.New("an account picture refers to media absent from the archive")
		}
	}
	// Covers and draft pictures are the article references that do not travel
	// through entry_media, so they are the ones a packaging mistake loses. An
	// archive naming a file it did not pack restores an article with a hole in
	// it.
	for id := range articleMediaIDs {
		if _, exists := mediaIDs[id]; !exists {
			return errors.New("an article or draft picture refers to media absent from the archive")
		}
	}
	return validatePortableMediaGrants(ctx, files)
}

// portableEntryFields and the two article field lists are the keys a reader
// requires of each record. They are shared by validation and rebuild so the
// two cannot disagree about what a well-formed record contains.
var (
	portableEntryFields        = []string{"id", "kind", "content_md", "created_at", "updated_at", "published_at", "media"}
	portableEntryArticleFields = []string{"entry_id", "title", "body", "cover_media_id"}
	portableArticleDraftFields = []string{
		"id", "title", "body", "cover_media_id", "published_entry_id", "media", "created_at", "updated_at",
	}
)

// validatePortableArticles validates the two article members and returns every
// media id an article or a draft points at, for the reachability sweep the
// caller runs once media records are known.
//
// An archive without those members is not rejected: it was written before they
// existed, and it carries no article entry either -- which the entry stream has
// already proved, because an article entry with no body is refused here.
func validatePortableArticles(
	ctx context.Context,
	files map[string]*zip.File,
	document manifest,
	entryIDs map[string]struct{},
	articleEntryUsages map[string][]sqlitestore.ArchiveUsage,
) (map[string]struct{}, error) {
	pictures := make(map[string]struct{})
	if !archiveCarriesArticles(files) {
		return nil, errors.New("portable archives must contain article and draft members")
	}
	bodies := make(map[string]struct{}, len(articleEntryUsages))
	lastArticleID := ""
	articles, err := validateJSONLines[entryArticleRecord](ctx, files["data/entry_articles.jsonl"], portableEntryArticleFields, func(value entryArticleRecord, fields map[string]json.RawMessage) error {
		if int64(len(bodies)) >= document.Counts.Articles {
			return errors.New("article records exceed the manifest count")
		}
		if err := requireNonNull(fields, "entry_id", "title", "body"); err != nil {
			return err
		}
		if value.EntryID <= lastArticleID {
			return errors.New("article records are not in strict entry ID order")
		}
		usages, exists := articleEntryUsages[value.EntryID]
		if !exists {
			return fmt.Errorf("article body %s belongs to no article entry in this archive", value.EntryID)
		}
		archived, err := archiveEntryArticleOf(value)
		if err != nil {
			return err
		}
		if document.FormatVersion < 3 && archived.SchemaVersion >= 3 {
			return errors.New("article schema 3 requires archive format 3")
		}
		article, err := validateArchiveEntryArticle(archived)
		if err != nil {
			return err
		}
		stated := make([]domain.EntryMediaUsage, len(usages))
		for index, usage := range usages {
			mediaID, err := domain.ParseID(usage.MediaID)
			if err != nil {
				return err
			}
			stated[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
		}
		if err := sameArchiveUsages(article.InlineMedia(), stated); err != nil {
			return fmt.Errorf("article %s: %w", value.EntryID, err)
		}
		if value.CoverMediaID != nil {
			pictures[*value.CoverMediaID] = struct{}{}
		}
		bodies[value.EntryID] = struct{}{}
		lastArticleID = value.EntryID
		return nil
	})
	if err != nil {
		return nil, err
	}
	for id := range articleEntryUsages {
		if _, exists := bodies[id]; !exists {
			return nil, fmt.Errorf("article entry %s has no body in this archive", id)
		}
	}
	draftIDs := make(map[string]struct{})
	publishedEntries := make(map[string]struct{})
	lastDraftID := ""
	drafts, err := validateJSONLines[articleDraftRecord](ctx, files["data/article_drafts.jsonl"], portableArticleDraftFields, func(value articleDraftRecord, fields map[string]json.RawMessage) error {
		if int64(len(draftIDs)) >= document.Counts.ArticleDrafts {
			return errors.New("article draft records exceed the manifest count")
		}
		if err := requireNonNull(fields, "id", "title", "body", "media", "created_at", "updated_at"); err != nil {
			return err
		}
		if value.Media == nil {
			return errors.New("article draft media must be an array, not null")
		}
		var rawUsages []json.RawMessage
		if err := json.Unmarshal(fields["media"], &rawUsages); err != nil || rawUsages == nil || len(rawUsages) != len(value.Media) {
			return errors.New("article draft media must be a complete array")
		}
		for _, raw := range rawUsages {
			if err := requireObjectFields(raw, []string{"media_id", "position", "alt_text"}, []string{"media_id", "position"}); err != nil {
				return err
			}
		}
		if value.ID <= lastDraftID {
			return errors.New("article draft records are not in strict ID order")
		}
		archived, err := archiveArticleDraftOf(value)
		if err != nil {
			return err
		}
		if document.FormatVersion < 3 && archived.SchemaVersion >= 3 {
			return errors.New("article schema 3 requires archive format 3")
		}
		if err := validateArchiveArticleDraft(archived); err != nil {
			return err
		}
		if value.PublishedEntryID != nil {
			if _, exists := entryIDs[*value.PublishedEntryID]; !exists {
				return errors.New("an article draft names an entry absent from the archive")
			}
			// The column is a unique index where it is not null: two drafts
			// claiming one article would restore as one draft or neither.
			if _, exists := publishedEntries[*value.PublishedEntryID]; exists {
				return errors.New("two article drafts are the working copy of one article")
			}
			publishedEntries[*value.PublishedEntryID] = struct{}{}
		}
		if value.CoverMediaID != nil {
			pictures[*value.CoverMediaID] = struct{}{}
		}
		for _, usage := range value.Media {
			pictures[usage.MediaID] = struct{}{}
		}
		draftIDs[value.ID] = struct{}{}
		lastDraftID = value.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if articles != document.Counts.Articles || drafts != document.Counts.ArticleDrafts {
		return nil, errors.New("manifest article counts do not match portable records")
	}
	return pictures, nil
}

// validatePortableAccounts validates the two accounts members and returns every
// media id an account points at, for the reachability sweep the caller runs
// once media records are known.
//
// An archive without those members is not rejected: it was written before they
// existed and still states its one account, in the site record. The counts it
// declares must then say so too, which is what stops a manifest from claiming
// accounts an archive does not carry.
func validatePortableAccounts(ctx context.Context, files map[string]*zip.File, document manifest, site siteRecord) (map[string]struct{}, error) {
	pictures := make(map[string]struct{})
	if !archiveCarriesAccounts(files) {
		return nil, errors.New("portable archives must contain account members")
	}
	if site.ProfileUserID == "" {
		return nil, errors.New("an archive carrying accounts must name its display account in the site record")
	}
	userIDs := make(map[string]struct{})
	usernames := make(map[string]struct{})
	activeAdmins := 0
	lastUserID := ""
	users, err := validateJSONLines[userRecord](ctx, files["data/users.jsonl"], []string{
		"id", "username", "display_name", "bio", "avatar_media_id", "cover_media_id", "role", "status", "created_at", "updated_at",
	}, func(value userRecord, fields map[string]json.RawMessage) error {
		if int64(len(userIDs)) >= document.Counts.Users {
			return errors.New("account records exceed the manifest count")
		}
		if err := requireNonNull(fields, "id", "username", "display_name", "bio", "role", "status", "created_at", "updated_at"); err != nil {
			return err
		}
		account, err := archiveUserOf(value)
		if err != nil {
			return err
		}
		if err := validateArchiveUser(account); err != nil {
			return err
		}
		if value.ID <= lastUserID {
			return errors.New("account records are not in strict ID order")
		}
		handle := strings.ToLower(value.Username)
		if _, exists := usernames[handle]; exists {
			return errors.New("account username is duplicated")
		}
		usernames[handle] = struct{}{}
		if domain.UserRole(value.Role) == domain.UserRoleAdmin && domain.AccountStatus(value.Status) == domain.AccountActive {
			activeAdmins++
		}
		if value.ID == site.ProfileUserID {
			if value.Username != site.Username || value.DisplayName != site.DisplayName ||
				value.Bio != site.Bio || !reflect.DeepEqual(value.AvatarMediaID, site.AvatarMediaID) ||
				!reflect.DeepEqual(value.CoverMediaID, site.CoverMediaID) {
				return errors.New("display account and site record describe different profiles")
			}
		}
		for _, picture := range []*string{value.AvatarMediaID, value.CoverMediaID} {
			if picture != nil {
				pictures[*picture] = struct{}{}
			}
		}
		userIDs[value.ID] = struct{}{}
		lastUserID = value.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if activeAdmins < 1 {
		return nil, errors.New("portable accounts must contain an active administrator")
	}
	if _, found := userIDs[site.ProfileUserID]; !found {
		return nil, errors.New("site profile account is absent")
	}
	lastProvider, lastProviderUserID := "", ""
	seenIdentities := int64(0)
	identities, err := validateJSONLines[identityRecord](ctx, files["data/identities.jsonl"], []string{
		"provider", "provider_user_id", "user_id", "email", "email_verified", "email_deliverable", "display_name", "avatar_url", "created_at", "last_login_at",
	}, func(value identityRecord, fields map[string]json.RawMessage) error {
		if seenIdentities >= document.Counts.Identities {
			return errors.New("identity records exceed the manifest count")
		}
		if err := requireNonNull(fields, "provider", "provider_user_id", "user_id", "email_verified", "email_deliverable", "display_name", "created_at", "last_login_at"); err != nil {
			return err
		}
		identity, err := archiveIdentityOf(value)
		if err != nil {
			return err
		}
		if err := validateArchiveIdentity(identity); err != nil {
			return err
		}
		if value.Provider < lastProvider || value.Provider == lastProvider && value.ProviderUserID <= lastProviderUserID {
			return errors.New("identity records are not in strict provider and account order")
		}
		if _, exists := userIDs[value.UserID]; !exists {
			return errors.New("identity refers to an account absent from the archive")
		}
		lastProvider, lastProviderUserID = value.Provider, value.ProviderUserID
		seenIdentities++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if users != document.Counts.Users || identities != document.Counts.Identities {
		return nil, errors.New("manifest account counts do not match portable records")
	}
	return pictures, nil
}

func portableCommentUsages(value commentRecord, fields map[string]json.RawMessage) ([]sqlitestore.ArchiveUsage, error) {
	raw, present := fields["media"]
	if !present {
		return []sqlitestore.ArchiveUsage{}, nil
	}
	var rawUsages []json.RawMessage
	if err := json.Unmarshal(raw, &rawUsages); err != nil || rawUsages == nil || len(rawUsages) != len(value.Media) {
		return nil, errors.New("comment media must be a complete array")
	}
	usages := make([]sqlitestore.ArchiveUsage, len(value.Media))
	for index, usage := range value.Media {
		if err := requireObjectFields(rawUsages[index], []string{"media_id", "position", "alt_text"}, []string{"media_id", "position"}); err != nil {
			return nil, err
		}
		usages[index] = sqlitestore.ArchiveUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
	}
	return usages, nil
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

// jsonLinesReader reads one JSONL member a record at a time.
//
// It exists because two members have to be read in lockstep when a generation
// is rebuilt: an article's body lives in its own member, and the entry it
// belongs to cannot be written without it -- an article's indexed text is its
// title and its words. Buffering every body first would hold half a megabyte
// per article in memory, and writing the envelope first and patching it after
// would leave the index claiming the article says nothing. Every other reader
// takes a member whole through validateJSONLines, which is this reader with the
// loop already written.
type jsonLinesReader[T any] struct {
	source   io.ReadCloser
	tracked  *lastByteReader
	scanner  *bufio.Scanner
	required []string
}

func openJSONLines[T any](ctx context.Context, file *zip.File, required []string) (*jsonLinesReader[T], error) {
	if file == nil {
		return nil, errors.New("JSONL member is missing")
	}
	source, err := file.Open()
	if err != nil {
		return nil, err
	}
	tracked := &lastByteReader{reader: contextReader{ctx: ctx, reader: source}}
	scanner := bufio.NewScanner(tracked)
	scanner.Buffer(make([]byte, 64<<10), maxPortableRecordBytes)
	return &jsonLinesReader[T]{source: source, tracked: tracked, scanner: scanner, required: required}, nil
}

// next reports the following record, or false once the member is exhausted.
func (reader *jsonLinesReader[T]) next(ctx context.Context) (T, map[string]json.RawMessage, bool, error) {
	var value T
	if !reader.scanner.Scan() {
		if err := reader.scanner.Err(); err != nil {
			return value, nil, false, err
		}
		if reader.tracked.total > 0 && reader.tracked.last != '\n' {
			return value, nil, false, errors.New("JSONL member must end with LF")
		}
		return value, nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return value, nil, false, err
	}
	if len(reader.scanner.Bytes()) == 0 || !utf8.Valid(reader.scanner.Bytes()) {
		return value, nil, false, errors.New("JSONL record is empty or not UTF-8")
	}
	line := append(append([]byte(nil), reader.scanner.Bytes()...), '\n')
	fields, err := decodePortableJSONObject(line, &value, reader.required)
	if err != nil {
		return value, nil, false, err
	}
	return value, fields, true, nil
}

func (reader *jsonLinesReader[T]) close() error { return reader.source.Close() }

func validateJSONLines[T any](ctx context.Context, file *zip.File, required []string, validate func(T, map[string]json.RawMessage) error) (int64, error) {
	reader, err := openJSONLines[T](ctx, file, required)
	if err != nil {
		return 0, err
	}
	defer func() { _ = reader.close() }()
	var count int64
	for {
		value, fields, more, err := reader.next(ctx)
		if err != nil {
			return 0, err
		}
		if !more {
			return count, nil
		}
		if err := validate(value, fields); err != nil {
			return 0, err
		}
		count++
	}
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

func portablePublicationState(state string) string {
	if state == "published" {
		return ""
	}
	return state
}
