package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	entryID       = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	mediaOneID    = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	mediaTwoID    = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	replyID       = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	rootCommentID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	orphanMediaID = "01ARZ3NDEKTSV4RRFFQ69G5FB0"
)

func TestExportV1FullDataChecksumsOrderingAndExclusions(t *testing.T) {
	fixture := newExportFixture(t, true)
	var first bytes.Buffer
	summary, err := fixture.exporter.Export(context.Background(), &first)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FormatVersion != 1 || summary.ArchiveBytes != int64(first.Len()) || summary.ArchiveSHA256 != shaHex(first.Bytes()) {
		t.Fatalf("summary = %#v", summary)
	}
	members, order := readArchive(t, first.Bytes())
	wantOrder := []string{
		"README.txt", "data/site.json", "data/entries.jsonl", "data/comments.jsonl", "data/media.jsonl",
	}
	blobs := []string{"media/" + fixture.mediaOneSHA + ".jpg", "media/" + fixture.mediaTwoSHA + ".png"}
	sort.Strings(blobs)
	wantOrder = append(wantOrder, blobs...)
	wantOrder = append(wantOrder, "manifest.json", "checksums.sha256")
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("member order = %#v", order)
	}
	for _, forbidden := range []string{"secrets.json", "moment.db", "content_text", "unreferenced_since_ms", "preview"} {
		for name, body := range members {
			if strings.Contains(name, forbidden) || bytes.Contains(body, []byte(forbidden)) {
				t.Fatalf("forbidden %q leaked through %s", forbidden, name)
			}
		}
	}

	var document manifest
	if err := json.Unmarshal(members["manifest.json"], &document); err != nil {
		t.Fatal(err)
	}
	if document.Format != formatName || document.FormatVersion != 1 || document.Source.InstanceID != fixture.runtime.Instance.ID || len(document.Source.InstanceID) != 32 {
		t.Fatalf("manifest identity = %#v", document)
	}
	if document.Counts != (countManifest{Entries: 1, Comments: 2, Media: 2}) {
		t.Fatalf("counts = %#v", document.Counts)
	}
	checksums, expanded := parseChecksums(t, members)
	if document.Limits.ExpandedBytes != expanded {
		t.Fatalf("expanded_bytes=%d want %d", document.Limits.ExpandedBytes, expanded)
	}
	if len(checksums) != len(members)-1 {
		t.Fatalf("checksum count=%d members=%d", len(checksums), len(members))
	}

	entryLines := nonemptyLines(members["data/entries.jsonl"])
	if len(entryLines) != 1 {
		t.Fatalf("entry lines = %d", len(entryLines))
	}
	var entry entryRecord
	if err := json.Unmarshal(entryLines[0], &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ContentMD != "Unicode 世界" || len(entry.Media) != 3 || entry.Media[0].AltText != nil || entry.Media[1].AltText == nil || *entry.Media[1].AltText != "" || entry.Media[2].AltText == nil || *entry.Media[2].AltText != "街景" {
		t.Fatalf("entry alt tri-state = %#v", entry)
	}
	commentLines := nonemptyLines(members["data/comments.jsonl"])
	if len(commentLines) != 2 {
		t.Fatalf("comment lines = %d", len(commentLines))
	}
	var root, reply commentRecord
	if json.Unmarshal(commentLines[0], &root) != nil || json.Unmarshal(commentLines[1], &reply) != nil {
		t.Fatal("decode comments")
	}
	if root.ID != rootCommentID || reply.ID != replyID || reply.ParentID == nil || *reply.ParentID != rootCommentID {
		t.Fatalf("comment order = %#v then %#v", root, reply)
	}

	var second bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("same snapshot and clock did not produce deterministic archive bytes")
	}
}

