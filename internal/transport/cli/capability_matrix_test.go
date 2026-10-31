package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/moment/moment/internal/buildinfo"
	"github.com/spf13/cobra"
)

//go:embed testdata/capability-matrix.json
var capabilityMatrixJSON []byte

type capabilityMatrix struct {
	SchemaVersion            int                   `json:"schema_version"`
	AllowedMetaNormalization []string              `json:"allowed_meta_normalization"`
	Commands                 []capabilityMatrixRow `json:"commands"`
}

type capabilityMatrixRow struct {
	Command        string                 `json:"command"`
	Direct         capabilityModeContract `json:"direct"`
	API            capabilityModeContract `json:"api"`
	Destructive    destructiveContract    `json:"destructive"`
	OutputSchema   string                 `json:"output_schema"`
	ExpectedExits  []ExitCode             `json:"expected_exits"`
	ParityStrategy string                 `json:"parity_strategy"`
}

type capabilityModeContract struct {
	Availability string   `json:"availability"`
	Auth         string   `json:"auth"`
	Locks        []string `json:"locks"`
}

type destructiveContract struct {
	RequiresYes    bool `json:"requires_yes"`
	SupportsDryRun bool `json:"supports_dry_run"`
}

type matrixInputProbe struct{ reads int }

func (probe *matrixInputProbe) Read([]byte) (int, error) {
	probe.reads++
	return 0, errors.New("matrix command unexpectedly read stdin")
}

