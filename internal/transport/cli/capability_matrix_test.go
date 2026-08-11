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
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/moment/moment/internal/buildinfo"
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
	validAuth := map[string]bool{"none": true, "local_data_access": true, "owner_token": true, "not_applicable": true}
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
		"post get": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post update": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post delete": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment list": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"comment create": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment reply": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"comment delete": {"01ARZ3NDEKTSV4RRFFQ69G5FAW"}, "media add": {"image.png"},
		"media get": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "backup inspect": {"archive.zip"},
		"dev scenario": {"timeline-empty"},
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
	expected := map[string]string{
		"backup":                 "0f09009ee6ac784a7d474be063af381e39043929396d4602da77dbead9426645",
		"backup export":          "41c2c59d7320e821b4bb80b6a605df0285f9fb6903d7dff0a22d3adaf795c9c2",
		"backup import":          "eccbd5ccc42fb93abb68350404f07cf3368cdb8929d23dfa61805f978b5f1ba5",
		"backup inspect":         "ecc5db494ffe8401cdaeab289d84f8a44b7d701f5584774a036cf185b002ab10",
		"backup status":          "2ad2eb8806b33a180826d201157e189c2488d7747aa9169bb79c42411118cb9e",
		"comment":                "e59757017d913839937dc3eaa6abc91e594fcc3518d4d1e33f6c93ad8ed66d89",
		"comment create":         "855c0858d8ff5c84b82300374b564df4de610528bdf567aa612ce96ba87b4cf3",
		"comment delete":         "1ae438d8870bb0da2d34306260131316228dc82b19c29e34215ac8e1eaf2c7d9",
		"comment list":           "f92976c87a2f2f329d76fce26b1fd6004cc3f5c3a8d58305619329c362a4e633",
		"comment reply":          "a56fd1da10f8558021bb9467dd8a15c1433c0ff05da06f82158ebc89bf3d64ee",
		"dev":                    "f3fbc09380185bb3d32d04b94712c5968c4e734067ceacb8ef00a3e6666e9a32",
		"dev reset":              "623279be28439377ce7e723b4351046fb758f7126e5c81db39a55646fc2621ce",
		"dev scenario":           "008cd54831c7ef825cf0e41d7370ba3026e19e9452aab49202d958cbc793b5d8",
		"dev seed":               "652cb437617e1c3e6d94d016bb75f325710cf092008899ccc6da9dcf13d7fdbb",
		"doctor":                 "6dd90e5897b539b1f8048d016a8e091ffdaf91392ae22902c256ab5dfcffe44e",
		"init":                   "1aa57e0cc287754c3515c7155864ae9e79fe99d3afcd5d298e1eb3aa238b4c19",
		"media":                  "c41a56ccf11dae431650b9b7a726820567c3f31d30612a383f5d3ce8dd0efede",
		"media add":              "5dde8c7f45f638cef87117dd6431edde094175378e5745fe3d9dfe07811ecc7d",
		"media get":              "6840a6e4053f84760e7a1a92efdd31c9a77965a502a2b9e41d57c9696e2618f5",
		"media list":             "6fc9294aa50466b8ccfa034dc9280b40efbcffa56c02fb76e1cb237446f2c69f",
		"media prune":            "f2719f2b81bbba418e0ad4527416b14e13b54cf7dc804c6f8e15ba773ecbc54d",
		"media rebuild-previews": "3b52ab84b61f18541b637bcd2248ea1be45967e350f9824cf3f839e5fcdf8e9e",
		"media verify":           "6440583fd5a92254431d459066dfad2c2376f6145816f68ea59b142a4789ef4e",
		"moment":                 "523e7afd292a0f8b5336537d1d8db5f808b9cb1b093a27a5005b290275a4305b",
		"owner":                  "623d517d548b68bf12499451b46d1b4a34d4ac6b0d7252bb59db8338875e1e9d",
		"owner init":             "3e2d3549b6ade126689229226e50f68c9b5868376873c44a1c9fa20d0818419e",
		"owner rotate-token":     "3e34b359dcfd5ced8c32ea28d9160947ab67a314d327fe9a3d213142bced6e0c",
		"owner status":           "ca4e6635f78403f88e14f77f225d3aad619123cf25c0e73b57f3daa61eb39d34",
		"post":                   "97fba13be8410b983c125b46ffebd5f43b67f5728aef6173a39291b5006364ac",
		"post create":            "a720b47adeeea603c918683b8e40c70c19df2632d48b965177a382429e1c8f47",
		"post delete":            "f053d4498c17f292e84b660b8b8abcb9dcb00daa9291433c0f5eb643233226e6",
		"post get":               "48690ea66ae796353be0b4cdd94f1fc0b4352abc9e2c834f7964ad2f79104a8e",
		"post list":              "6afa5e64cd9dee4cafd2f868cb9745ec45e45ee1845dbc81ff9d19ab35fb8739",
		"post update":            "e6c267409edfb1eeed2a512ef24f483fe179f83263205b76a815f220a74ee43e",
		"search":                 "6d87f554839c4d517bb1b4be03d897a5e7f571af0e5be6b8c63351f693dc31a1",
		"search query":           "b9b05f473ce5193c2c3970af09381c42934faaced45199f11751a1c08ac58e6a",
		"search rebuild":         "dd03cd5a216bca7e503f4f6d03d96c032db91e13f66ef8411045df7f6e37eb28",
		"serve":                  "3916e71755d97d99ae6c697b16380776e65ed083b1537a6272dfcdefe8916636",
		"site":                   "09043ff9807f0d5b49d4192cca5f0acec67b53014304a19e85e31f1275ad8da7",
		"site get":               "04ec3ec58064043f01538f2b52c13ee43476ad2143440f1254ec0c90296520ec",
		"site update":            "4bcd99a395bab472c49ae459fb6e633d735131206519ab3c78e50b322600ad50",
		"version":                "0b69aae12549989186adc1cc9dd8066fcdb54f7f349a2b023e2df150d7f1dce1",
	}

	paths := map[string][]string{"moment": nil}
	for _, group := range application.groups {
		paths[group.Name] = []string{group.Name}
	}
	for _, leaf := range application.leaves {
		paths[strings.Join(leaf.Path, " ")] = append([]string(nil), leaf.Path...)
	}
	if len(expected) > 0 && len(expected) != len(paths) {
		t.Fatalf("help golden entries=%d registered surfaces=%d", len(expected), len(paths))
	}
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
		want, present := expected[command]
		if !present {
			t.Logf("%q: %q,", command, digest)
			continue
		}
		if digest != want {
			t.Fatalf("%s help digest=%s want=%s\n%s", command, digest, want, output.stdout)
		}
	}
	if len(expected) == 0 {
		t.Fatal("record the logged full help-tree golden digests")
	}
}