func TestExportV1EmptyArchiveAndManifestFixedPointBoundaries(t *testing.T) {
	fixture := newExportFixture(t, false)
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	members, order := readArchive(t, output.Bytes())
	if len(order) != 7 || len(members["data/entries.jsonl"]) != 0 || len(members["data/comments.jsonl"]) != 0 || len(members["data/media.jsonl"]) != 0 {
		t.Fatalf("empty archive = %#v", order)
	}
	parseChecksums(t, members)

	for _, boundary := range []int64{10, 100} {
		other := boundary - int64(len(strconv.FormatInt(boundary-1, 10)))
		data, err := fixedPointBytes(other, func(value int64) ([]byte, error) {
			return []byte(strconv.FormatInt(value, 10)), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		value, _ := strconv.ParseInt(string(data), 10, 64)
		if value != other+int64(len(data)) || value < boundary {
			t.Fatalf("fixed point around %d = %d (%q)", boundary, value, data)
		}
	}
}

func TestExportSnapshotRemainsBeforeConcurrentCommit(t *testing.T) {
	fixture := newExportFixture(t, true)
	fixture.exporter.afterSnapshot = func() {
		if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
			_, err := queries.ExecContext(context.Background(), `UPDATE entries SET content_md = 'after', updated_at_ms = updated_at_ms + 1 WHERE id = ?`, entryID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	members, _ := readArchive(t, output.Bytes())
	if !bytes.Contains(members["data/entries.jsonl"], []byte("Unicode")) || bytes.Contains(members["data/entries.jsonl"], []byte("after")) {
		t.Fatalf("archive was torn by concurrent commit: %s", members["data/entries.jsonl"])
	}
	var live string
	if err := fixture.database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT content_md FROM entries WHERE id = ?`, entryID).Scan(&live)
	}); err != nil || live != "after" {
		t.Fatalf("live mutation = %q, %v", live, err)
	}
}

func TestExportHoldsMaintenanceAndMediaLocksThroughBlobCopy(t *testing.T) {
	fixture := newExportFixture(t, true)
	reached := make(chan struct{})
	release := make(chan struct{})
	fixture.exporter.afterSnapshot = func() { close(reached); <-release }
	result := make(chan error, 1)
	go func() { _, err := fixture.exporter.Export(context.Background(), io.Discard); result <- err }()
	<-reached
	for _, request := range []runtimefs.LockRequest{
		{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		_, err := fixture.runtime.Locks.Acquire(ctx, request.Name, request.Mode)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("exclusive %s acquired during export: %v", request.Name, err)
		}
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.runtime.Locks.Acquire(context.Background(), runtimefs.MediaLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestExportFailureCancellationLargeStreamingAndAtomicFileSafety(t *testing.T) {
	fixture := newExportFixture(t, true)
	original := filepath.Join(fixture.runtime.Current.Root, filepath.FromSlash(fixture.mediaOnePath))
	if err := os.WriteFile(original, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.exporter.Export(context.Background(), io.Discard); !domain.HasCode(err, domain.CodeExportInvalid) && !domain.HasCode(err, domain.CodeExportChecksumMismatch) {
		t.Fatalf("corrupt original error = %v", err)
	}
	if err := os.WriteFile(original, fixture.mediaOneBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(destination, []byte("owner data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.exporter.ExportFile(context.Background(), destination, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("no-clobber = %v", err)
	}
	if data, _ := os.ReadFile(destination); string(data) != "owner data" {
		t.Fatal("no-clobber changed existing destination")
	}
	if _, err := fixture.exporter.ExportFile(context.Background(), destination, true); err != nil {
		t.Fatal(err)
	}
	if _, err := zip.OpenReader(destination); err != nil {
		t.Fatalf("overwrite did not publish a ZIP: %v", err)
	}

	cancelled := filepath.Join(filepath.Dir(destination), "cancelled.zip")
	ctx, cancel := context.WithCancel(context.Background())
	err := func() error {
		_, err := WriteFile(ctx, cancelled, false, func(ctx context.Context, _ io.Writer) (Summary, error) {
			cancel()
			return Summary{}, ctx.Err()
		})
		return err
	}()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := os.Stat(cancelled); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel left final destination")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".cancelled.zip.partial.*"))
	if len(matches) != 0 {
		t.Fatalf("cancel left partials: %#v", matches)
	}

	large := bytes.Repeat([]byte("x"), 5<<20)
	limited := &maxWriteRecorder{max: 1 << 20}
	_, _ = WriteFile(context.Background(), filepath.Join(filepath.Dir(destination), "stream.zip"), false, func(_ context.Context, writer io.Writer) (Summary, error) {
		zipWriter := zip.NewWriter(io.MultiWriter(writer, limited))
		member, _ := zipWriter.Create("large.bin")
		_, err := io.CopyBuffer(member, bytes.NewReader(large), make([]byte, 128<<10))
		if closeErr := zipWriter.Close(); err == nil {
			err = closeErr
		}
		return Summary{FormatVersion: 1}, err
	})
	// ValidateFile correctly rejects this non-protocol fixture; the writer
	// bound is the independent property under test.
	if limited.tooLarge {
		t.Fatalf("stream issued a write larger than %d", limited.max)
	}
}

func TestExportRejectsCorruptAuthorityBeforeSuccessfulArchive(t *testing.T) {
	fixture := newExportFixture(t, false)
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `UPDATE site_settings SET timezone = 'Local' WHERE id = 1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.exporter.Export(context.Background(), io.Discard); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("corrupt site error = %v", err)
	}
}

func TestExportCancellationDuringJSONLRetainsCancellationIdentity(t *testing.T) {
	fixture := newExportFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.exporter.recordYield = func(kind string, _ int64) {
		if kind == "entry" {
			cancel()
		}
	}
	_, err := fixture.exporter.Export(ctx, io.Discard)
	if !errors.Is(err, context.Canceled) || !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("JSONL cancellation identity = %v", err)
	}
}

func TestValidateFileRejectsCanonicalChecksumCoveredRecordCorruption(t *testing.T) {
	fixture := newExportFixture(t, true)
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string][]byte)
	}{
		{
			name: "invalid JSONL",
			mutate: func(members map[string][]byte) {
				members["data/entries.jsonl"][0] = '['
			},
		},
		{
			name: "manifest count mismatch",
			mutate: func(members map[string][]byte) {
				members["manifest.json"] = bytes.Replace(members["manifest.json"], []byte(`"entries":1`), []byte(`"entries":2`), 1)
			},
		},
		{
			name: "media path mismatch",
			mutate: func(members map[string][]byte) {
				members["data/media.jsonl"] = bytes.Replace(members["data/media.jsonl"], []byte(`"archive_path":"media/`), []byte(`"archive_path":"medix/`), 1)
			},
		},
		{
			name: "entry media is null",
			mutate: func(members map[string][]byte) {
				var value map[string]any
				_ = json.Unmarshal(nonemptyLines(members["data/entries.jsonl"])[0], &value)
				value["media"] = nil
				members["data/entries.jsonl"] = append(mustMarshalTest(t, value), '\n')
			},
		},
		{
			name: "entry usage refers to absent media",
			mutate: func(members map[string][]byte) {
				members["data/entries.jsonl"] = bytes.Replace(members["data/entries.jsonl"], []byte(mediaOneID), []byte(orphanMediaID), 1)
			},
		},
		{
			name: "duplicate entry breaks strict order",
			mutate: func(members map[string][]byte) {
				members["data/entries.jsonl"] = append(append([]byte(nil), members["data/entries.jsonl"]...), members["data/entries.jsonl"]...)
				setManifestCount(t, members, "entries", 2)
			},
		},
		{
			name: "comments out of thread order",
			mutate: func(members map[string][]byte) {
				lines := nonemptyLines(members["data/comments.jsonl"])
				members["data/comments.jsonl"] = append(append(append([]byte(nil), lines[1]...), '\n'), append(lines[0], '\n')...)
			},
		},
		{
			name: "reply parent absent",
			mutate: func(members map[string][]byte) {
				lines := nonemptyLines(members["data/comments.jsonl"])
				var reply map[string]any
				_ = json.Unmarshal(lines[1], &reply)
				reply["parent_id"] = orphanMediaID
				members["data/comments.jsonl"] = append(append(append([]byte(nil), lines[0]...), '\n'), append(mustMarshalTest(t, reply), '\n')...)
			},
		},
		{
			name: "media records out of hash order",
			mutate: func(members map[string][]byte) {
				lines := nonemptyLines(members["data/media.jsonl"])
				members["data/media.jsonl"] = append(append(append([]byte(nil), lines[1]...), '\n'), append(lines[0], '\n')...)
			},
		},
		{
			name: "site avatar absent",
			mutate: func(members map[string][]byte) {
				var site map[string]any
				_ = json.Unmarshal(members["data/site.json"], &site)
				site["avatar_media_id"] = orphanMediaID
				members["data/site.json"] = append(mustMarshalTest(t, site), '\n')
			},
		},
		{
			name: "duplicate JSON field",
			mutate: func(members map[string][]byte) {
				members["data/site.json"] = bytes.Replace(members["data/site.json"], []byte(`"title":`), []byte(`"title":"duplicate","title":`), 1)
			},
		},
		{
			name: "missing required JSON field",
			mutate: func(members map[string][]byte) {
				var site map[string]any
				_ = json.Unmarshal(members["data/site.json"], &site)
				delete(site, "bio")
				members["data/site.json"] = append(mustMarshalTest(t, site), '\n')
			},
		},
		{
			name: "media bytes replaced with same size",
			mutate: func(members map[string][]byte) {
				for name, body := range members {
					if strings.HasPrefix(name, "media/") && strings.HasSuffix(name, ".jpg") {
						replaced := append([]byte(nil), body...)
						replaced[len(replaced)-1] ^= 0xff
						members[name] = replaced
						return
					}
				}
			},
		},
		{
			name: "media dimensions mismatch",
			mutate: func(members map[string][]byte) {
				members["data/media.jsonl"] = bytes.Replace(members["data/media.jsonl"], []byte(`"width":32`), []byte(`"width":31`), 1)
			},
		},
		{
			name: "media byte size mismatch",
			mutate: func(members map[string][]byte) {
				lines := nonemptyLines(members["data/media.jsonl"])
				var value map[string]any
				_ = json.Unmarshal(lines[0], &value)
				value["byte_size"] = value["byte_size"].(float64) + 1
				members["data/media.jsonl"] = append(append(mustMarshalTest(t, value), '\n'), append(lines[1], '\n')...)
			},
		},
		{
			name: "README zip bomb budget",
			mutate: func(members map[string][]byte) {
				members["README.txt"] = bytes.Repeat([]byte("x"), 1<<20)
			},
		},
		{
			name: "entry sub-millisecond timestamp",
			mutate: func(members map[string][]byte) {
				members["data/entries.jsonl"] = bytes.Replace(members["data/entries.jsonl"], []byte(".123Z"), []byte(".123456Z"), 1)
			},
		},
		{
			name: "comment sub-millisecond timestamp",
			mutate: func(members map[string][]byte) {
				members["data/comments.jsonl"] = bytes.Replace(members["data/comments.jsonl"], []byte(".123Z"), []byte(".123456Z"), 1)
			},
		},
		{
			name: "media sub-millisecond timestamp",
			mutate: func(members map[string][]byte) {
				members["data/media.jsonl"] = bytes.Replace(members["data/media.jsonl"], []byte(".123Z"), []byte(".123456Z"), 1)
			},
		},
		{
			name: "manifest sub-millisecond timestamp",
			mutate: func(members map[string][]byte) {
				members["manifest.json"] = bytes.Replace(members["manifest.json"], []byte(".123Z"), []byte(".123456Z"), 1)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			members, order := readArchive(t, output.Bytes())
			test.mutate(members)
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			name := filepath.Join(t.TempDir(), "tampered.zip")
			writeArchive(t, name, members, order)
			if err := ValidateFile(name); !domain.HasCode(err, domain.CodeExportInvalid) {
				t.Fatalf("tampered archive validation = %v", err)
			}
		})
	}
}

