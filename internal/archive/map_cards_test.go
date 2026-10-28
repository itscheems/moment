package archive

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const archiveMapJSON = `{"places":[{"name":"Ginza","display_name":"Tokyo","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"},{"name":"静安寺","display_name":"上海","latitude":31.2231861,"longitude":121.4450667,"coordinate_system":"gcj02"}],"connect_points":true}`

func mapCardForArchive(t *testing.T) *domain.MapCard {
	t.Helper()
	v, err := domain.ParseMapCard([]byte(archiveMapJSON))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestMapCardsRoundtripFullArchiveAndArticleDrafts(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, true)
	seedArticleFixture(t, f)
	card := mapCardForArchive(t)
	const mapOnlyID = "01ARZ3NDEKTSV4RRFFQ69G5FE2"
	if err := f.database.WriteTx(ctx, func(q sqlitestore.Queries) error {
		if _, err := q.ExecContext(ctx, `INSERT INTO entry_map_card(entry_id,map_card_json) VALUES (?,?)`, entryID, archiveMapJSON); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO entries(id,kind,content_md,visibility,author_user_id,created_at_ms,updated_at_ms,published_at_ms) VALUES (?,'moment','','everyone',?,1,1,1)`, mapOnlyID, fixtureAccountID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO entry_map_card(entry_id,map_card_json) VALUES (?,?)`, mapOnlyID, archiveMapJSON); err != nil {
			return err
		}
		// Keep the original pictures and append a map to both published and draft bodies.
		for _, table := range []string{"entry_article", "article_drafts"} {
			var body string
			column, id := "entry_id", articleEntryID
			if table == "article_drafts" {
				column, id = "id", articleDraftID
			}
			if err := q.QueryRowContext(ctx, `SELECT body_json FROM `+table+` WHERE `+column+` = ?`, id).Scan(&body); err != nil {
				return err
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return err
			}
			doc["schema_version"] = 3
			doc["content"] = append(doc["content"].([]any), map[string]any{"type": "mapCard", "attrs": map[string]any{"map_card": card, "id": "map-trip"}})
			encoded, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET body_json = ?, body_schema_version = 3 WHERE `+column+` = ?`, string(encoded), id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bytes := exportFixtureBytes(t, f)
	members, _ := readArchive(t, bytes)
	if !strings.Contains(string(members["data/entries.jsonl"]), `"map_card"`) || !strings.Contains(string(members["data/entry_articles.jsonl"]), `"mapCard"`) || !strings.Contains(string(members["data/article_drafts.jsonl"]), `"mapCard"`) {
		t.Fatal("export omitted maps")
	}
	db := stageArchive(t, f, writeArchiveBytes(t, bytes))
	snapshot := stagedSnapshot(t, db)
	if err := snapshot.Entries(ctx, func(entry sqlitestore.ArchiveEntry) error {
		if (entry.ID == entryID || entry.ID == mapOnlyID) && !reflect.DeepEqual(entry.MapCard, card) {
			t.Fatalf("map snapshot changed: %#v", entry.MapCard)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.EntryArticles(ctx, func(article sqlitestore.ArchiveEntryArticle) error {
		body, err := domain.ParseArticleDocument([]byte(article.BodyJSON))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(body.Content[len(body.Content)-1].MapCard, card) {
			t.Fatal("article map changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ArticleDrafts(ctx, func(draft sqlitestore.ArchiveArticleDraft) error {
		body, err := domain.ParseArticleDraftDocument([]byte(draft.BodyJSON))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(body.Content[len(body.Content)-1].MapCard, card) {
			t.Fatal("draft map changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestArchiveV2StillRestoresAndRejectsMaps(t *testing.T) {
	for _, withMap := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "reject map"}[withMap], func(t *testing.T) {
			f := newExportFixture(t, true)
			members, order := readArchive(t, exportFixtureBytes(t, f))
			var manifest map[string]any
			if err := json.Unmarshal(members["manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["format_version"] = 2
			members["manifest.json"] = append(mustMarshalTest(t, manifest), '\n')
			members["README.txt"] = []byte(strings.Replace(readme, "v5", "v2", 1))
			if withMap {
				members["data/entries.jsonl"] = rewriteEntryLine(t, members["data/entries.jsonl"], entryID, func(line map[string]json.RawMessage) { line["map_card"] = json.RawMessage(archiveMapJSON) })
			}
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			path := filepath.Join(t.TempDir(), "legacy.zip")
			writeArchive(t, path, members, order)
			if withMap {
				now := time.Now()
				_, err := testInspector(t, f.runtime.Root, f.database, &now, math.MaxUint64).InspectFile(context.Background(), path, testAuthorization(1))
				if !domain.HasCode(err, domain.CodeExportInvalid) {
					t.Fatalf("v2 map accepted: %v", err)
				}
				return
			}
			db := stageArchive(t, f, path)
			snapshot := stagedSnapshot(t, db)
			if err := snapshot.Entries(context.Background(), func(entry sqlitestore.ArchiveEntry) error {
				if entry.MapCard != nil {
					t.Fatal("legacy archive invented a map")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMapChangesParticipateInMergeAndClone(t *testing.T) {
	card := mapCardForArchive(t)
	incomingCard := domain.CloneMapCard(card)
	incomingCard.ConnectPoints = false
	local := canonicalMergeFixture(PortableAuthority{Site: sqlitestore.ArchiveSite{ProfileUserID: "owner"}, Entries: []sqlitestore.ArchiveEntry{{ID: entryID, ContentMD: domain.MarkdownBody("trip"), MapCard: card}}})
	incoming := canonicalMergeFixture(PortableAuthority{Site: sqlitestore.ArchiveSite{ProfileUserID: "owner"}, Entries: []sqlitestore.ArchiveEntry{{ID: entryID, ContentMD: domain.MarkdownBody("trip"), MapCard: incomingCard}}})
	plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("a"), BaseGeneration: "generation", ExpiresAt: time.Unix(40, 0).UTC(), Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	selection := plan.DefaultSelection
	for i, item := range plan.Items {
		if item.ID == entryID {
			found = true
			if !reflect.DeepEqual(item.ChangedFields, []string{"map_card"}) {
				t.Fatalf("fields=%v", item.ChangedFields)
			}
			selection.Items[i].Action = MergeUseIncoming
		}
	}
	if !found {
		t.Fatal("map change was not a conflict")
	}
	merged, err := ResolvePortableMerge(plan, selection, local, incoming, MergeResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged.Authority.Entries[0].MapCard, incomingCard) {
		t.Fatal("merge lost map")
	}
	merged.Authority.Entries[0].MapCard.Places[0].Name = "changed result"
	if incoming.Entries[0].MapCard.Places[0].Name != "Ginza" {
		t.Fatal("merge result aliases incoming map")
	}
	review := reviewEntry(incoming.Entries[0], MergeReviewIncoming, nil)
	if review.MapCard == nil {
		t.Fatal("review omitted map")
	}
}