func readCapabilityMatrix(t *testing.T) capabilityMatrix {
	t.Helper()
	var matrix capabilityMatrix
	decoder := json.NewDecoder(strings.NewReader(string(capabilityMatrixJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&matrix); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("capability matrix trailing content: %v", err)
	}
	return matrix
}

func TestCheckedInCapabilityMatrixMatchesEveryRegisteredLeaf(t *testing.T) {
	matrix := readCapabilityMatrix(t)
	if matrix.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", matrix.SchemaVersion)
	}
	if !reflect.DeepEqual(matrix.AllowedMetaNormalization, []string{"request_id", "duration_ms", "transport"}) {
		t.Fatalf("allowed normalization = %#v", matrix.AllowedMetaNormalization)
	}

	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	leaves := make(map[string]LeafSpec, len(application.leaves))
	for _, leaf := range application.leaves {
		command := strings.Join(leaf.Path, " ")
		if _, exists := leaves[command]; exists {
			t.Fatalf("registered duplicate leaf %q", command)
		}
		leaves[command] = leaf
	}

	previous := ""
	seen := make(map[string]struct{}, len(matrix.Commands))
	validAvailability := map[string]bool{
		"supported": true, "offline": true, "online_maintenance": true,
		"deep_or_summary": true, "summary_only": true, "unsupported": true,
	}
	validAuth := map[string]bool{"none": true, "local_data_access": true, "local_account": true, "account_token": true, "not_applicable": true}
	for _, row := range matrix.Commands {
		if row.Command <= previous {
			t.Fatalf("matrix commands are not strictly sorted: %q after %q", row.Command, previous)
		}
		previous = row.Command
		if _, duplicate := seen[row.Command]; duplicate {
			t.Fatalf("duplicate matrix row %q", row.Command)
		}
		seen[row.Command] = struct{}{}
		leaf, registered := leaves[row.Command]
		if !registered {
			t.Fatalf("matrix row %q has no registered leaf", row.Command)
		}
		for modeName, mode := range map[string]capabilityModeContract{"direct": row.Direct, "api": row.API} {
			if !validAvailability[mode.Availability] || !validAuth[mode.Auth] {
				t.Fatalf("%s %s has invalid contract: %#v", row.Command, modeName, mode)
			}
			if mode.Availability == "unsupported" && (mode.Auth != "not_applicable" || len(mode.Locks) != 0) {
				t.Fatalf("%s %s unsupported contract is not inert: %#v", row.Command, modeName, mode)
			}
		}
		if got := row.Direct.Availability != "unsupported"; got != leaf.Capability.supports(ModeDirect) {
			t.Fatalf("%s direct support matrix=%t registration=%t", row.Command, got, leaf.Capability.supports(ModeDirect))
		}
		if got := row.API.Availability != "unsupported"; got != leaf.Capability.supports(ModeAPI) {
			t.Fatalf("%s API support matrix=%t registration=%t", row.Command, got, leaf.Capability.supports(ModeAPI))
		}
		if row.Destructive.RequiresYes != leaf.Policy.Destructive || row.Destructive.SupportsDryRun != leaf.Policy.SupportsDryRun {
			t.Fatalf("%s destructive matrix=%#v registration=%#v", row.Command, row.Destructive, leaf.Policy)
		}
		if strings.TrimSpace(row.OutputSchema) == "" || strings.TrimSpace(row.ParityStrategy) == "" {
			t.Fatalf("%s omits output/parity contract", row.Command)
		}
		if leaf.RawStdout != strings.Contains(row.OutputSchema, "zip_stream") {
			t.Fatalf("%s raw stdout mismatch: output=%q registered=%t", row.Command, row.OutputSchema, leaf.RawStdout)
		}
		if len(row.ExpectedExits) < 3 || row.ExpectedExits[0] != ExitSuccess {
			t.Fatalf("%s expected exits = %#v", row.Command, row.ExpectedExits)
		}
		for index, exit := range row.ExpectedExits {
			if exit != ExitSuccess && (exit < ExitInvalidInput || exit > ExitInternal) {
				t.Fatalf("%s contains invalid exit %d", row.Command, exit)
			}
			if index > 0 && row.ExpectedExits[index-1] >= exit {
				t.Fatalf("%s exits are not strictly sorted: %#v", row.Command, row.ExpectedExits)
			}
		}
		if row.ExpectedExits[1] != ExitInvalidInput || row.ExpectedExits[len(row.ExpectedExits)-1] != ExitInternal {
			t.Fatalf("%s omits common input/internal exits: %#v", row.Command, row.ExpectedExits)
		}
		if row.Command == "init" &&
			!reflect.DeepEqual(row.Direct.Locks, []string{"import:exclusive", "serve:exclusive"}) {
			t.Fatalf("%s initialization lock order = %#v", row.Command, row.Direct.Locks)
		}
	}
	if len(seen) != len(leaves) {
		missing := make([]string, 0)
		for command := range leaves {
			if _, ok := seen[command]; !ok {
				missing = append(missing, command)
			}
		}
		sort.Strings(missing)
		t.Fatalf("matrix missing registered leaves: %v", missing)
	}
}