func TestValidateFileRejectsMediaMIMESpoofWithRecomputedChecksums(t *testing.T) {
	fixture := newExportFixture(t, true)
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	members, order := readArchive(t, output.Bytes())
	lines := nonemptyLines(members["data/media.jsonl"])
	for index, line := range lines {
		var value mediaRecord
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value.MIMEType != "image/jpeg" {
			continue
		}
		oldPath := value.ArchivePath
		value.MIMEType = "image/png"
		value.ArchivePath = strings.TrimSuffix(oldPath, ".jpg") + ".png"
		lines[index] = mustMarshalTest(t, value)
		members[value.ArchivePath] = members[oldPath]
		delete(members, oldPath)
		break
	}
	var mediaJSON []byte
	for _, line := range lines {
		mediaJSON = append(mediaJSON, line...)
		mediaJSON = append(mediaJSON, '\n')
	}
	members["data/media.jsonl"] = mediaJSON
	mediaNames := make([]string, 0, 2)
	for name := range members {
		if strings.HasPrefix(name, "media/") {
			mediaNames = append(mediaNames, name)
		}
	}
	sort.Strings(mediaNames)
	order = append(append([]string(nil), order[:5]...), append(mediaNames, "manifest.json", "checksums.sha256")...)
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "mime-spoof.zip")
	writeArchive(t, name, members, order)
	if err := ValidateFile(name); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("MIME-spoofed original = %v", err)
	}
}

