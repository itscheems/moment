package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestCommentQuotesRoundTripWithMediaAndAccountExport(t *testing.T) {
	ctx := context.Background()
	fixture := newExportFixture(t, true)
	quoteID := "01H00000000000000000000100"
	if err := fixture.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		if _, err := q.ExecContext(ctx, `INSERT INTO entries(id,kind,content_md,author_user_id,created_at_ms,updated_at_ms,published_at_ms) VALUES (?,'moment','',?,1,1,1)`, quoteID, fixtureAccountID); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `INSERT INTO entry_quotes(entry_id,quoted_comment_id) VALUES (?,?)`, quoteID, rootCommentID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	data := exportFixtureBytes(t, fixture)
	members, _ := readArchive(t, data)
	if !bytes.Contains(members["data/entries.jsonl"], []byte(`"quoted_comment_id":"`+rootCommentID+`"`)) {
		t.Fatal("comment target omitted")
	}
	var account bytes.Buffer
	if summary, err := fixture.exporter.ExportAccount(ctx, &account, fixtureAccountID); err != nil || summary.FormatVersion != 2 {
		t.Fatalf("account export=%+v %v", summary, err)
	}
	personal, _ := readArchive(t, account.Bytes())
	if !bytes.Contains(personal["data/entries.jsonl"], []byte(`"quoted_comment_id":"`+rootCommentID+`"`)) {
		t.Fatal("personal export lost target")
	}
	database := stageArchive(t, fixture, writeArchiveBytes(t, data))
	snapshot := stagedSnapshot(t, database)
	if err := snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
		if value.ID == quoteID && (value.QuotedCommentID == nil || *value.QuotedCommentID != rootCommentID || value.QuoteUnavailable) {
			return fmt.Errorf("restored quote=%+v", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
		if value.ID == rootCommentID && len(value.Media) != 2 {
			return fmt.Errorf("lost quoted media: %+v", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCommentQuoteFormatCompatibility(t *testing.T) {
	for _, version := range []int{2, 3} {
		for _, quoted := range []bool{false, true} {
			t.Run(fmt.Sprintf("v%d-quote-%t", version, quoted), func(t *testing.T) {
				fixture := newExportFixture(t, true)
				members, order := readArchive(t, exportFixtureBytes(t, fixture))
				var manifest map[string]any
				if err := json.Unmarshal(members["manifest.json"], &manifest); err != nil {
					t.Fatal(err)
				}
				manifest["format_version"] = version
				members["manifest.json"] = append(mustMarshalTest(t, manifest), '\n')
				members["README.txt"] = []byte(strings.Replace(readme, "v4", fmt.Sprintf("v%d", version), 1))
				if quoted {
					members["data/entries.jsonl"] = rewriteEntryLine(t, members["data/entries.jsonl"], entryID, func(line map[string]json.RawMessage) {
						line["quoted_comment_id"] = json.RawMessage(`"` + rootCommentID + `"`)
					})
				}
				rebuildManifestExpanded(t, members)
				rebuildChecksums(members)
				path := filepath.Join(t.TempDir(), "legacy.zip")
				writeArchive(t, path, members, order)
				err := ValidateFileContext(context.Background(), path)
				if quoted {
					if err == nil {
						t.Fatal("legacy version accepted new relation")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					stageArchive(t, fixture, path)
				}
			})
		}
	}
}

func TestMergeCommentQuotesRemapIncomingTargetsAndRejectSkippedComments(t *testing.T) {
	postID, commentID, quoteID := "01H00000000000000000000101", "01H00000000000000000000102", "01H00000000000000000000103"
	makeAuthority := func(text string) PortableAuthority {
		return canonicalMergeFixture(PortableAuthority{Site: sqlitestore.ArchiveSite{ProfileUserID: "owner"}, Entries: []sqlitestore.ArchiveEntry{{ID: postID, ContentMD: domain.MarkdownBody(text)}, {ID: quoteID, ContentMD: domain.MarkdownBody(text), QuotedCommentID: &commentID}}, Comments: []sqlitestore.ArchiveComment{{ID: commentID, EntryID: postID, ContentMD: text}}})
	}
	local, incoming := makeAuthority("local"), makeAuthority("incoming")
	plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("d"), BaseGeneration: "generation", ExpiresAt: time.Unix(35, 0).UTC(), Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	selection := plan.DefaultSelection
	for i := range selection.Items {
		if selection.Items[i].Kind == MergeEntry || selection.Items[i].Kind == MergeComment {
			selection.Items[i].Action = MergeKeepBoth
		}
	}
	resolved, err := ResolvePortableMerge(plan, selection, local, incoming, MergeResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	entries := indexEntries(resolved.Authority.Entries)
	if *entries[quoteID].QuotedCommentID != commentID || *entries[resolved.EntryRemap[quoteID]].QuotedCommentID != resolved.CommentRemap[commentID] {
		t.Fatalf("wrong target after keep both: %+v", entries)
	}
	// Removing a target from an otherwise selected import must refuse the selection.
	empty := canonicalMergeFixture(PortableAuthority{Site: local.Site})
	plan, err = ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("d"), BaseGeneration: "generation", ExpiresAt: time.Unix(35, 0).UTC(), Local: empty, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	selection = plan.DefaultSelection
	for i := range selection.Items {
		if selection.Items[i].Kind == MergeComment {
			selection.Items[i].Action = MergeSkip
		}
	}
	if _, err := ResolvePortableMerge(plan, selection, empty, incoming, MergeResolveOptions{}); err == nil {
		t.Fatal("selected quote accepted skipped target")
	}
}
