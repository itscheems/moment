package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestDecodeMergeInspectionAPIAllowsOnlyExpectedNullBlockReason(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	authority := archive.PortableAuthority{Site: sqlitestore.ArchiveSite{
		ProfileUserID: "owner", Username: "owner", DisplayName: "Moment", Title: "Moment",
		Timezone: "UTC", CommentsEnabled: true,
	}}
	plan, err := archive.ComparePortableAuthorities(archive.MergePlanInput{
		ArchiveSHA256: strings.Repeat("a", 64), BaseGeneration: "generation",
		ExpiresAt: now.Add(importInspectionTokenTTL), Local: authority, Incoming: authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	review := archive.MergeReview{
		IncomingOnly: archive.MergeReviewBucket{Items: []archive.MergeReviewItem{}},
		LocalOnly:    archive.MergeReviewBucket{Items: []archive.MergeReviewItem{}},
		Conflicts:    archive.MergeReviewBucket{Items: []archive.MergeReviewItem{}},
		SiteFields:   make([]archive.MergeReviewSiteField, len(plan.SiteFields)),
	}
	for index, field := range plan.SiteFields {
		review.SiteFields[index] = archive.MergeReviewSiteField{
			Field: field.Field, Classification: field.Classification,
			LocalValue: field.LocalValue, IncomingValue: field.IncomingValue,
		}
	}
	inspection := archive.MergeInspection{
		Plan: plan, Review: review, Warnings: []string{}, MergeAllowed: true,
		ExpiresAt: plan.ExpiresAt, StagingToken: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
	body, err := json.Marshal(map[string]any{
		"data": inspection,
		"meta": map[string]any{"request_id": "req_merge_null"},
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := decodeMergeInspectionAPI(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.MergeAllowed || decoded.MergeBlockedReason != nil {
		t.Fatalf("decoded inspection = %#v", decoded)
	}
	if _, err := decodeExactJSONObjectAllowNull([]byte(`{"allowed":null,"rejected":null}`), []string{"allowed"}, "allowed", "rejected"); err == nil {
		t.Fatal("unexpected nullable field was accepted")
	}
}

func TestBackupMergeDomainConflictsUseConflictExit(t *testing.T) {
	for _, test := range []struct {
		code       domain.Code
		suggestion string
	}{
		{code: domain.CodeImportMergePlanStale, suggestion: "moment backup merge-plan"},
	} {
		t.Run(string(test.code), func(t *testing.T) {
			err := backupImportCLIError(domain.NewError(test.code, "merge conflict", map[string]any{"safe": true}))
			var commandErr *CommandError
			if !errors.As(err, &commandErr) {
				t.Fatalf("error = %T %v", err, err)
			}
			if commandErr.Code != string(test.code) || commandErr.ExitCode != ExitConflict ||
				!strings.Contains(commandErr.Suggestion, test.suggestion) || !reflect.DeepEqual(commandErr.Details, map[string]any{"safe": true}) {
				t.Fatalf("command error = %#v", commandErr)
			}
		})
	}
}

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

func TestBackupMergeDiscardAPIUsesPrivateCredentialAndStrictJSON(t *testing.T) {
	document := mergeSessionDocument{
		Version: mergeSessionVersion, StagingToken: "private-token",
		Selection: archive.MergeSelection{
			PlanID: strings.Repeat("a", 64), ArchiveSHA256: strings.Repeat("b", 64),
			BaseGeneration: "generation", BaseAuthoritySHA256: strings.Repeat("c", 64),
			Items: []archive.MergeSelectionItem{}, SiteFields: []archive.MergeSiteSelection{},
		},
	}
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := writePrivateMergeSession(path, document); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/owner/import/merge/discard" ||
			request.Header.Get("Authorization") != "Bearer owner-test-token" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request=%s %s %#v", request.Method, request.URL.Path, request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != `{"staging_token":"private-token"}` {
			t.Errorf("request body=%q err=%v", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"data":{"discarded":true},"meta":{"request_id":"req_discard"}}`)
	}))
	defer server.Close()
	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	output := execute(application, "", "--url", server.URL, "backup", "merge-discard", path, "--json")
	if output.exit != ExitSuccess || output.stderr != "" {
		t.Fatalf("discard output=%#v", output)
	}
	data := commandData(t, output)
	if data["discarded"] != true || data["plan_id"] != document.Selection.PlanID ||
		decodeOneDocument(t, output.stdout)["meta"].(map[string]any)["request_id"] != "req_discard" {
		t.Fatalf("discard document=%s", output.stdout)
	}
}