func TestValidateFileIgnoresUnknownFieldsAndBoundsUntrustedCounts(t *testing.T) {
	fixture := newExportFixture(t, true)
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	members, order := readArchive(t, output.Bytes())
	var site map[string]any
	_ = json.Unmarshal(members["data/site.json"], &site)
	site["future_extension"] = map[string]any{"enabled": true}
	members["data/site.json"] = append(mustMarshalTest(t, site), '\n')
	var top map[string]json.RawMessage
	_ = json.Unmarshal(members["manifest.json"], &top)
	top["future_manifest"] = json.RawMessage(`{"version":2}`)
	members["manifest.json"] = append(mustMarshalTest(t, top), '\n')
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "extended.zip")
	writeArchive(t, name, members, order)
	if err := ValidateFile(name); err != nil {
		t.Fatalf("unknown v1 extensions were not ignored: %v", err)
	}

	hugeMembers, hugeOrder := readArchive(t, output.Bytes())
	for _, field := range []string{"entries", "comments", "media"} {
		setManifestCount(t, hugeMembers, field, int64(^uint64(0)>>1))
	}
	rebuildManifestExpanded(t, hugeMembers)
	rebuildChecksums(hugeMembers)
	hugeName := filepath.Join(t.TempDir(), "huge-counts.zip")
	writeArchive(t, hugeName, hugeMembers, hugeOrder)
	if err := ValidateFile(hugeName); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("untrusted huge counts = %v", err)
	}
}

