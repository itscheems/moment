// Package devtools owns safe, deterministic development datasets.
package devtools

import (
	"fmt"
	"sort"
	"strings"
)

// RecipeVersion changes whenever logical fixture identity, ordering, content,
// or byte generation changes. Consumers may persist this value in goldens.
//
// v2: comments are owner-authored throughout. Retiring anonymous commenting
// removed the guest branch from the generator, so every scenario with comments
// now yields author_kind 'owner' where it used to alternate with 'guest' — at
// the same deterministic IDs, since those derive from this constant. A golden
// taken under v1 would otherwise have compared clean against different rows,
// which is the exact failure this version exists to prevent.
const RecipeVersion = "moment-dev-v2"

// Category identifies the acceptance surface exercised by a named scenario.
type Category string

const (
	// CategoryEdge identifies boundary and unusual-input scenarios.
	CategoryEdge Category = "edge"
	// CategoryUX identifies representative user-facing scenarios.
	CategoryUX Category = "ux"
	// CategoryArchive identifies export and restore scenarios.
	CategoryArchive Category = "archive"
	// CategoryCorruption identifies intentional diagnostic findings.
	CategoryCorruption Category = "corruption"
	// CategoryPerformance identifies release-scale generator scenarios.
	CategoryPerformance Category = "performance"
)

// Counts are the exact authority records and original bytes a recipe creates.
type Counts struct {
	Entries    int64 `json:"entries"`
	Comments   int64 `json:"comments"`
	Media      int64 `json:"media"`
	MediaBytes int64 `json:"media_bytes"`
}

// Progress is a bounded, content-free observation emitted between fixture
// batches. It is safe for CLI diagnostics and cancellation tests.
type Progress struct {
	Kind      string `json:"kind"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
}

// Scenario is the stable public catalog entry shared by help, validation,
// JSON output, tests, and downstream acceptance suites.
type Scenario struct {
	Name                  string   `json:"name"`
	Category              Category `json:"category"`
	RecipeVersion         string   `json:"recipe_version"`
	Description           string   `json:"description"`
	Counts                Counts   `json:"counts"`
	ExpectedDoctorCheckID string   `json:"expected_doctor_check_id,omitempty"`
	ArchiveRoundTrip      bool     `json:"archive_round_trip"`
}

const gib = int64(1 << 30)

var scenarios = []Scenario{
	{Name: "timeline-empty", Category: CategoryEdge, RecipeVersion: RecipeVersion, Description: "an initialized empty timeline", Counts: Counts{}},
	{Name: "timeline-basic", Category: CategoryUX, RecipeVersion: RecipeVersion, Description: "stable cursor ordering across 25 text posts", Counts: Counts{Entries: 25}},
	{Name: "post-long-markdown", Category: CategoryEdge, RecipeVersion: RecipeVersion, Description: "one post at the 20,000-code-point content boundary", Counts: Counts{Entries: 1}},
	{Name: "post-with-images", Category: CategoryUX, RecipeVersion: RecipeVersion, Description: "one, four, and nine-image post layouts with repeated managed media", Counts: Counts{Entries: 3, Media: 12, MediaBytes: 12 * 4096}},
	{Name: "post-images-missing-alt", Category: CategoryEdge, RecipeVersion: RecipeVersion, Description: "missing, decorative, and provided image alt states", Counts: Counts{Entries: 1, Media: 3, MediaBytes: 3 * 4096}},
	{Name: "comment-one-level", Category: CategoryUX, RecipeVersion: RecipeVersion, Description: "owner roots with replies flattened to one level", Counts: Counts{Entries: 1, Comments: 6}},
	{Name: "search-cjk", Category: CategoryUX, RecipeVersion: RecipeVersion, Description: "Chinese, English, numeric, and phrase search fixtures", Counts: Counts{Entries: 6}},
	{Name: "archive-years", Category: CategoryUX, RecipeVersion: RecipeVersion, Description: "posts spanning deterministic year, month, and day aggregates", Counts: Counts{Entries: 12}},
	{Name: "archive-roundtrip", Category: CategoryArchive, RecipeVersion: RecipeVersion, Description: "portable site, entries, comments, media, and alt states restored through format v1", Counts: Counts{Entries: 4, Comments: 8, Media: 6, MediaBytes: 6 * 4096}, ArchiveRoundTrip: true},
	{Name: "corruption-search-drift", Category: CategoryCorruption, RecipeVersion: RecipeVersion, Description: "one derived FTS row removed for doctor handoff", Counts: Counts{Entries: 3}, ExpectedDoctorCheckID: "fts_consistency"},
	{Name: "acceptance-scale", Category: CategoryPerformance, RecipeVersion: RecipeVersion, Description: "release-scale streaming dataset", Counts: Counts{Entries: 10_000, Comments: 50_000, Media: 20_000, MediaBytes: 20 * gib}},
	{Name: "release-scale", Category: CategoryPerformance, RecipeVersion: RecipeVersion, Description: "release verification streaming dataset", Counts: Counts{Entries: 10_000, Comments: 50_000, Media: 20_000, MediaBytes: 20 * gib}},
}

// Catalog returns a name-sorted defensive copy.
func Catalog() []Scenario {
	result := append([]Scenario(nil), scenarios...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// ScenarioNames is the canonical help and error spelling.
func ScenarioNames() []string {
	catalog := Catalog()
	names := make([]string, len(catalog))
	for index := range catalog {
		names[index] = catalog[index].Name
	}
	return names
}

// LookupScenario resolves exactly one canonical scenario name.
func LookupScenario(name string) (Scenario, error) {
	if strings.TrimSpace(name) != name || name == "" {
		return Scenario{}, fmt.Errorf("scenario name must be canonical")
	}
	for _, scenario := range scenarios {
		if scenario.Name == name {
			return scenario, nil
		}
	}
	return Scenario{}, fmt.Errorf("unknown scenario %q; choose one of: %s", name, strings.Join(ScenarioNames(), ", "))
}

// DefaultSeedScenario is the deterministic mixed fixture produced by dev seed.
func DefaultSeedScenario() Scenario {
	return Scenario{
		Name: "seed-default", Category: CategoryUX, RecipeVersion: RecipeVersion,
		Description: "mixed timeline, comment, media, search, and archive fixture",
		Counts:      Counts{Entries: 32, Comments: 48, Media: 16, MediaBytes: 16 * 4096},
	}
}
