package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// AccountFormatVersion identifies the restorable personal portability contract.
const AccountFormatVersion = 3

// AccountFormatHeaderValue distinguishes personal portability from site recovery.
const AccountFormatHeaderValue = "moment-account-export-v3"
const accountFormatName = "moment-account-export"

type accountManifest struct {
	Format           string        `json:"format"`
	FormatVersion    int           `json:"format_version"`
	CreatedAt        string        `json:"created_at"`
	AccountID        string        `json:"account_id"`
	SourceInstanceID string        `json:"source_instance_id,omitempty"`
	Counts           countManifest `json:"counts"`
}

// ExportAccount streams only one account's portable personal data.
func (exporter *Exporter) ExportAccount(ctx context.Context, destination io.Writer, accountID string) (summary Summary, resultErr error) {
	if exporter == nil || destination == nil || accountID == "" {
		return Summary{}, errors.New("exporter, destination and account are required")
	}
	runtimeState, maintenance, err := runtimefs.OpenForSnapshotContext(ctx, exporter.dataDir)
	if err != nil {
		return Summary{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, maintenance.Release()) }()
	lease, err := runtimeState.Locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
	if err != nil {
		return Summary{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Release()) }()
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return Summary{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		return Summary{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	var account *sqlitestore.ArchiveUser
	profiles := map[string]sqlitestore.ArchiveUser{}
	if err := snapshot.Users(ctx, func(value sqlitestore.ArchiveUser) error {
		profiles[value.ID] = value
		if value.ID == accountID {
			account = &value
		}
		return nil
	}); err != nil {
		return Summary{}, err
	}
	if account == nil {
		return Summary{}, domain.NewError(domain.CodeAccountNotFound, "account does not exist", nil)
	}
	if err := validateArchiveUser(*account); err != nil {
		return Summary{}, err
	}
	digest := sha256.New()
	counted := &digestWriter{writer: contextWriter{ctx: ctx, writer: io.MultiWriter(destination, digest)}}
	writer := zip.NewWriter(counted)
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, writer.Close())
		}
	}()
	now := domain.NormalizeTime(exporter.clock())
	protocol := &protocolWriter{zip: writer, createdAt: now}
	if err := protocol.bytesMember("README.txt", zip.Deflate, []byte("Moment personal data export v3\nRestore into a signed-in account using personal import.\nIncludes this account's content and its comment conversations; external quoted content is excluded.\nChecksums cover every member except checksums.sha256 itself.\n")); err != nil {
		return Summary{}, err
	}
	if err := protocol.jsonMember("data/account.json", portableUserRecord(*account)); err != nil {
		return Summary{}, err
	}
	selected := map[string]bool{}
	selectedSHAs := map[string]bool{}
	resolvedSHAs := map[string]bool{}
	for _, id := range []*string{account.AvatarMediaID, account.CoverMediaID} {
		if id != nil {
			selected[*id] = true
		}
	}
	identities, err := protocol.jsonLines("data/identities.jsonl", func(write func(any) error) error {
		return snapshot.Identities(ctx, func(value sqlitestore.ArchiveIdentity) error {
			if value.UserID != accountID {
				return nil
			}
			if err := validateArchiveIdentity(value); err != nil {
				return err
			}
			return write(portableIdentityRecord(value))
		})
	})
	if err != nil {
		return Summary{}, err
	}
	selectedEntries := make(map[string]bool)
	if err := snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
		if value.AuthorUserID == accountID {
			selectedEntries[value.ID] = true
		}
		return nil
	}); err != nil {
		return Summary{}, err
	}
	selectedComments := map[string]bool{}
	if err := snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
		if selectedEntries[value.EntryID] {
			selectedComments[value.ID] = true
		}
		return nil
	}); err != nil {
		return Summary{}, err
	}
	entries, err := protocol.jsonLines("data/entries.jsonl", func(write func(any) error) error {
		return snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
			if value.AuthorUserID != accountID {
				return nil
			}
			selectedEntries[value.ID] = true
			if err := validateArchiveEntry(value); err != nil {
				return err
			}
			for _, usage := range value.Media {
				selected[usage.MediaID] = true
			}
			for _, card := range value.LinkCards {
				for _, sha := range []string{card.ThumbnailSHA256, card.FaviconSHA256} {
					if sha != "" {
						selectedSHAs[sha] = true
					}
				}
			}

			if value.QuotedEntryID != nil && !selectedEntries[*value.QuotedEntryID] || value.QuotedCommentID != nil && !selectedComments[*value.QuotedCommentID] {
				value.QuotedEntryID = nil
				value.QuotedCommentID = nil
				value.QuoteUnavailable = true
			}
			return write(portableEntryRecord(value))
		})
	})
	if err != nil {
		return Summary{}, err
	}
	// An article's words are not in its entry, so an export that stopped at the
	// envelope would hand somebody a file full of posts with nothing in them.
	// Drafts come too: unfinished writing exists nowhere else, which makes it
	// the part of a personal export a person is most likely to actually want.
	articles, err := protocol.jsonLines("data/entry_articles.jsonl", func(write func(any) error) error {
		return snapshot.EntryArticles(ctx, func(value sqlitestore.ArchiveEntryArticle) error {
			if !selectedEntries[value.EntryID] {
				return nil
			}
			if _, err := validateArchiveEntryArticle(value); err != nil {
				return err
			}
			if value.CoverMediaID != nil {
				selected[*value.CoverMediaID] = true
			}
			record, err := portableEntryArticleRecord(value)
			if err != nil {
				return err
			}
			return write(record)
		})
	})
	if err != nil {
		return Summary{}, err
	}
	drafts, err := protocol.jsonLines("data/article_drafts.jsonl", func(write func(any) error) error {
		return snapshot.ArticleDrafts(ctx, func(value sqlitestore.ArchiveArticleDraft) error {
			if value.AuthorUserID != accountID {
				return nil
			}
			if err := validateArchiveArticleDraft(value); err != nil {
				return err
			}
			if value.CoverMediaID != nil {
				selected[*value.CoverMediaID] = true
			}
			for _, usage := range value.Media {
				selected[usage.MediaID] = true
			}
			record, err := portableArticleDraftRecord(value)
			if err != nil {
				return err
			}
			return write(record)
		})
	})
	if err != nil {
		return Summary{}, err
	}
	comments, err := protocol.jsonLines("data/comments.jsonl", func(write func(any) error) error {
		return snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
			if !selectedEntries[value.EntryID] {
				return nil
			}
			if value.AuthorUserID != nil && *value.AuthorUserID != accountID {
				if profile, ok := profiles[*value.AuthorUserID]; ok {
					value.AuthorSnapshot = &domain.CommentAuthorSnapshot{Username: profile.Username, DisplayName: profile.DisplayName, AvatarMediaID: profile.AvatarMediaID}
				}
				value.AuthorUserID = nil
			}
			if value.AuthorSnapshot != nil && value.AuthorSnapshot.AvatarMediaID != nil {
				selected[*value.AuthorSnapshot.AvatarMediaID] = true
			}
			if err := validateArchiveComment(value); err != nil {
				return err
			}
			for _, usage := range value.Media {
				selected[usage.MediaID] = true
			}
			return write(portableCommentRecord(value))
		})
	})
	if err != nil {
		return Summary{}, err
	}
	mediaCount, err := protocol.jsonLines("data/media.jsonl", func(write func(any) error) error {
		return snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
			if selectedSHAs[value.SHA256] {
				selected[value.ID] = true
				resolvedSHAs[value.SHA256] = true
			}
			if !selected[value.ID] {
				return nil
			}
			value.Grants = []sqlitestore.ArchiveMediaGrant{}
			value.UnreferencedSinceMS = nil
			record, err := portableMediaRecord(value)
			if err != nil {
				return err
			}
			return write(record)
		})
	})
	if err != nil {
		return Summary{}, err
	}
	if mediaCount != int64(len(selected)) || len(resolvedSHAs) != len(selectedSHAs) {
		return Summary{}, archiveInvalid("account references missing media", nil)
	}
	buffer := make([]byte, copyBufferSize)
	err = snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
		if !selected[value.ID] {
			return nil
		}
		source, err := openOriginal(runtimeState.Current.Root, value.StoragePath)
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }()
		id, err := domain.ParseID(value.ID)
		if err != nil {
			return err
		}
		if err := media.VerifyPortableOriginal(ctx, source, media.Media{ID: id, SHA256: value.SHA256, StoragePath: value.StoragePath, OriginalName: value.OriginalName, MIMEType: value.MIMEType, ByteSize: value.ByteSize, Width: value.Width, Height: value.Height, CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC()}); err != nil {
			return err
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return err
		}
		name, err := mediaArchivePath(value)
		if err != nil {
			return err
		}
		member, err := protocol.startMember(name, zip.Store)
		if err != nil {
			return err
		}
		count, err := io.CopyBuffer(member, contextReader{ctx: ctx, reader: source}, buffer)
		if err != nil {
			return err
		}
		if count != value.ByteSize || hex.EncodeToString(member.digest.Sum(nil)) != value.SHA256 {
			return archiveInvalid("account media checksum mismatch", nil)
		}
		return protocol.finishMember(member)
	})
	if err != nil {
		return Summary{}, err
	}
	manifest := accountManifest{Format: accountFormatName, FormatVersion: AccountFormatVersion, CreatedAt: now.Format(time.RFC3339Nano), AccountID: accountID, SourceInstanceID: runtimeState.Instance.ID, Counts: countManifest{Users: 1, Identities: identities, Entries: entries, Comments: comments, Media: mediaCount, Articles: articles, ArticleDrafts: drafts}}
	if err := protocol.jsonMember("manifest.json", manifest); err != nil {
		return Summary{}, err
	}
	if err := protocol.writeChecksums(); err != nil {
		return Summary{}, err
	}
	if err := writer.Close(); err != nil {
		return Summary{}, err
	}
	closed = true
	return Summary{FormatVersion: AccountFormatVersion, ArchiveBytes: counted.size, ArchiveSHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// WriteAccountFile shares atomic publication with backups but validates the
// distinct personal format; it never sends a personal archive through import.
func WriteAccountFile(ctx context.Context, destination string, overwrite bool, producer func(context.Context, io.Writer) (Summary, error)) (Summary, error) {
	return writeFile(ctx, destination, overwrite, producer, ValidateAccountFileContext)
}

// ValidateAccountFileContext verifies format and every ZIP member's checksum.
func ValidateAccountFileContext(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, err := zip.OpenReader(name)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	if int64(len(reader.File)) > defaultMaxMedia+10 {
		return archiveInvalid("account archive member count exceeds the budget", nil)
	}
	files := map[string]*zip.File{}
	var expanded uint64
	for _, file := range reader.File {
		if err := validateMemberName(file.Name); err != nil {
			return err
		}
		if _, exists := files[file.Name]; exists {
			return archiveInvalid("duplicate account archive member", nil)
		}
		if !file.Mode().IsRegular() || file.Mode()&os.ModeSymlink != 0 {
			return archiveInvalid("account archive contains a non-regular member", nil)
		}
		if file.UncompressedSize64 > uint64(defaultMaxExpandedBytes)-expanded {
			return archiveInvalid("account archive expanded bytes exceed the budget", nil)
		}
		if file.UncompressedSize64 > 0 && (file.CompressedSize64 == 0 || file.UncompressedSize64/file.CompressedSize64 > defaultMaxCompression) {
			return archiveInvalid("account member compression ratio exceeds the budget", nil)
		}
		expanded += file.UncompressedSize64
		switch file.Name {
		case "README.txt", "manifest.json", "checksums.sha256", "data/account.json", "data/entries.jsonl",
			"data/entry_articles.jsonl", "data/article_drafts.jsonl", "data/comments.jsonl",
			"data/identities.jsonl", "data/media.jsonl":
		default:
			if !strings.HasPrefix(file.Name, "media/") {
				return archiveInvalid("unknown account archive member", nil)
			}
		}
		files[file.Name] = file
	}
	for _, name := range []string{
		"README.txt", "manifest.json", "checksums.sha256", "data/account.json", "data/entries.jsonl",
		"data/entry_articles.jsonl", "data/article_drafts.jsonl", "data/comments.jsonl",
		"data/identities.jsonl", "data/media.jsonl",
	} {
		if files[name] == nil {
			return archiveInvalid("account archive member missing", nil)
		}
	}
	raw, err := readSmallZIPMember(ctx, files["manifest.json"], 1<<20)
	if err != nil {
		return err
	}
	var document accountManifest
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	if document.Format != accountFormatName || (document.FormatVersion != 1 && document.FormatVersion != 2 && document.FormatVersion != 3) || document.AccountID == "" {
		return archiveInvalid("not a personal account export", nil)
	}
	sums, err := readChecksums(ctx, files["checksums.sha256"], len(files))
	if err != nil {
		return err
	}
	if len(sums) != len(files)-1 {
		return archiveInvalid("account checksums are incomplete", nil)
	}
	for name, file := range files {
		if name == "checksums.sha256" {
			continue
		}
		expected, ok := sums[name]
		if !ok {
			return archiveInvalid("account member checksum missing", nil)
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		digest := sha256.New()
		count, err := io.Copy(digest, contextReader{ctx: ctx, reader: source})
		closeErr := source.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if count != expected.size || hex.EncodeToString(digest.Sum(nil)) != expected.sha256 {
			return fmt.Errorf("account member checksum mismatch: %s", name)
		}
	}
	return nil
}
