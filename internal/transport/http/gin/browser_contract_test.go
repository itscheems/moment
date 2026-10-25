package ginhttp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/commentdto"
	"github.com/moment/moment/internal/transport/postdto"
)

// browserMirroredTypes are the wire shapes a browser parser validates by exact
// key set, and where the parser that mirrors each one lives.
//
// The browser client refuses a response carrying a key it does not know, which
// makes every field added on this side a change that breaks the page until the
// parser is widened -- and nothing on this side said so. That failure landed
// three separate times in one round: import counts, merge review entities, and
// the kinds a merge plan may name. Every one reached a running server, and one
// of them was caught by a person reading a diff rather than by a test.
//
// Recording the key set here does not remove the coupling. What it removes is
// the silence: adding a field now fails at the moment it is added, in a message
// naming the file that has to follow.
var browserMirroredTypes = map[string]struct {
	value  any
	mirror string
}{
	"accountView":               {accountView{}, "web/src/api/public.ts parseAccount"},
	"privateAccountView":        {privateAccountView{}, "web/src/api/public.ts parsePrivateAccount"},
	"accountSessionView":        {accountSessionView{}, "web/src/api/owner.ts accountSession"},
	"accountLimitsView":         {accountLimitsView{}, "web/src/api/owner.ts accountSession"},
	"identityView":              {identityView{}, "web/src/api/owner.ts parseIdentity"},
	"sessionListItem":           {sessionListItem{}, "web/src/api/owner.ts parseSessionListItem"},
	"archive.ImportCounts":      {archive.ImportCounts{}, "web/src/api/owner.ts parseImportCounts"},
	"archive.MergeReviewEntity": {archive.MergeReviewEntity{}, "web/src/api/owner.ts parseMergeReviewEntity"},
	"postdto.Post":              {postdto.Post{}, "web/src/api/public.ts parsePost and parseSearchHit"},
	"commentdto.Comment":        {commentdto.Comment{}, "web/src/api/public.ts parseComment"},
	"commentdto.Context":        {commentdto.Context{}, "web/src/api/public.ts commentContext"},
	"commentdto.Author":         {commentdto.Author{}, "web/src/api/public.ts parseCommentAuthor"},
	"app.MentionSuggestion":     {app.MentionSuggestion{}, "web/src/api/public.ts mentionSuggestions"},
	"app.SiteSettings":          {app.SiteSettings{}, "web/src/api/public.ts parseSiteIdentity"},
	"oauth.Descriptor":          {oauth.Descriptor{}, "web/src/api/owner.ts loginProviders"},
}

// browserMirroredBounds are the numeric limits a browser parser enforces by
// copying the constant, and where the copy lives.
//
// A bound drifts the same way a key set does and fails far more quietly. A key
// the parser does not know breaks the page for the first reader who loads it; a
// ceiling raised on this side and not on that one breaks it only once somebody
// reaches the new headroom -- and parseComment throws from inside the loop over
// a thread's items, so the one comment that reached it takes the whole
// conversation down with it for every reader. That is the failure this map is
// here for: MaxCommentAuthorNameCodePoints moved from 40 to 80 when comments
// started carrying the writer's real display name, the parser stayed at 40, and
// nothing said so.
//
// Recording the number cannot check what the TypeScript says -- nothing here
// reads it. What it does is refuse to let this side move in silence, in a
// message naming the file that has to follow, which is the whole job the key
// set contract does too.
var browserMirroredBounds = map[string]struct {
	value  int
	mirror string
}{
	"domain.MaxCommentAuthorNameCodePoints": {domain.MaxCommentAuthorNameCodePoints, "web/src/api/public.ts MAX_DISPLAY_NAME_CODE_POINTS"},
	"domain.MaxDisplayNameLength":           {domain.MaxDisplayNameLength, "web/src/api/public.ts MAX_DISPLAY_NAME_CODE_POINTS"},
	"domain.MaxCommentContentCodePoints":    {domain.MaxCommentContentCodePoints, "web/src/api/public.ts parseComment content_md"},
	"domain.MaxCommentMediaUsages":          {domain.MaxCommentMediaUsages, "web/src/api/public.ts parseComment parseMediaUsages"},
	"domain.MaxMediaAltCodePoints":          {domain.MaxMediaAltCodePoints, "web/src/api/public.ts parseMediaUsages alt_text"},
}