func TestValidateFileRequiresLosslessCanonicalPublicBaseURL(t *testing.T) {
	fixture := newExportFixture(t, true)
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, publicURL := range []string{
		"https://example.com/%2F",
		"https://example.com:99999",
		"https://example.com:443",
		"HTTPS://EXAMPLE.COM/a/../b/",
	} {
		t.Run(publicURL, func(t *testing.T) {
			members, order := readArchive(t, output.Bytes())
			var site map[string]any
			_ = json.Unmarshal(members["data/site.json"], &site)
			site["public_base_url"] = publicURL
			members["data/site.json"] = append(mustMarshalTest(t, site), '\n')
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			name := filepath.Join(t.TempDir(), "bad-url.zip")
			writeArchive(t, name, members, order)
			if err := ValidateFile(name); !domain.HasCode(err, domain.CodeExportInvalid) {
				t.Fatalf("noncanonical URL %q = %v", publicURL, err)
			}
		})
	}
}

func TestValidateFileContextCancelsDuringLargeJSONLAndCleansPreparedOutput(t *testing.T) {
	fixture := newExportFixture(t, true)
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	members, order := readArchive(t, output.Bytes())
	var template entryRecord
	if err := json.Unmarshal(nonemptyLines(members["data/entries.jsonl"])[0], &template); err != nil {
		t.Fatal(err)
	}
	var entries bytes.Buffer
	entries.Write(members["data/entries.jsonl"])
	entropy := mathrand.New(mathrand.NewSource(24))
	base := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	for index := 0; index < 2_000; index++ {
		id, err := domain.NewID(base.Add(time.Duration(index)*time.Millisecond), entropy)
		if err != nil {
			t.Fatal(err)
		}
		value := template
		value.ID = id.String()
		entries.Write(mustMarshalTest(t, value))
		entries.WriteByte('\n')
	}
	members["data/entries.jsonl"] = entries.Bytes()
	setManifestCount(t, members, "entries", 2_001)
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	archivePath := filepath.Join(t.TempDir(), "large.zip")
	writeArchive(t, archivePath, members, order)
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := newDeterministicCancelContext(20)
	directory := t.TempDir()
	destination := filepath.Join(directory, "cancelled.zip")
	_, err = WriteFile(ctx, destination, false, func(_ context.Context, writer io.Writer) (Summary, error) {
		_, err := io.Copy(writer, bytes.NewReader(archiveBytes))
		return Summary{FormatVersion: FormatVersion}, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validator cancellation = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled validator left a final file")
	}
	partials, _ := filepath.Glob(filepath.Join(directory, ".cancelled.zip.partial.*"))
	if len(partials) != 0 {
		t.Fatalf("cancelled validator left partials: %#v", partials)
	}
}

func TestExportRejectsMediaMetadataOutsidePortableLimits(t *testing.T) {
	fixture := newExportFixture(t, true)
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `UPDATE media SET width = 20000 WHERE id = ?`, mediaOneID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.exporter.Export(context.Background(), io.Discard); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("oversized media metadata error = %v", err)
	}
}

