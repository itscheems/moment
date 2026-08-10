package cli

import (
	"os"
	"strings"
	"testing"
)

func TestDevCLIConfirmationBootstrapJSONAndHelp(t *testing.T) {
	application := momentTestApplication()
	root := t.TempDir()
	for _, output := range []commandOutput{
		execute(application, "", "dev", "seed", "--data-dir", root, "--json"),
		execute(application, "", "dev", "seed", "--data-dir", root, "--no-input", "--json"),
	} {
		if output.exit != ExitInvalidInput {
			t.Fatalf("unconfirmed output = %#v", output)
		}
		if document := decodeOneDocument(t, output.stdout); document["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("unconfirmed document = %#v", document)
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("confirmation invoked handler: entries=%v err=%v", entries, err)
	}
	dryRun := execute(application, "", "dev", "scenario", "timeline-basic", "--data-dir", root, "--dry-run", "--json")
	if dryRun.exit != ExitSuccess || decodeOneDocument(t, dryRun.stdout)["ok"] != true {
		t.Fatalf("dry run = %#v", dryRun)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("dry run mutated bootstrap root: entries=%v err=%v", entries, err)
	}
	unsupported := execute(application, "", "dev", "seed", "--url", "http://127.0.0.1:9", "--yes", "--json")
	if unsupported.exit != ExitInvalidInput || decodeOneDocument(t, unsupported.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("unsupported = %#v", unsupported)
	}
	seeded := execute(application, "", "dev", "scenario", "search-cjk", "--data-dir", root, "--seed", "73", "--yes", "--no-input", "--json")
	if seeded.exit != ExitSuccess {
		t.Fatalf("seeded = %#v", seeded)
	}
	document := decodeOneDocument(t, seeded.stdout)
	data := document["data"].(map[string]any)
	if data["owner_token"] == "" || strings.Contains(seeded.stderr, data["owner_token"].(string)) {
		t.Fatalf("owner token delivery = %#v stderr=%q", data, seeded.stderr)
	}
	doctor := execute(application, "", "doctor", "--deep", "--data-dir", root, "--json")
	if doctor.exit != ExitSuccess || decodeOneDocument(t, doctor.stdout)["data"].(map[string]any)["status"] != "healthy" {
		t.Fatalf("doctor = %#v", doctor)
	}
	help := execute(application, "", "dev", "scenario", "--help")
	for _, required := range []string{"archive-roundtrip", "corruption-search-drift", "acceptance-scale", "--dry-run", "--seed", "strictly empty"} {
		if !strings.Contains(help.stdout, required) {
			t.Fatalf("scenario help missing %q:\n%s", required, help.stdout)
		}
	}
}

func TestDevResetRejectsUnmarkedRootEvenWithYes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+string(os.PathSeparator)+"canary", []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := execute(momentTestApplication(), "", "dev", "reset", "--data-dir", root, "--yes", "--json")
	if output.exit != ExitIntegrity || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DATA_INTEGRITY_FAILED" {
		t.Fatalf("reset = %#v", output)
	}
	if data, err := os.ReadFile(root + string(os.PathSeparator) + "canary"); err != nil || string(data) != "keep" {
		t.Fatalf("canary changed: %q %v", data, err)
	}
}
