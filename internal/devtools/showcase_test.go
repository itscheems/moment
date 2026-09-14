package devtools

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
)

// showcaseExport reads the portable archive of a built showcase into the
// records the assertions below reason about. The archive is the one reader
// that sees every table at once without a service per table.
type showcaseExport struct {
	entries  []map[string]any
	comments []map[string]any
	users    []map[string]any
	digest   string
}

func exportShowcase(t *testing.T, root string) showcaseExport {
	t.Helper()
	exporter, err := archive.NewExporter(archive.Options{DataDir: root, AppVersion: RecipeVersion})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	result := showcaseExport{}
	var ownerID string
	for _, member := range reader.File {
		if member.Name != "data/site.json" {
			continue
		}
		opened, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		var site struct {
			OwnerID string `json:"owner_id"`
		}
		if err := json.NewDecoder(opened).Decode(&site); err != nil {
			t.Fatal(err)
		}
		_ = opened.Close()
		ownerID = site.OwnerID
	}
	// The owner's account id is minted at random per instance and reaches
	// the export as an author id and a user id, so the logical digest
	// compares it under one name.
	digest := sha256.New()
	for _, member := range reader.File {
		var into *[]map[string]any
		switch member.Name {
		case "data/entries.jsonl":
			into = &result.entries
		case "data/comments.jsonl":
			into = &result.comments
		case "data/users.jsonl":
			into = &result.users
		default:
			continue
		}
		opened, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(opened)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		var lines []string
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			*into = append(*into, record)
			for _, field := range []string{"id", "author_user_id"} {
				if record[field] == ownerID {
					record[field] = "owner"
				}
			}
			canonical, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(canonical))
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		_ = opened.Close()
		// Rows are exported in id order, and the owner's id is random, so
		// the digest reads them in content order instead.
		sort.Strings(lines)
		digest.Write([]byte(member.Name))
		for _, line := range lines {
			digest.Write([]byte(line))
			digest.Write([]byte{'\n'})
		}
	}
	result.digest = hex.EncodeToString(digest.Sum(nil))
	return result
}

func buildShowcase(t *testing.T, anchor time.Time, seed int64) (Result, string) {
	t.Helper()
	runtimeState := developmentRuntime(t)
	result, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: ShowcaseScenarioName, Seed: seed, Anchor: anchor,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result, runtimeState.Root
}