func TestEveryMatrixModeExecutesOrRejectsBeforeHandlerAndPrompt(t *testing.T) {
	matrix := readCapabilityMatrix(t)
	production := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	application := New(Config{BinaryName: "moment", Now: production.config.Now, RequestID: production.config.RequestID})
	for _, group := range production.groups {
		application.MustRegisterGroup(group)
	}
	calls := make(map[string]int)
	for _, original := range production.leaves {
		leaf := original
		command := strings.Join(leaf.Path, " ")
		leaf.Run = func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
			key := command + "/" + string(invocation.Mode())
			calls[key]++
			return Result{Data: map[string]any{"command": command, "mode": invocation.Mode()}}, nil
		}
		application.MustRegisterLeaf(leaf)
	}
	positional := map[string][]string{
		"post like": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post unlike": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post like-status": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post reply-context": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"comment like": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment unlike": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment like-status": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment reply-context": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "notification get": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"user import":       {"archive.zip"},
		"comment get":       {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"auth revoke-token": {"0123456789abcdef0123456789abcdef"},
		"post get":          {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post update": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post delete": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post pin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post repost": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post unrepost": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post unpin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment list": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"comment create": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment reply": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"comment delete": {"01ARZ3NDEKTSV4RRFFQ69G5FAW"}, "comment pin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"comment unpin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"}, "media add": {"image.png"},
		"comment replies": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"media delete":    {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"media get":       {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "backup inspect": {"archive.zip"},
		"backup merge-plan": {"archive.zip"}, "backup merge": {"merge-selection.json"},
		"backup merge-discard": {"merge-selection.json"},
		"dev scenario":         {"timeline-empty"},
		"link unfurl":          {"https://example.com/a"},
		"login":                {"github"},
		"user approve":         {"applicant"},
		"user set-role":        {"applicant"},
		"site set-profile":     {"applicant"},
		"user suspend":         {"applicant"},
		"user identities":      {"applicant"},
		"user delete":          {"applicant"},
		"article draft":        {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article save":         {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article discard":      {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article publish":      {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article get":          {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article unpublish":    {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article delete":       {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"article edit":         {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	// A leaf that takes a positional argument and has no entry above would be
	// executed here with none, and a leaf that takes one without declaring it
	// would never be reached at all. Both shipped once: three account review
	// commands answered "unknown command <username>" because they omitted
	// Args, and nothing noticed because nothing ran them with an argument.
	for _, leaf := range production.leaves {
		command := strings.Join(leaf.Path, " ")
		if leaf.ArgsUsage == "" {
			continue
		}
		if leaf.Args == nil {
			t.Fatalf("%s documents %q but declares no Args, so cobra reads it as a subcommand", command, leaf.ArgsUsage)
		}
		// Only a required argument needs an entry. Asking the validator itself
		// rather than reading the brackets in ArgsUsage keeps the two from
		// disagreeing about which arguments are optional.
		if _, present := positional[command]; !present && leaf.Args(&cobra.Command{}, nil) != nil {
			t.Fatalf("%s requires %q but this test would execute it with no argument", command, leaf.ArgsUsage)
		}
	}
	for _, row := range matrix.Commands {
		for _, modeCase := range []struct {
			name     string
			mode     Mode
			contract capabilityModeContract
		}{
			{name: "direct", mode: ModeDirect, contract: row.Direct},
			{name: "api", mode: ModeAPI, contract: row.API},
		} {
			t.Run(strings.ReplaceAll(row.Command+"_"+modeCase.name, " ", "_"), func(t *testing.T) {
				key := row.Command + "/" + string(modeCase.mode)
				before := calls[key]
				args := make([]string, 0, 12)
				if modeCase.mode == ModeAPI {
					args = append(args, "--url", "http://127.0.0.1:1")
				} else {
					args = append(args, "--data-dir", "unused-probe-data")
				}
				args = append(args, strings.Fields(row.Command)...)
				args = append(args, positional[row.Command]...)
				if row.Destructive.RequiresYes && modeCase.contract.Availability != "unsupported" {
					args = append(args, "--yes")
				}
				args = append(args, "--json")
				inputProbe := &matrixInputProbe{}
				var stdout, stderr bytes.Buffer
				exit := application.Execute(context.Background(), args, Streams{In: inputProbe, Out: &stdout, Err: &stderr})
				output := commandOutput{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
				document := decodeOneDocument(t, output.stdout)
				if modeCase.contract.Availability == "unsupported" {
					errorData := document["error"].(map[string]any)
					details := errorData["details"].(map[string]any)
					if output.exit != ExitInvalidInput || output.stderr != "" || errorData["code"] != "MODE_UNSUPPORTED" ||
						details["command"] != row.Command || details["mode"] != string(modeCase.mode) || calls[key] != before || inputProbe.reads != 0 {
						t.Fatalf("output = %#v document=%#v calls=%d stdin_reads=%d", output, document, calls[key]-before, inputProbe.reads)
					}
					return
				}
				data := document["data"].(map[string]any)
				if output.exit != ExitSuccess || output.stderr != "" || calls[key] != before+1 ||
					data["command"] != row.Command || data["mode"] != string(modeCase.mode) || inputProbe.reads != 0 {
					t.Fatalf("output = %#v document=%#v calls=%d stdin_reads=%d", output, document, calls[key]-before, inputProbe.reads)
				}
			})
		}
	}
}

func TestEveryLeafHelpIsDiscoverableAndScoped(t *testing.T) {
	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	for _, leaf := range application.leaves {
		command := strings.Join(leaf.Path, " ")
		t.Run(strings.ReplaceAll(command, " ", "_"), func(t *testing.T) {
			args := append(append([]string(nil), leaf.Path...), "--help")
			output := execute(application, "", args...)
			if output.exit != ExitSuccess || output.stderr != "" {
				t.Fatalf("help output = %#v", output)
			}
			for _, required := range []string{"INPUTS", "OUTPUT", "ERRORS", "Examples:", leaf.Example} {
				if !strings.Contains(output.stdout, required) {
					t.Fatalf("help for %s missing %q:\n%s", command, required, output.stdout)
				}
			}
		})
	}
}

func TestEveryHelpSurfaceMatchesGoldenDigest(t *testing.T) {
	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	expected := readHelpDigests(t)

	paths := map[string][]string{"moment": nil}
	for _, group := range application.groups {
		paths[group.Name] = []string{group.Name}
	}
	for _, leaf := range application.leaves {
		paths[strings.Join(leaf.Path, " ")] = append([]string(nil), leaf.Path...)
	}
	if !updatingGolden() && len(expected) != len(paths) {
		t.Fatalf("help golden entries=%d registered surfaces=%d; rerun with %s=1 to record", len(expected), len(paths), updateGoldenEnv)
	}
	recorded := make(map[string]string, len(paths))
	commands := make([]string, 0, len(paths))
	for command := range paths {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, command := range commands {
		args := append(append([]string(nil), paths[command]...), "--help")
		output := execute(application, "", args...)
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("%s help output = %#v", command, output)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(output.stdout)))
		recorded[command] = digest
		if updatingGolden() {
			continue
		}
		want, present := expected[command]
		if !present {
			t.Fatalf("%s has no recorded help digest; rerun with %s=1 to record", command, updateGoldenEnv)
		}
		if digest != want {
			t.Fatalf("%s help digest=%s want=%s\n%s", command, digest, want, output.stdout)
		}
	}
	if updatingGolden() {
		writeHelpDigests(t, recorded)
		t.Logf("recorded %d help digests; rerun without %s to assert them", len(recorded), updateGoldenEnv)
	}
}

// updateGoldenEnv regenerates the help digests instead of asserting them.
//
// The digests used to live inline in this file, which made adding one command a
// four step manual dance: blank the map, run with -v, scrape the logged lines,
// paste them back. Every step of that is a chance to record a digest for output
// nobody looked at. Writing the file the test already knows how to read removes
// the dance without removing the assertion -- the default path still fails on
// any drift, and the recorded file is reviewable in the diff.
const updateGoldenEnv = "MOMENT_UPDATE_HELP_GOLDEN"

func updatingGolden() bool { return os.Getenv(updateGoldenEnv) == "1" }

func helpDigestPath() string { return filepath.Join("testdata", "help-digests.json") }

func readHelpDigests(t *testing.T) map[string]string {
	t.Helper()
	contents, err := os.ReadFile(helpDigestPath())
	if err != nil {
		if updatingGolden() {
			return map[string]string{}
		}
		t.Fatalf("read help digests: %v", err)
	}
	var digests map[string]string
	if err := json.Unmarshal(contents, &digests); err != nil {
		t.Fatalf("decode help digests: %v", err)
	}
	return digests
}

func writeHelpDigests(t *testing.T, digests map[string]string) {
	t.Helper()
	encoded, err := json.MarshalIndent(digests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helpDigestPath(), append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write help digests: %v", err)
	}
}
