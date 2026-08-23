package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
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
		if (row.Command == "init" || row.Command == "owner init") &&
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
		"post get": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post update": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post delete": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post pin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post repost": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "post unrepost": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"post unpin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment list": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"comment create": {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "comment reply": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"comment delete": {"01ARZ3NDEKTSV4RRFFQ69G5FAW"}, "comment pin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"comment unpin": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"}, "media add": {"image.png"},
		"comment replies": {"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"media get":       {"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, "backup inspect": {"archive.zip"},
		"backup merge-plan": {"archive.zip"}, "backup merge": {"merge-selection.json"},
		"backup merge-discard": {"merge-selection.json"},
		"dev scenario":         {"timeline-empty"},
		"link unfurl":          {"https://example.com/a"},
		"user approve":         {"applicant"},
		"user suspend":         {"applicant"},
		"user identities":      {"applicant"},
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
	expected := map[string]string{
		"backup":                 "b4e985e599b0a59cf7cc357d777aa08d81d3704d986bd833dc7f5853d757c802",
		"backup export":          "41c2c59d7320e821b4bb80b6a605df0285f9fb6903d7dff0a22d3adaf795c9c2",
		"backup import":          "eccbd5ccc42fb93abb68350404f07cf3368cdb8929d23dfa61805f978b5f1ba5",
		"backup inspect":         "ecc5db494ffe8401cdaeab289d84f8a44b7d701f5584774a036cf185b002ab10",
		"backup merge":           "b161c3f508eba9a3fffc2f4a824de6d48b6dca21ec3d47758b31eee14de39e65",
		"backup merge-discard":   "48f213411c4676e6cd6af1400aef5029bf8d5307fd2ced4dff53b4cd1cc8c8ea",
		"backup merge-plan":      "f9ea98401a607bc62eb672b7e0cabc316a86c9d654d29b8389e9cfbad522f822",
		"backup status":          "2ad2eb8806b33a180826d201157e189c2488d7747aa9169bb79c42411118cb9e",
		"comment":                "230133520bafcd6cc7473c2db58288a7244e3b467bdce00000960f8181afe1d8",
		"comment create":         "32c0e6f9d033f0a50d98cf533271111a03ed03cbbf8b893e888576f8b9a1e732",
		"comment delete":         "1ae438d8870bb0da2d34306260131316228dc82b19c29e34215ac8e1eaf2c7d9",
		"comment list":           "41532cca3d73777d12d94bd8b0c4d2feec3f8a2b055221f29952b567f783ec4a",
		"comment pin":            "584f1e03045b9a85dd17fe5cf5ded482ae326f11033b3503b3ab31f8fc173d6a",
		"comment replies":        "47eb1c8fb6a5b8fc6a09e72bcb2d398a92016d9864217e692c63a288541cbd42",
		"comment reply":          "0a085ef08bacd2ebaf30871ca5c4c528fbbcc11ea82cb1cea3dd4332c528c045",
		"comment unpin":          "b09b2234e10e25962623f4b638c0f1fdd9a3a69477dee847a2e1037b12eb2975",
		"dev":                    "f3fbc09380185bb3d32d04b94712c5968c4e734067ceacb8ef00a3e6666e9a32",
		"dev reset":              "623279be28439377ce7e723b4351046fb758f7126e5c81db39a55646fc2621ce",
		"dev scenario":           "008cd54831c7ef825cf0e41d7370ba3026e19e9452aab49202d958cbc793b5d8",
		"dev seed":               "652cb437617e1c3e6d94d016bb75f325710cf092008899ccc6da9dcf13d7fdbb",
		"doctor":                 "6dd90e5897b539b1f8048d016a8e091ffdaf91392ae22902c256ab5dfcffe44e",
		"init":                   "1dd44779f406db30589ba2b0d248a147ab21a109273421425d95ebd1492edf8f",
		"link":                   "175714f000b50b006c997661226dd64008e57f4cafcaf6d07e9e8bc9715f8f84",
		"link unfurl":            "13e7f5d505af46600335b81a187428e8b6c6345420f6178a2181de981f39c823",
		"location":               "26b587f599def76745ccc2b145fdedd6443d1bd1b44c4e70f354ffba6be1472d",
		"location nearby":        "85c27f5fda0617bb0cc70a3d68d155e8521207e456adf61b2e20363cfbc36cf2",
		"location search":        "1fa980dd1019997d94d26f7baa5bb872b7d2359fb906ec619bebaa1355c6afd0",
		"media":                  "18d3cfca2f27ecd8ad62bc09d6ad8d88fdd36a6678331a8abfd5276e1a1ae489",
		"media add":              "41308df6ac040740cde176c81e2543d5eb2facaa5bfc383473e017a7aa81a2b9",
		"media get":              "6840a6e4053f84760e7a1a92efdd31c9a77965a502a2b9e41d57c9696e2618f5",
		"media list":             "6fc9294aa50466b8ccfa034dc9280b40efbcffa56c02fb76e1cb237446f2c69f",
		"media prune":            "f2719f2b81bbba418e0ad4527416b14e13b54cf7dc804c6f8e15ba773ecbc54d",
		"media rebuild-previews": "3b52ab84b61f18541b637bcd2248ea1be45967e350f9824cf3f839e5fcdf8e9e",
		"media verify":           "6440583fd5a92254431d459066dfad2c2376f6145816f68ea59b142a4789ef4e",
		"moment":                 "269d1c9a1436cf4f4fa839a0792ecf75fd9951eae0f6419b16daabb33ecf6056",
		"owner":                  "2530fef4d511ab7ac40281ee35487342a0200f57b78baaaa44dd49077ac56182",
		"owner init":             "e79729458feaa3e8d4599971244aefaffa11e9f57e67cd15ffafc48255ace663",
		"owner rotate-token":     "3e34b359dcfd5ced8c32ea28d9160947ab67a314d327fe9a3d213142bced6e0c",
		"owner set-password":     "b1d70c130537b43fe989dffcd474b11dbc4dcc0ec4bba5f4148448e1decca7c9",
		"owner status":           "ca4e6635f78403f88e14f77f225d3aad619123cf25c0e73b57f3daa61eb39d34",
		"post":                   "218eac009d9d9de2dc0c755b6837f147126ec7d7a1aacf7a3a5e2cb6f92732e7",
		"post create":            "4118a02d1b5c332dd65ebd1dffe46640a8cecd28f5331b9869851e4d02bb994c",
		"post delete":            "f053d4498c17f292e84b660b8b8abcb9dcb00daa9291433c0f5eb643233226e6",
		"post get":               "48690ea66ae796353be0b4cdd94f1fc0b4352abc9e2c834f7964ad2f79104a8e",
		"post list":              "6afa5e64cd9dee4cafd2f868cb9745ec45e45ee1845dbc81ff9d19ab35fb8739",
		"post pin":               "9bf12d6c579ea7841516e480defb99ded3dd4afcb8a23114ca97ab9a432800da",
		"post repost":            "fccb8d7b349ec397041cf9765e9565a2452dd7027cf83dc7d009bc3d5a872874",
		"post unpin":             "a821d706f49eab46b124084299862a0b9b325dc5a724b3d4dc0e91462eb159e8",
		"post unrepost":          "53ff90231dba9e088133db1b36e8cd93642383a6775d5503e1c3cec367e6d74f",
		"post update":            "86c35ea1c4410594f0e6b13b1f7d20e1d13e1cc606449824015740b463195ad1",
		"search":                 "6d87f554839c4d517bb1b4be03d897a5e7f571af0e5be6b8c63351f693dc31a1",
		"search query":           "b9b05f473ce5193c2c3970af09381c42934faaced45199f11751a1c08ac58e6a",
		"search rebuild":         "dd03cd5a216bca7e503f4f6d03d96c032db91e13f66ef8411045df7f6e37eb28",
		"serve":                  "3916e71755d97d99ae6c697b16380776e65ed083b1537a6272dfcdefe8916636",
		"site":                   "09043ff9807f0d5b49d4192cca5f0acec67b53014304a19e85e31f1275ad8da7",
		"site get":               "75894a8a50d52a6ed1b3d9d57232b7a89cea0b09127210fa712999079b264aa4",
		"site update":            "27c74502530077eb51c20a727a3ac4e7000928e03e7f1121683aae42a1018dd8",
		"user":                   "c41c66e7dd581b2b884c7db4b0c7d8bff7d0379839f6312db8c62132893dc58e",
		"user approve":           "3d352c04582b4209115e54fae864ad6a58b39c2f6cf1354224840e5a358733f8",
		"user identities":        "a29117dffc7f6ee2e562ddfe5df03fbd10f021b7557f2b301b9e5b16d072e81d",
		"user list":              "0c2784e8dc42021acf0951a51833f30c79093cc2096b89236ad61a014454b004",
		"user suggest":           "bdbd94abb523578a697bff57823b2435f3c1eb819b31006b04dffb7b1d97d854",
		"user suspend":           "afa8ad3db46186bac71f4e10fe898faa357505d70b5dc21f0a2a7aa7d174a0d4",
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