func TestShowcaseBuildsEveryListedCase(t *testing.T) {
	anchor := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	result, root := buildShowcase(t, anchor, 1)
	recipe, err := LookupScenario(ShowcaseScenarioName)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.DoctorHealthy || result.Plan.Counts != recipe.Counts || result.Showcase == nil {
		t.Fatalf("showcase result = %#v", result)
	}
	if result.Showcase.AnchoredAt != anchor.Format(time.RFC3339) || result.Showcase.Owner != showcaseOwnerUsername {
		t.Fatalf("manifest header = %#v", result.Showcase)
	}
	cases := map[string]ShowcaseCase{}
	for _, item := range result.Showcase.Cases {
		if _, duplicate := cases[item.Key]; duplicate {
			t.Fatalf("case key %q is listed twice", item.Key)
		}
		cases[item.Key] = item
	}
	for _, post := range showcasePosts() {
		item, listed := cases["post."+post.key]
		if !listed || item.ID == "" || item.Note == "" {
			t.Fatalf("post %q is not in the manifest: %#v", post.key, item)
		}
		if post.deleted != (item.Path == "") {
			t.Fatalf("post %q path = %q, deleted = %v", post.key, item.Path, post.deleted)
		}
	}
	for _, member := range showcaseMembers() {
		item, listed := cases["member."+member.key]
		if !listed || item.Path != "/"+member.username {
			t.Fatalf("member %q is not in the manifest: %#v", member.key, item)
		}
	}

	export := exportShowcase(t, root)
	if len(export.entries) != int(recipe.Counts.Entries) || len(export.comments) != int(recipe.Counts.Comments) {
		t.Fatalf("exported %d entries and %d comments, want %+v", len(export.entries), len(export.comments), recipe.Counts)
	}
	years := map[string]int{}
	var hidden, quoted, unavailable, cards, reposted, edited, located, media int
	for _, entry := range export.entries {
		created, err := time.Parse(time.RFC3339Nano, entry["published_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !created.Before(anchor) {
			t.Fatalf("entry %v is dated after the anchor: %v", entry["id"], created)
		}
		years[created.In(time.FixedZone("CST", 8*3600)).Format("2006")]++
		if entry["visibility"] == "only_me" {
			hidden++
		}
		if entry["quoted_entry_id"] != nil {
			quoted++
		}
		if entry["quote_unavailable"] == true {
			unavailable++
		}
		if entry["link_cards"] != nil {
			cards++
		}
		if entry["reposted_at"] != nil {
			reposted++
		}
		if entry["edited_at"] != nil {
			edited++
		}
		if entry["location"] != nil {
			located++
		}
		if usages, ok := entry["media"].([]any); ok && len(usages) > 0 {
			media++
		}
	}
	if len(years) < 8 {
		t.Fatalf("entries span %d years, want at least eight: %v", len(years), years)
	}
	if hidden != 2 || quoted != 2 || unavailable != 1 || cards != 4 || reposted != 2 || edited != 1 || located != 3 || media < 12 {
		t.Fatalf("entry states: hidden=%d quoted=%d unavailable=%d cards=%d reposted=%d edited=%d located=%d media=%d", hidden, quoted, unavailable, cards, reposted, edited, located, media)
	}

	var pinned, answers, memberAuthored, withMedia int
	perEntry := map[string]int{}
	for _, comment := range export.comments {
		perEntry[comment["entry_id"].(string)]++
		if comment["pinned"] == true {
			pinned++
		}
		if comment["reply_to_comment_id"] != nil {
			answers++
		}
		if comment["author_name"] != showcaseOwnerDisplayName {
			memberAuthored++
		}
		if usages, ok := comment["media"].([]any); ok && len(usages) > 0 {
			withMedia++
		}
	}
	busiest := 0
	for _, count := range perEntry {
		if count > busiest {
			busiest = count
		}
	}
	if pinned != 2 || answers < 12 || memberAuthored < 30 || withMedia != 1 || busiest != 38 {
		t.Fatalf("comment states: pinned=%d answers=%d members=%d media=%d busiest=%d", pinned, answers, memberAuthored, withMedia, busiest)
	}

	statuses := map[string]int{}
	usernames := map[string]bool{}
	for _, user := range export.users {
		statuses[user["role"].(string)+"/"+user["status"].(string)]++
		usernames[user["username"].(string)] = true
	}
	if statuses["owner/active"] != 1 || statuses["member/active"] != 3 || statuses["member/pending"] != 1 || statuses["member/suspended"] != 1 {
		t.Fatalf("account statuses = %v", statuses)
	}
	for _, username := range []string{showcaseOwnerUsername, "aming", "lena", "xiaobei", "wanger", "bestdeals4u"} {
		if !usernames[username] {
			t.Fatalf("username %q is missing from %v", username, usernames)
		}
	}
}

func TestShowcaseIsDeterministicForOneAnchorAndSeed(t *testing.T) {
	anchor := time.Date(2026, time.February, 1, 8, 30, 0, 0, time.UTC)
	_, firstRoot := buildShowcase(t, anchor, 7)
	_, secondRoot := buildShowcase(t, anchor, 7)
	first, second := exportShowcase(t, firstRoot), exportShowcase(t, secondRoot)
	if first.digest != second.digest {
		t.Fatalf("same anchor and seed drifted: %s != %s", first.digest, second.digest)
	}
	// Early in a year every "this year" calendar case would be ahead of the
	// anchor; they fall back a year rather than into the future.
	for _, entry := range first.entries {
		created, err := time.Parse(time.RFC3339Nano, entry["published_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !created.Before(anchor) {
			t.Fatalf("entry %v is dated after a February anchor: %v", entry["id"], created)
		}
	}
}

func TestShowcasePicturesFitTheirBudgets(t *testing.T) {
	// Every picture is padded to its budget; a drawing that outgrows it
	// would move the catalog's byte total, so it fails here first.
	specs := []showcaseImage{landscape(0, 88), portrait(90, 9), square(180, 12), panorama(270, 4), tall(45, 5), tiny(135, 9), large(225, 1), favicon(315), thumbnail(60, 0)}
	for index, spec := range specs {
		contents, err := showcasePNG(spec, 1, index)
		if err != nil {
			t.Fatalf("%dx%d: %v", spec.Width, spec.Height, err)
		}
		if int64(len(contents)) != spec.Bytes || !bytes.HasPrefix(contents, []byte("\x89PNG")) || !bytes.HasSuffix(contents, []byte("IEND\xaeB`\x82")) {
			t.Fatalf("%dx%d encoded to %d bytes, want a %d byte PNG", spec.Width, spec.Height, len(contents), spec.Bytes)
		}
	}
	if _, err := showcasePNG(showcaseImage{Width: 600, Height: 400, Bytes: 64}, 1, 0); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("an impossible budget must be refused, got %v", err)
	}
}