func TestExportLargeOriginalUsesBoundedStreamingWrites(t *testing.T) {
	fixture := newExportFixture(t, true)
	large := append(encodedTestImage(t, "jpeg"), bytes.Repeat([]byte{0}, 5<<20)...)
	largeID := "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	_, _ = insertArchiveMedia(t, fixture.database, fixture.runtime, largeID, large, "image/jpeg", "large.jpg", false, time.Now().UTC())
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 3, NULL)`, entryID, largeID); err != nil {
			return err
		}
		_, err := queries.ExecContext(context.Background(), `UPDATE media SET unreferenced_since_ms = NULL WHERE id = ?`, largeID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	limited := &maxWriteRecorder{max: 256 << 10}
	summary, err := fixture.exporter.Export(context.Background(), limited)
	if err != nil {
		t.Fatal(err)
	}
	if limited.tooLarge || summary.ArchiveBytes < int64(len(large)) {
		t.Fatalf("large stream bytes=%d max-write-violated=%t", summary.ArchiveBytes, limited.tooLarge)
	}
}

func TestExportRejectsNonCanonicalInstanceIdentityBeforeFirstByte(t *testing.T) {
	fixture := newExportFixture(t, false)
	instance := fixture.runtime.Instance
	instance.ID = strings.ToUpper(instance.ID)
	data, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.runtime.Root, "instance.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("noncanonical instance identity = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("source validation wrote %d bytes before failing", output.Len())
	}
}

type exportFixture struct {
	exporter                               *Exporter
	runtime                                *runtimefs.Runtime
	database                               *sqlitestore.Database
	mediaOneBytes                          []byte
	mediaOnePath, mediaOneSHA, mediaTwoSHA string
}

func newExportFixture(t *testing.T, full bool) exportFixture {
	t.Helper()
	ctx := context.Background()
	runtimeState, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	clock := time.Date(2026, 8, 10, 12, 0, 0, 123_000_000, time.UTC)
	exporter, err := NewExporter(Options{DataDir: runtimeState.Root, AppVersion: "0.1.0", Clock: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	fixture := exportFixture{exporter: exporter, runtime: runtimeState, database: database}
	if !full {
		return fixture
	}
	one := encodedTestImage(t, "jpeg")
	two := encodedTestImage(t, "png")
	orphan := []byte("unreferenced bytes")
	onePath, oneSHA := insertArchiveMedia(t, database, runtimeState, mediaOneID, one, "image/jpeg", "相片.jpg", false, clock)
	_, twoSHA := insertArchiveMedia(t, database, runtimeState, mediaTwoID, two, "image/png", "street.png", false, clock)
	insertArchiveMedia(t, database, runtimeState, orphanMediaID, orphan, "image/gif", "orphan.gif", true, clock)
	fixture.mediaOneBytes, fixture.mediaOnePath, fixture.mediaOneSHA, fixture.mediaTwoSHA = one, onePath, oneSHA, twoSHA
	if err := database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms) VALUES (?, 'moment', ?, ?, ?, ?)`, []any{entryID, "Unicode 世界", clock.UnixMilli(), clock.UnixMilli(), clock.UnixMilli()}},
			{`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, []any{entryID, mediaOneID}},
			{`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 1, '')`, []any{entryID, mediaOneID}},
			{`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 2, '街景')`, []any{entryID, mediaTwoID}},
			{`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'guest', '访客', 'root', ?)`, []any{rootCommentID, entryID, clock.UnixMilli()}},
			{`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'owner', 'Owner', 'reply', ?)`, []any{replyID, entryID, rootCommentID, clock.UnixMilli()}},
			{`UPDATE site_settings SET title = '我的 Moment', bio = 'portable', timezone = 'Asia/Shanghai', public_base_url = 'http://localhost:8080', avatar_media_id = ?, comments_enabled = 1 WHERE id = 1`, []any{mediaOneID}},
			{`UPDATE media SET unreferenced_since_ms = NULL WHERE id IN (?, ?)`, []any{mediaOneID, mediaTwoID}},
		}
		for _, statement := range statements {
			if _, err := queries.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeState.Root, "secrets.json"), []byte("must-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview := filepath.Join(runtimeState.Root, "cache", "previews", "recipe", "preview.jpg")
	if err := os.MkdirAll(filepath.Dir(preview), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preview, []byte("preview-must-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func insertArchiveMedia(t *testing.T, database *sqlitestore.Database, runtimeState *runtimefs.Runtime, id string, body []byte, mimeType, originalName string, orphan bool, now time.Time) (string, string) {
	t.Helper()
	sha := shaHex(body)
	ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif"}[mimeType]
	storage := "media/original/" + sha[:2] + "/" + sha + "." + ext
	full := filepath.Join(runtimeState.Current.Root, filepath.FromSlash(storage))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var unreferenced any
	if orphan {
		unreferenced = now.UnixMilli()
	}
	if err := database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, ?, ?, ?, 32, 24, ?, ?)`, id, sha, storage, originalName, mimeType, len(body), now.UnixMilli(), unreferenced)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return storage, sha
}