const browserContractEnv = "MOMENT_UPDATE_BROWSER_CONTRACT"

func browserContractPath() string { return filepath.Join("testdata", "browser-wire-keys.json") }

func browserBoundsPath() string { return filepath.Join("testdata", "browser-wire-bounds.json") }

// jsonKeys reports the wire keys a struct serialises, including through
// embedded structs, and without omitempty hiding the ones a zero value drops.
func jsonKeys(value any) []string {
	keys := make([]string, 0, 16)
	var walk func(reflect.Type)
	walk = func(structType reflect.Type) {
		for index := 0; index < structType.NumField(); index++ {
			field := structType.Field(index)
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name := tag
			for position, character := range tag {
				if character == ',' {
					name = tag[:position]
					break
				}
			}
			if field.Anonymous && name == "" {
				embedded := field.Type
				if embedded.Kind() == reflect.Pointer {
					embedded = embedded.Elem()
				}
				if embedded.Kind() == reflect.Struct {
					walk(embedded)
					continue
				}
			}
			if name == "" {
				name = field.Name
			}
			if !field.IsExported() {
				continue
			}
			keys = append(keys, name)
		}
	}
	structType := reflect.TypeOf(value)
	if structType.Kind() == reflect.Pointer {
		structType = structType.Elem()
	}
	walk(structType)
	sort.Strings(keys)
	return keys
}

func TestBrowserMirroredWireShapesHaveNotDrifted(t *testing.T) {
	recorded := make(map[string][]string, len(browserMirroredTypes))
	for name, mirrored := range browserMirroredTypes {
		recorded[name] = jsonKeys(mirrored.value)
	}

	if os.Getenv(browserContractEnv) == "1" {
		encoded, err := json.MarshalIndent(recorded, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(browserContractPath(), append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d wire shapes; update the browser parsers named above, then rerun without %s",
			len(recorded), browserContractEnv)
		return
	}

	contents, err := os.ReadFile(browserContractPath())
	if err != nil {
		t.Fatalf("read browser wire contract: %v", err)
	}
	var expected map[string][]string
	if err := json.Unmarshal(contents, &expected); err != nil {
		t.Fatalf("decode browser wire contract: %v", err)
	}
	if len(expected) != len(recorded) {
		t.Fatalf("contract records %d shapes, %d are declared; rerun with %s=1", len(expected), len(recorded), browserContractEnv)
	}
	for name, want := range expected {
		got, declared := recorded[name]
		if !declared {
			t.Fatalf("%s is recorded but no longer declared; rerun with %s=1", name, browserContractEnv)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s wire keys changed\n  now:  %v\n  was:  %v\nThe browser refuses a response carrying a key it does not know. Widen %s, then rerun with %s=1.",
				name, got, want, browserMirroredTypes[name].mirror, browserContractEnv)
		}
	}
}

func TestBrowserMirroredBoundsHaveNotDrifted(t *testing.T) {
	recorded := make(map[string]int, len(browserMirroredBounds))
	for name, mirrored := range browserMirroredBounds {
		recorded[name] = mirrored.value
	}

	if os.Getenv(browserContractEnv) == "1" {
		encoded, err := json.MarshalIndent(recorded, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(browserBoundsPath(), append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d bounds; update the browser parsers named above, then rerun without %s",
			len(recorded), browserContractEnv)
		return
	}

	contents, err := os.ReadFile(browserBoundsPath())
	if err != nil {
		t.Fatalf("read browser bound contract: %v", err)
	}
	var expected map[string]int
	if err := json.Unmarshal(contents, &expected); err != nil {
		t.Fatalf("decode browser bound contract: %v", err)
	}
	if len(expected) != len(recorded) {
		t.Fatalf("contract records %d bounds, %d are declared; rerun with %s=1", len(expected), len(recorded), browserContractEnv)
	}
	for name, want := range expected {
		got, declared := recorded[name]
		if !declared {
			t.Fatalf("%s is recorded but no longer declared; rerun with %s=1", name, browserContractEnv)
		}
		if got != want {
			t.Fatalf("%s changed from %d to %d.\nThe browser copies this number and refuses anything past it, which fails the whole response rather than the one field. Update %s, then rerun with %s=1.",
				name, want, got, browserMirroredBounds[name].mirror, browserContractEnv)
		}
	}
}
