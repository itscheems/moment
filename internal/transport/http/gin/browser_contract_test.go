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
	"github.com/moment/moment/internal/transport/commentdto"
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
	"commentdto.Comment":        {commentdto.Comment{}, "web/src/api/public.ts parseComment"},
	"commentdto.Author":         {commentdto.Author{}, "web/src/api/public.ts parseCommentAuthor"},
	"app.MentionSuggestion":     {app.MentionSuggestion{}, "web/src/api/public.ts mentionSuggestions"},
	"app.SiteSettings":          {app.SiteSettings{}, "web/src/api/public.ts parseSiteIdentity"},
}

const browserContractEnv = "MOMENT_UPDATE_BROWSER_CONTRACT"

func browserContractPath() string { return filepath.Join("testdata", "browser-wire-keys.json") }

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