func readArchive(t *testing.T, data []byte) (map[string][]byte, []string) {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	members := make(map[string][]byte, len(reader.File))
	order := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		body, err := func() ([]byte, error) {
			stream, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = stream.Close() }()
			return io.ReadAll(stream)
		}()
		if err != nil {
			t.Fatal(err)
		}
		members[file.Name] = body
		order = append(order, file.Name)
	}
	return members, order
}

func parseChecksums(t *testing.T, members map[string][]byte) (map[string]memberDigest, int64) {
	t.Helper()
	parsed := make(map[string]memberDigest)
	var paths []string
	var expanded int64
	for _, line := range nonemptyLines(members["checksums.sha256"]) {
		fields := strings.Split(string(line), "\t")
		if len(fields) != 3 {
			t.Fatalf("checksum line = %q", line)
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		body, exists := members[fields[2]]
		if !exists || fields[2] == "checksums.sha256" {
			t.Fatalf("checksum target %q", fields[2])
		}
		if fields[0] != shaHex(body) || size != int64(len(body)) {
			t.Fatalf("checksum mismatch for %s", fields[2])
		}
		parsed[fields[2]] = memberDigest{path: fields[2], sha256: fields[0], size: size}
		paths = append(paths, fields[2])
		expanded += size
	}
	if !sort.StringsAreSorted(paths) {
		t.Fatalf("checksums unordered: %#v", paths)
	}
	return parsed, expanded
}

func nonemptyLines(data []byte) [][]byte {
	var result [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) > 0 {
			result = append(result, line)
		}
	}
	return result
}

