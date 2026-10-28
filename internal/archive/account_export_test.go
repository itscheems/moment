package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestAccountExportValidationRejectsOversizedMemberBeforeReading(t *testing.T) {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	if _, err := writer.CreateRaw(&zip.FileHeader{Name: "data/account.json", Method: zip.Store, UncompressedSize64: uint64(defaultMaxExpandedBytes) + 1}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "oversized.zip")
	if err := os.WriteFile(file, output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccountFileContext(context.Background(), file); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("oversized member was not rejected before reading: %v", err)
	}
}

func TestAccountExportSelectsOnlyOwnRecordsAndMedia(t *testing.T) {
	fixture := newExportFixture(t, true)
	insertAccountsFixture(t, fixture)
	ctx := context.Background()
	if err := fixture.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(ctx, `UPDATE users SET avatar_media_id = ? WHERE id = ?`, mediaTwoID, memberAccountID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	summary, err := fixture.exporter.ExportAccount(ctx, &output, memberAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ArchiveBytes != int64(output.Len()) {
		t.Fatal("wrong byte count")
	}
	members, _ := readArchive(t, output.Bytes())
	if bytes.Contains(members["manifest.json"], []byte(`"format":"moment-export"`)) || !bytes.Contains(members["manifest.json"], []byte(`"format":"moment-account-export"`)) {
		t.Fatal("wrong format")
	}
	if len(nonemptyLines(members["data/entries.jsonl"])) != 0 || len(nonemptyLines(members["data/comments.jsonl"])) != 0 || len(nonemptyLines(members["data/identities.jsonl"])) != 2 {
		t.Fatal("account scope leaked or lost records")
	}
	if members["data/site.json"] != nil || members["data/users.jsonl"] != nil || members["media/"+fixture.mediaOneSHA+".jpg"] != nil {
		t.Fatal("another account's data leaked")
	}
	if len(members["media/"+fixture.mediaTwoSHA+".png"]) == 0 {
		t.Fatal("own avatar missing")
	}
	for name, body := range members {
		if bytes.Contains(body, []byte(pendingAccountID)) || bytes.Contains(body, []byte("must-not-leak")) {
			t.Fatalf("leak in %s", name)
		}
	}
	file := filepath.Join(t.TempDir(), "account.zip")
	if err := os.WriteFile(file, output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccountFileContext(ctx, file); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFileContext(ctx, file); err == nil {
		t.Fatal("personal export accepted as site backup")
	}
	corrupted := append([]byte(nil), output.Bytes()...)
	offset := bytes.Index(corrupted, members["media/"+fixture.mediaTwoSHA+".png"])
	if offset < 0 {
		t.Fatal("stored media not found")
	}
	corrupted[offset+10] ^= 0xff
	if err := os.WriteFile(file, corrupted, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccountFileContext(ctx, file); err == nil {
		t.Fatal("corruption accepted")
	}
}

func TestAccountExportUnknownCancellationAndAtomicDestination(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertAccountsFixture(t, fixture)
	var output bytes.Buffer
	if _, err := fixture.exporter.ExportAccount(context.Background(), &output, "missing"); err == nil || output.Len() != 0 {
		t.Fatal("missing account exported")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.exporter.ExportAccount(ctx, &output, memberAccountID); err == nil {
		t.Fatal("cancelled export succeeded")
	}
	destination := filepath.Join(t.TempDir(), "mine.zip")
	producer := func(ctx context.Context, w io.Writer) (Summary, error) {
		return fixture.exporter.ExportAccount(ctx, w, memberAccountID)
	}
	if _, err := WriteAccountFile(context.Background(), destination, false, producer); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(destination)
	if _, err := WriteAccountFile(context.Background(), destination, false, producer); err == nil {
		t.Fatal("overwrote existing destination")
	}
	after, _ := os.ReadFile(destination)
	if !bytes.Equal(before, after) {
		t.Fatal("changed existing destination")
	}
}
