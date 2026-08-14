package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/buildinfo"
)

func TestBackupMergeSelectionFileIsPrivateExactAndDryRunnable(t *testing.T) {
	document := mergeSessionDocument{
		Version: mergeSessionVersion, StagingToken: "private-token",
		Selection: archive.MergeSelection{
			PlanID: strings.Repeat("a", 64), ArchiveSHA256: strings.Repeat("b", 64),
			BaseGeneration: "generation", BaseAuthoritySHA256: strings.Repeat("c", 64),
			Items:      []archive.MergeSelectionItem{{Kind: archive.MergeEntry, ID: "01H00000000000000000000001", Action: archive.MergeKeepBoth}},
			SiteFields: []archive.MergeSiteSelection{{Field: "title", Action: archive.MergeKeepLocal}},
		},
	}
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := writePrivateMergeSession(path, document); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("selection mode=%v err=%v", info.Mode().Perm(), err)
	}
	loaded, err := readPrivateMergeSession(path)
	if err != nil || !reflect.DeepEqual(loaded, document) {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	output := execute(application, "", "--data-dir", "unused", "backup", "merge", path, "--dry-run", "--json")
	if output.exit != ExitSuccess || output.stderr != "" || !strings.Contains(output.stdout, `"dry_run":true`) || !strings.Contains(output.stdout, `"keep_both":1`) {
		t.Fatalf("dry-run output=%#v", output)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateMergeSession(path); err == nil {
		t.Fatal("world-readable selection was accepted")
	}
}

func TestBackupMergeSelectionRejectsUnknownDuplicateAndExistingOutput(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "selection.json")
	document := mergeSessionDocument{Version: mergeSessionVersion, StagingToken: "token", Selection: archive.MergeSelection{Items: []archive.MergeSelectionItem{}, SiteFields: []archive.MergeSiteSelection{}}}
	if err := writePrivateMergeSession(path, document); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateMergeSession(path, document); err == nil {
		t.Fatal("existing selection output was overwritten")
	}
	for name, value := range map[string]any{
		"unknown.json": map[string]any{"version": 1, "staging_token": "token", "selection": document.Selection, "extra": true},
	} {
		body, _ := json.Marshal(value)
		candidate := filepath.Join(directory, name)
		if err := os.WriteFile(candidate, append(body, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPrivateMergeSession(candidate); err == nil {
			t.Fatalf("invalid selection %s was accepted", name)
		}
	}
	duplicate := filepath.Join(directory, "duplicate.json")
	if err := os.WriteFile(duplicate, []byte(`{"version":1,"version":1,"staging_token":"token","selection":{"plan_id":"","archive_sha256":"","base_generation":"","base_authority_sha256":"","items":[],"site_fields":[]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateMergeSession(duplicate); err == nil {
		t.Fatal("duplicate selection keys were accepted")
	}
}