func shaHex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func rebuildChecksums(members map[string][]byte) {
	paths := make([]string, 0, len(members)-1)
	for name := range members {
		if name != "checksums.sha256" {
			paths = append(paths, name)
		}
	}
	sort.Strings(paths)
	var output strings.Builder
	for _, name := range paths {
		fmt.Fprintf(&output, "%s\t%d\t%s\n", shaHex(members[name]), len(members[name]), name)
	}
	members["checksums.sha256"] = []byte(output.String())
}

func rebuildManifestExpanded(t *testing.T, members map[string][]byte) {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(members["manifest.json"], &document); err != nil {
		t.Fatal(err)
	}
	var limits map[string]json.RawMessage
	if err := json.Unmarshal(document["limits"], &limits); err != nil {
		t.Fatal(err)
	}
	var other int64
	for name, body := range members {
		if name != "manifest.json" && name != "checksums.sha256" {
			other += int64(len(body))
		}
	}
	expanded := other
	for attempt := 0; attempt < manifestFixedPointLimit; attempt++ {
		limits["expanded_bytes"] = json.RawMessage(strconv.FormatInt(expanded, 10))
		document["limits"] = mustMarshalTest(t, limits)
		manifestData := append(mustMarshalTest(t, document), '\n')
		next := other + int64(len(manifestData))
		members["manifest.json"] = manifestData
		if next == expanded {
			return
		}
		expanded = next
	}
	t.Fatal("test manifest fixed point did not converge")
}

func setManifestCount(t *testing.T, members map[string][]byte, field string, value int64) {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(members["manifest.json"], &document); err != nil {
		t.Fatal(err)
	}
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(document["counts"], &counts); err != nil {
		t.Fatal(err)
	}
	counts[field] = json.RawMessage(strconv.FormatInt(value, 10))
	document["counts"] = mustMarshalTest(t, counts)
	members["manifest.json"] = append(mustMarshalTest(t, document), '\n')
}

func mustMarshalTest(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeArchive(t *testing.T, name string, members map[string][]byte, order []string) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	archiveWriter := zip.NewWriter(file)
	for _, memberName := range order {
		member, err := archiveWriter.Create(memberName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(members[memberName]); err != nil {
			t.Fatal(err)
		}
	}
	if err := archiveWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func encodedTestImage(t *testing.T, format string) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			value.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 9), B: uint8(x + y), A: 0xff})
		}
	}
	var output bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&output, value, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&output, value)
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type maxWriteRecorder struct {
	max      int
	tooLarge bool
}

type deterministicCancelContext struct {
	context.Context
	remaining atomic.Int64
	done      chan struct{}
	once      sync.Once
}

func newDeterministicCancelContext(checks int64) *deterministicCancelContext {
	ctx := &deterministicCancelContext{Context: context.Background(), done: make(chan struct{})}
	ctx.remaining.Store(checks)
	return ctx
}

func (ctx *deterministicCancelContext) Done() <-chan struct{} { return ctx.done }

func (ctx *deterministicCancelContext) Err() error {
	if ctx.remaining.Add(-1) > 0 {
		return nil
	}
	ctx.once.Do(func() { close(ctx.done) })
	return context.Canceled
}

func (writer *maxWriteRecorder) Write(data []byte) (int, error) {
	if len(data) > writer.max {
		writer.tooLarge = true
		return 0, fmt.Errorf("write too large: %d", len(data))
	}
	return len(data), nil
}
