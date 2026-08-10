package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

func TestBackupExportDirectFileStdoutFlagsAndStableErrors(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	destination := filepath.Join(t.TempDir(), "moment-export.zip")
	fileOutput := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", destination, "--no-input", "--json")
	data := commandData(t, fileOutput)
	if data["format_version"] != float64(archive.FormatVersion) || data["path"] != destination || data["archive_sha256"] == "" {
		t.Fatalf("file result = %#v", data)
	}
	if err := archive.ValidateFile(destination); err != nil {
		t.Fatal(err)
	}
	if existing := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", destination, "--json"); existing.exit != ExitConflict || decodeOneDocument(t, existing.stdout)["error"].(map[string]any)["code"] != "OUTPUT_EXISTS" {
		t.Fatalf("no-clobber = %#v", existing)
	}
	if replaced := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", destination, "--overwrite", "--json"); replaced.exit != ExitSuccess {
		t.Fatalf("overwrite = %#v", replaced)
	}

	var stdout, stderr bytes.Buffer
	exit := application.Execute(context.Background(), []string{"backup", "export", "--data-dir", dataDir, "--stdout", "--no-input"}, Streams{Out: &stdout, Err: &stderr})
	if exit != ExitSuccess || stderr.Len() != 0 || !bytes.HasPrefix(stdout.Bytes(), []byte("PK\x03\x04")) {
		t.Fatalf("stdout export exit=%d bytes=%d stderr=%q", exit, stdout.Len(), stderr.String())
	}
	if _, err := zip.NewReader(bytes.NewReader(stdout.Bytes()), int64(stdout.Len())); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"backup", "export", "--data-dir", dataDir, "--json"},
		{"backup", "export", "--data-dir", dataDir, "--stdout", "--output", filepath.Join(t.TempDir(), "both.zip"), "--json"},
		{"backup", "export", "--data-dir", dataDir, "--stdout", "--json"},
		{"backup", "export", "--data-dir", dataDir, "--output", "a.zip", "--output", "b.zip", "--json"},
		{"backup", "export", "--data-dir", dataDir, "--stdout", "--stdout", "--json"},
	} {
		output := execute(application, "", args...)
		if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("args %q = %#v", args, output)
		}
	}

	fresh := execute(application, "", "backup", "export", "--data-dir", t.TempDir(), "--output", filepath.Join(t.TempDir(), "fresh.zip"), "--json")
	if fresh.exit != ExitIO || decodeOneDocument(t, fresh.stdout)["error"].(map[string]any)["code"] != "INSTANCE_NOT_READY" {
		t.Fatalf("fresh data dir = %#v", fresh)
	}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	unavailable := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", filepath.Join(parentFile, "export.zip"), "--json")
	if unavailable.exit != ExitIO || decodeOneDocument(t, unavailable.stdout)["error"].(map[string]any)["code"] != "OUTPUT_UNAVAILABLE" {
		t.Fatalf("unavailable output = %#v", unavailable)
	}
	help := execute(application, "", "backup", "export", "--help")
	if help.exit != ExitSuccess || !strings.Contains(help.stdout, "--stdout") || !strings.Contains(help.stdout, "--overwrite") || !strings.Contains(help.stdout, "raw ZIP bytes") || !strings.Contains(help.stdout, "outside the selected --data-dir") {
		t.Fatalf("backup help = %#v", help)
	}

	databaseBefore, err := os.ReadFile(runtimeState.Current.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	protected := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", runtimeState.Current.DatabasePath, "--overwrite", "--json")
	if protected.exit != ExitIO || decodeOneDocument(t, protected.stdout)["error"].(map[string]any)["code"] != "OUTPUT_UNAVAILABLE" {
		t.Fatalf("protected database output = %#v", protected)
	}
	databaseAfter, _ := os.ReadFile(runtimeState.Current.DatabasePath)
	if !bytes.Equal(databaseBefore, databaseAfter) {
		t.Fatal("protected database bytes changed")
	}
	alias := filepath.Join(t.TempDir(), "generation-alias")
	if err := os.Symlink(runtimeState.Current.Root, alias); err != nil {
		t.Fatal(err)
	}
	aliasOutput := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", filepath.Join(alias, "export.zip"), "--json")
	if aliasOutput.exit != ExitIO || decodeOneDocument(t, aliasOutput.stdout)["error"].(map[string]any)["code"] != "OUTPUT_UNAVAILABLE" {
		t.Fatalf("symlink alias output = %#v", aliasOutput)
	}
	partials, _ := filepath.Glob(filepath.Join(runtimeState.Current.Root, ".export.zip.partial.*"))
	if len(partials) != 0 {
		t.Fatalf("protected output left partials: %#v", partials)
	}
	caseAlias := strings.ToUpper(runtimeState.Root)
	if aliasInfo, err := os.Stat(caseAlias); err == nil {
		rootInfo, statErr := os.Stat(runtimeState.Root)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if os.SameFile(rootInfo, aliasInfo) && caseAlias != runtimeState.Root {
			caseOutput := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", filepath.Join(caseAlias, "case-alias.zip"), "--json")
			if caseOutput.exit != ExitIO || decodeOneDocument(t, caseOutput.stdout)["error"].(map[string]any)["code"] != "OUTPUT_UNAVAILABLE" {
				t.Fatalf("case-insensitive alias output = %#v", caseOutput)
			}
		}
	}
}

func TestBackupExportAPIParityHeaderValidationTruncationAndTimeout(t *testing.T) {
	_, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	if output := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", sourcePath, "--json"); output.exit != ExitSuccess {
		t.Fatalf("source export = %#v", output)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "owner-test-token")

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/owner/export" || request.Header.Get("Authorization") != "Bearer owner-test-token" || request.Body == nil {
			t.Errorf("request = %s %#v", request.URL.Path, request.Header)
		}
		writer.Header().Set("Content-Type", "application/zip")
		writer.Header().Set("X-Archive-Format", archive.FormatHeaderValue)
		_, _ = writer.Write(source)
	}))
	defer server.Close()
	apiPath := filepath.Join(t.TempDir(), "api.zip")
	apiOutput := execute(application, "", "--url", server.URL, "backup", "export", "--output", apiPath, "--json")
	if apiOutput.exit != ExitSuccess {
		t.Fatalf("API export = %#v", apiOutput)
	}
	apiBytes, _ := os.ReadFile(apiPath)
	if !bytes.Equal(apiBytes, source) || archive.ValidateFile(apiPath) != nil {
		t.Fatal("API output was not the exact validated v1 archive")
	}

	missingHeader := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		_, _ = writer.Write(source)
	}))
	var raw, diagnostics bytes.Buffer
	exit := application.Execute(context.Background(), []string{"--url", missingHeader.URL, "backup", "export", "--stdout"}, Streams{Out: &raw, Err: &diagnostics})
	missingHeader.Close()
	if exit != ExitUnavailable || raw.Len() != 0 || !strings.Contains(diagnostics.String(), "DEPENDENCY_INVALID_RESPONSE") {
		t.Fatalf("missing format header exit=%d stdout=%d stderr=%q", exit, raw.Len(), diagnostics.String())
	}

	for _, test := range []struct {
		name   string
		status int
		body   []byte
	}{
		{name: "no content", status: http.StatusNoContent},
		{name: "partial content", status: http.StatusPartialContent, body: source[:len(source)/2]},
		{name: "empty archive", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/zip")
				writer.Header().Set("X-Archive-Format", archive.FormatHeaderValue)
				writer.WriteHeader(test.status)
				if len(test.body) > 0 {
					_, _ = writer.Write(test.body)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			exit := application.Execute(context.Background(), []string{"--url", server.URL, "backup", "export", "--stdout"}, Streams{Out: &stdout, Err: &stderr})
			if exit != ExitUnavailable || stdout.Len() != 0 || !strings.Contains(stderr.String(), "DEPENDENCY_INVALID_RESPONSE") {
				t.Fatalf("raw response exit=%d stdout=%d stderr=%q", exit, stdout.Len(), stderr.String())
			}
		})
	}

	truncated := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		writer.Header().Set("X-Archive-Format", archive.FormatHeaderValue)
		writer.Header().Set("Content-Length", strconv.Itoa(len(source)+100))
		_, _ = writer.Write(source[:len(source)/2])
	}))
	truncatedPath := filepath.Join(t.TempDir(), "truncated.zip")
	truncatedOutput := execute(application, "", "--url", truncated.URL, "backup", "export", "--output", truncatedPath, "--json")
	truncated.Close()
	if truncatedOutput.exit != ExitUnavailable || decodeOneDocument(t, truncatedOutput.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
		t.Fatalf("truncated API = %#v", truncatedOutput)
	}
	if _, err := os.Stat(truncatedPath); !os.IsNotExist(err) {
		t.Fatal("truncated API left a final archive")
	}
	partials, _ := filepath.Glob(filepath.Join(filepath.Dir(truncatedPath), ".truncated.zip.partial.*"))
	if len(partials) != 0 {
		t.Fatalf("truncated API left partial files: %#v", partials)
	}

	invalid := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		writer.Header().Set("X-Archive-Format", archive.FormatHeaderValue)
		_, _ = writer.Write([]byte("not a ZIP"))
	}))
	invalidPath := filepath.Join(t.TempDir(), "invalid.zip")
	invalidOutput := execute(application, "", "--url", invalid.URL, "backup", "export", "--output", invalidPath, "--json")
	invalid.Close()
	if invalidOutput.exit != ExitIntegrity || decodeOneDocument(t, invalidOutput.stdout)["error"].(map[string]any)["code"] != "EXPORT_INVALID" {
		t.Fatalf("invalid API archive = %#v", invalidOutput)
	}
	for name, body := range map[string][]byte{
		"unsafe member":    malformedBackupZIP(t, "../escape"),
		"duplicate member": malformedBackupZIP(t, "same", "same"),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/zip")
				writer.Header().Set("X-Archive-Format", archive.FormatHeaderValue)
				_, _ = writer.Write(body)
			}))
			defer server.Close()
			directory := t.TempDir()
			destination := filepath.Join(directory, "malicious.zip")
			output := execute(application, "", "--url", server.URL, "backup", "export", "--output", destination, "--json")
			if output.exit != ExitIntegrity || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "EXPORT_INVALID" {
				t.Fatalf("malicious archive = %#v", output)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("malicious API archive left a final file")
			}
			partials, _ := filepath.Glob(filepath.Join(directory, ".malicious.zip.partial.*"))
			if len(partials) != 0 {
				t.Fatalf("malicious API archive left partials: %#v", partials)
			}
		})
	}

	timeout := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() }))
	timeoutOutput := execute(application, "", "--url", timeout.URL, "backup", "export", "--output", filepath.Join(t.TempDir(), "timeout.zip"), "--timeout", "10ms", "--json")
	timeout.Close()
	if timeoutOutput.exit != ExitTimeout || decodeOneDocument(t, timeoutOutput.stdout)["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("API timeout = %#v", timeoutOutput)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	unavailable := execute(application, "", "--url", closedURL, "backup", "export", "--output", filepath.Join(t.TempDir(), "offline.zip"), "--json")
	if unavailable.exit != ExitUnavailable || decodeOneDocument(t, unavailable.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("offline API = %#v", unavailable)
	}

	integrity := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(writer, `{"error":{"code":"EXPORT_INVALID","message":"archive authority is invalid","details":{"kind":"reference"},"request_id":"req_export"}}`)
	}))
	defer integrity.Close()
	integrityPath := filepath.Join(t.TempDir(), "integrity.zip")
	integrityOutput := execute(application, "", "--url", integrity.URL, "backup", "export", "--output", integrityPath, "--json")
	if integrityOutput.exit != ExitIntegrity || decodeOneDocument(t, integrityOutput.stdout)["error"].(map[string]any)["code"] != "EXPORT_INVALID" {
		t.Fatalf("API integrity parity = %#v", integrityOutput)
	}
	if _, err := os.Stat(integrityPath); !os.IsNotExist(err) {
		t.Fatal("API integrity failure left a final file")
	}
}

func TestBackupExportDestinationFaultMappingAndDomainPriority(t *testing.T) {
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		writer.Header().Set("X-Archive-Format", archive.FormatHeaderValue)
		_, _ = writer.Write(bytes.Repeat([]byte("x"), 1024))
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	invocation := Invocation{target: apiTarget{baseURL: baseURL}}
	_, err = downloadExport(context.Background(), invocation, failingBackupWriter{})
	mapped := backupCLIError(err)
	var commandErr *CommandError
	if !errors.As(mapped, &commandErr) || commandErr.Code != "OUTPUT_UNAVAILABLE" || commandErr.ExitCode != ExitIO {
		t.Fatalf("destination mapping = %#v (%v)", commandErr, err)
	}

	primary := errors.Join(
		domain.NewError(domain.CodeExportInvalid, "invalid archive authority", nil),
		archive.ErrDestinationUnavailable,
	)
	mapped = backupCLIError(primary)
	commandErr = nil
	if !errors.As(mapped, &commandErr) || commandErr.Code != string(domain.CodeExportInvalid) || commandErr.ExitCode != ExitIntegrity {
		t.Fatalf("domain priority = %#v", commandErr)
	}
}

func TestBackupInspectDirectJSONHelpAndActiveState(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if output := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", archivePath, "--json"); output.exit != ExitSuccess {
		t.Fatalf("source export = %#v", output)
	}
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	result := execute(application, "", "backup", "inspect", archivePath, "--data-dir", dataDir, "--timeout", "30s", "--json")
	if result.exit != ExitSuccess || result.stderr != "" {
		t.Fatalf("inspect = %#v", result)
	}
	data := commandData(t, result)
	if data["format_version"] != float64(archive.FormatVersion) || data["staging_token"] == "" || data["generation_id"] == "" {
		t.Fatalf("inspection data = %#v", data)
	}
	disk := data["disk"].(map[string]any)
	if disk["space_ok"] != true || disk["required_additional_bytes"].(float64) <= 0 {
		t.Fatalf("disk = %#v", disk)
	}
	again := execute(application, "", "backup", "inspect", archivePath, "--data-dir", dataDir, "--json")
	if again.exit != ExitConflict || decodeOneDocument(t, again.stdout)["error"].(map[string]any)["code"] != "IMPORT_IN_PROGRESS" {
		t.Fatalf("active inspection = %#v", again)
	}
	help := execute(application, "", "backup", "inspect", "--help")
	for _, expected := range []string{"<archive.zip>", "restartable staging", "one-time staging token", "never changes CURRENT", "moment backup inspect ./moment-export.zip"} {
		if help.exit != ExitSuccess || !strings.Contains(help.stdout, expected) {
			t.Fatalf("help missing %q: %#v", expected, help)
		}
	}
}

func TestBackupImportDirectSafetySuccessReplayAndHelp(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if output := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", archivePath, "--json"); output.exit != ExitSuccess {
		t.Fatalf("source export=%#v", output)
	}
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	inspected := execute(application, "", "backup", "inspect", archivePath, "--data-dir", dataDir, "--json")
	token := commandData(t, inspected)["staging_token"].(string)
	withoutYes := execute(application, "", "backup", "import", "--staging-token", token, "--data-dir", dataDir, "--json")
	if withoutYes.exit != ExitInvalidInput || decodeOneDocument(t, withoutYes.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("missing confirmation=%#v", withoutYes)
	}
	noInput := execute(application, "", "backup", "import", "--staging-token", token, "--data-dir", dataDir, "--no-input")
	if noInput.exit != ExitInvalidInput {
		t.Fatalf("no-input confirmation=%#v", noInput)
	}
	committed := execute(application, "", "backup", "import", "--staging-token", token, "--data-dir", dataDir, "--yes", "--json")
	if committed.exit != ExitSuccess || committed.stderr != "" || strings.Contains(committed.stdout, token) {
		t.Fatalf("direct import=%#v", committed)
	}
	data := commandData(t, committed)
	status := data["status"].(map[string]any)
	if status["phase"] != string(archive.CommitSucceeded) || status["active_generation"] != status["new_generation"] || status["backup_verified"] != true {
		t.Fatalf("direct commit status=%#v", status)
	}
	queried := execute(application, "", "backup", "status", "--data-dir", dataDir, "--json")
	if queried.exit != ExitSuccess || queried.stderr != "" || commandData(t, queried)["phase"] != string(archive.CommitSucceeded) {
		t.Fatalf("direct durable status=%#v", queried)
	}
	replayed := execute(application, "", "backup", "import", "--staging-token", token, "--data-dir", dataDir, "--yes", "--json")
	if replayed.exit != ExitConflict || decodeOneDocument(t, replayed.stdout)["error"].(map[string]any)["code"] != string(domain.CodeImportStagingInvalid) {
		t.Fatalf("replay=%#v", replayed)
	}
	help := execute(application, "", "backup", "import", "--help")
	for _, expected := range []string{"--staging-token", "--yes", "Direct mode requires the server to be stopped", "automatically roll back"} {
		if help.exit != ExitSuccess || !strings.Contains(help.stdout, expected) {
			t.Fatalf("help missing %q: %#v", expected, help)
		}
	}
	statusHelp := execute(application, "", "backup", "status", "--help")
	for _, expected := range []string{"latest durable import operation", "No staging token", "Read-only", "moment backup status"} {
		if statusHelp.exit != ExitSuccess || !strings.Contains(statusHelp.stdout, expected) {
			t.Fatalf("status help missing %q: %#v", expected, statusHelp)
		}
	}
}

func TestBackupImportAPIStrictSuccessAndMalformedResponse(t *testing.T) {
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	now := time.Date(2026, 8, 11, 7, 0, 0, 0, time.UTC)
	operationID := "import-20260811T070000000Z-0011223344556677"
	valid := map[string]any{
		"data": map[string]any{"status": map[string]any{
			"operation_id": operationID, "phase": "succeeded", "active_generation": operationID,
			"previous_generation": "gen-old", "new_generation": operationID,
			"backup_name": "pre-import-20260811T070000000Z-gen-old.zip", "backup_verified": true,
			"rollback_attempted": false, "rollback_succeeded": false,
			"started_at": now.Format(time.RFC3339Nano), "updated_at": now.Format(time.RFC3339Nano), "completed_at": now.Format(time.RFC3339Nano),
		}},
		"meta": map[string]any{"request_id": "req_import_commit"},
	}
	validBody, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/owner/import/commit" || request.Header.Get("Authorization") != "Bearer owner-test-token" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request=%s %s %#v", request.Method, request.URL.Path, request.Header)
		}
		var body map[string]string
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&body); err != nil || len(body) != 1 || body["staging_token"] != "stage-token" {
			t.Errorf("request body=%#v err=%v", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(validBody)
	}))
	defer server.Close()
	output := execute(application, "", "--url", server.URL, "backup", "import", "--staging-token", "stage-token", "--yes", "--json")
	if output.exit != ExitSuccess || output.stderr != "" {
		t.Fatalf("API import=%#v", output)
	}
	data := commandData(t, output)
	if data["status"].(map[string]any)["active_generation"] != operationID || decodeOneDocument(t, output.stdout)["meta"].(map[string]any)["request_id"] != "req_import_commit" {
		t.Fatalf("API output=%s", output.stdout)
	}

	malformedBodies := []string{
		`{"data":{"status":{}},"meta":{"request_id":"req_x"}}`,
		string(validBody[:len(validBody)-1]),
		strings.Replace(string(validBody), `"backup_verified":true`, `"backup_verified":false`, 1),
		strings.Replace(string(validBody), `"phase":"succeeded"`, `"phase":"verifying"`, 1),
		strings.Replace(string(validBody), `"completed_at":"`+now.Format(time.RFC3339Nano)+`"`, `"completed_at":"2000-01-01T00:00:00Z"`, 1),
	}
	for _, malformed := range malformedBodies {
		fake := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, malformed)
		}))
		failed := execute(application, "", "--url", fake.URL, "backup", "import", "--staging-token", "stage-token", "--yes", "--json")
		fake.Close()
		if failed.exit != ExitUnavailable || decodeOneDocument(t, failed.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" || failed.stderr != "" {
			t.Fatalf("malformed response accepted: %#v body=%s", failed, malformed)
		}
	}
}

func TestBackupStatusAPIStrictParityAndMalformedResponse(t *testing.T) {
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	operationID := "import-20260811T090000000Z-0011223344556677"
	valid := map[string]any{
		"data": map[string]any{
			"operation_id": operationID, "phase": "succeeded", "active_generation": operationID,
			"previous_generation": "gen-old", "new_generation": operationID,
			"backup_name": "pre-import-20260811T090000000Z-gen-old.zip", "backup_verified": true,
			"rollback_attempted": false, "rollback_succeeded": false,
			"started_at": now.Format(time.RFC3339Nano), "updated_at": now.Format(time.RFC3339Nano), "completed_at": now.Format(time.RFC3339Nano),
		},
		"meta": map[string]any{"request_id": "req_import_status"},
	}
	validBody, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/owner/import/status" ||
			request.Header.Get("Authorization") != "Bearer owner-test-token" || request.Body != http.NoBody {
			t.Errorf("status request=%s %s %#v", request.Method, request.URL.Path, request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(validBody)
	}))
	output := execute(application, "", "--url", server.URL, "backup", "status", "--json")
	server.Close()
	if output.exit != ExitSuccess || output.stderr != "" || commandData(t, output)["active_generation"] != operationID ||
		decodeOneDocument(t, output.stdout)["meta"].(map[string]any)["request_id"] != "req_import_status" {
		t.Fatalf("API status=%#v", output)
	}

	validText := string(validBody)
	malformedBodies := []string{
		`{}`,
		strings.Replace(validText, `"meta":`, `"extra":1,"meta":`, 1),
		strings.Replace(validText, `"operation_id":`, `"extra":1,"operation_id":`, 1),
		strings.Replace(validText, `"phase":"succeeded"`, `"phase":"unknown"`, 1),
		strings.Replace(validText, `,"completed_at":"`+now.Format(time.RFC3339Nano)+`"`, ``, 1),
		strings.Replace(validText, `"active_generation":"`+operationID+`"`, `"active_generation":"gen-old"`, 1),
		strings.Replace(validText, `"backup_verified":true`, `"backup_verified":false`, 1),
		strings.Replace(validText, `"operation_id":"`+operationID+`"`, `"operation_id":"`+operationID+`","operation_id":"`+operationID+`"`, 1),
	}
	for _, malformed := range malformedBodies {
		fake := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, malformed)
		}))
		failed := execute(application, "", "--url", fake.URL, "backup", "status", "--json")
		fake.Close()
		if failed.exit != ExitUnavailable || failed.stderr != "" ||
			decodeOneDocument(t, failed.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
			t.Fatalf("malformed status accepted: %#v body=%s", failed, malformed)
		}
	}
}

func TestBackupInspectAPIUploadParityErrorsAndTimeout(t *testing.T) {
	application := momentTestApplication()
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	archiveBody := []byte("PK\x03\x04payload")
	if err := os.WriteFile(archivePath, archiveBody, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	expires := time.Now().UTC().Truncate(time.Millisecond).Add(importInspectionTokenTTL)
	validEnvelope := validImportInspectionAPIEnvelope(int64(len(archiveBody)), expires)
	wantToken := validEnvelope["data"].(map[string]any)["staging_token"].(string)
	validBody, err := json.Marshal(validEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/owner/import/inspect" || request.Header.Get("Authorization") != "Bearer owner-test-token" || request.Header.Get("Content-Type") != "application/zip" {
			t.Errorf("request = %s %s %#v", request.Method, request.URL.Path, request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "PK\x03\x04payload" {
			t.Errorf("body = %q err=%v", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write(validBody)
	}))
	defer server.Close()
	output := execute(application, "", "--url", server.URL, "backup", "inspect", archivePath, "--json")
	if output.exit != ExitSuccess || output.stderr != "" {
		t.Fatalf("API inspect = %#v", output)
	}
	data := commandData(t, output)
	if data["staging_token"] != wantToken || data["expires_at"] != expires.Format(time.RFC3339Nano) {
		t.Fatalf("API data = %#v", data)
	}

	integrity := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(writer, `{"error":{"code":"EXPORT_CHECKSUM_MISMATCH","message":"checksum failed","details":{"member":"data/site.json"},"request_id":"req"}}`)
	}))
	failed := execute(application, "", "--url", integrity.URL, "backup", "inspect", archivePath, "--json")
	integrity.Close()
	if failed.exit != ExitIntegrity || decodeOneDocument(t, failed.stdout)["error"].(map[string]any)["code"] != "EXPORT_CHECKSUM_MISMATCH" {
		t.Fatalf("API integrity = %#v", failed)
	}

	timeout := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	timedOut := execute(application, "", "--url", timeout.URL, "backup", "inspect", archivePath, "--timeout", "10ms", "--json")
	timeout.Close()
	if timedOut.exit != ExitTimeout || decodeOneDocument(t, timedOut.stdout)["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("API timeout = %#v", timedOut)
	}

	symlink := filepath.Join(t.TempDir(), "archive-link.zip")
	if err := os.Symlink(archivePath, symlink); err != nil {
		t.Fatal(err)
	}
	invalid := execute(application, "", "--url", server.URL, "backup", "inspect", symlink, "--json")
	if invalid.exit != ExitInvalidInput || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("symlink input = %#v", invalid)
	}
}

func TestBackupInspectAPIRejectsMalformedSuccessEnvelopes(t *testing.T) {
	application := momentTestApplication()
	archiveBody := []byte("PK\x03\x04payload")
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(archivePath, archiveBody, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	tests := []struct {
		name        string
		status      int
		contentType string
		mutate      func(map[string]any)
		raw         func(map[string]any) []byte
	}{
		{name: "wrong success status", status: http.StatusOK},
		{name: "wrong content type", contentType: "text/plain"},
		{name: "missing top field", mutate: func(value map[string]any) { delete(value, "meta") }},
		{name: "empty metadata", mutate: func(value map[string]any) { value["meta"] = map[string]any{} }},
		{name: "unknown envelope field", mutate: func(value map[string]any) { value["extra"] = true }},
		{name: "unknown inspection field", mutate: func(value map[string]any) { value["data"].(map[string]any)["extra"] = true }},
		{name: "unknown metadata field", mutate: func(value map[string]any) { value["meta"].(map[string]any)["extra"] = true }},
		{name: "missing required inspection field", mutate: func(value map[string]any) { delete(value["data"].(map[string]any), "disk") }},
		{name: "null warnings", mutate: func(value map[string]any) { value["data"].(map[string]any)["warnings"] = nil }},
		{name: "invalid archive hash", mutate: func(value map[string]any) { value["data"].(map[string]any)["archive_sha256"] = strings.Repeat("A", 64) }},
		{name: "invalid generation ID", mutate: func(value map[string]any) { value["data"].(map[string]any)["generation_id"] = "import-1" }},
		{name: "negative count", mutate: func(value map[string]any) { value["data"].(map[string]any)["counts"].(map[string]any)["entries"] = -1 }},
		{name: "incomplete time range", mutate: func(value map[string]any) {
			delete(value["data"].(map[string]any)["time_range"].(map[string]any), "to")
		}},
		{name: "non UTC expiry", mutate: func(value map[string]any) { value["data"].(map[string]any)["expires_at"] = "2026-08-11T10:00:00+08:00" }},
		{name: "expired token", mutate: func(value map[string]any) { value["data"].(map[string]any)["expires_at"] = "2000-01-01T00:00:00Z" }},
		{name: "far future token", mutate: func(value map[string]any) {
			value["data"].(map[string]any)["expires_at"] = time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
		}},
		{name: "invalid staging token", mutate: func(value map[string]any) { value["data"].(map[string]any)["staging_token"] = "stage-token" }},
		{name: "inconsistent disk formula", mutate: func(value map[string]any) {
			value["data"].(map[string]any)["disk"].(map[string]any)["required_additional_bytes"] = 1
		}},
		{name: "empty request ID", mutate: func(value map[string]any) { value["meta"].(map[string]any)["request_id"] = "" }},
		{name: "duplicate top field", raw: func(value map[string]any) []byte {
			data, _ := json.Marshal(value["data"])
			meta, _ := json.Marshal(value["meta"])
			return []byte(fmt.Sprintf(`{"data":%s,"data":%s,"meta":%s}`, data, data, meta))
		}},
		{name: "duplicate nested field", raw: func(value map[string]any) []byte {
			data := value["data"].(map[string]any)
			counts := data["counts"].(map[string]any)
			data["counts"] = json.RawMessage(fmt.Sprintf(`{"entries":%v,"entries":%v,"comments":%v,"media":%v}`, counts["entries"], counts["entries"], counts["comments"], counts["media"]))
			body, _ := json.Marshal(value)
			return body
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validImportInspectionAPIEnvelope(int64(len(archiveBody)), time.Now().UTC().Add(importInspectionTokenTTL))
			if test.mutate != nil {
				test.mutate(value)
			}
			var body []byte
			if test.raw != nil {
				body = test.raw(value)
			} else {
				var err error
				body, err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
			}
			status := test.status
			if status == 0 {
				status = http.StatusCreated
			}
			contentType := test.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", contentType)
				writer.WriteHeader(status)
				_, _ = writer.Write(body)
			}))
			output := execute(application, "", "--url", server.URL, "backup", "inspect", archivePath, "--json")
			server.Close()
			if output.exit != ExitUnavailable || output.stderr != "" {
				t.Fatalf("malformed API response = %#v", output)
			}
			document := decodeOneDocument(t, output.stdout)
			if document["ok"] != false || document["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
				t.Fatalf("error document = %#v", document)
			}
		})
	}
}

func TestDecodeImportInspectionAPIExpiryBounds(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	tests := []struct {
		name      string
		expiresAt time.Time
		wantError bool
	}{
		{name: "past", expiresAt: now.Add(-time.Nanosecond), wantError: true},
		{name: "exact now", expiresAt: now, wantError: true},
		{name: "strictly future", expiresAt: now.Add(time.Nanosecond)},
		{name: "default TTL", expiresAt: now.Add(importInspectionTokenTTL)},
		{name: "clock skew upper bound", expiresAt: now.Add(importInspectionTokenTTL + importInspectionClockSkew)},
		{name: "beyond upper bound", expiresAt: now.Add(importInspectionTokenTTL + importInspectionClockSkew + time.Nanosecond), wantError: true},
		{name: "far future", expiresAt: now.Add(24 * time.Hour), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(validImportInspectionAPIEnvelope(11, test.expiresAt))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = decodeImportInspectionAPI(body, 11, now)
			if (err != nil) != test.wantError {
				t.Fatalf("expiry %s error = %v, wantError=%t", test.expiresAt.Format(time.RFC3339Nano), err, test.wantError)
			}
		})
	}
}

func validImportInspectionAPIEnvelope(archiveBytes int64, expiresAt time.Time) map[string]any {
	const safetyMargin = int64(2 << 30)
	const replacementBytes = int64(1)
	const expandedBytes = int64(20)
	const mediaBytes = int64(4)
	stagingBytes := mediaBytes + 2*(expandedBytes-mediaBytes)
	requiredBytes := stagingBytes + 2*replacementBytes + safetyMargin
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	return map[string]any{
		"data": map[string]any{
			"format_version": archive.FormatVersion, "archive_sha256": strings.Repeat("a", 64),
			"generation_id": "import-20260811T020000000Z-0102030405060708",
			"counts":        map[string]any{"entries": int64(1), "comments": int64(2), "media": int64(3)},
			"time_range": map[string]any{
				"from": "2026-08-10T01:02:03Z", "to": "2026-08-11T01:02:03.123Z",
			},
			"media_bytes": mediaBytes, "missing_alt": int64(0),
			"disk": map[string]any{
				"archive_bytes": archiveBytes, "expanded_bytes": expandedBytes, "staging_bytes": stagingBytes,
				"pre_import_backup_bytes": replacementBytes, "rollback_reserve_bytes": replacementBytes,
				"safety_margin_bytes": safetyMargin, "required_additional_bytes": requiredBytes,
				"estimated_peak_footprint_bytes": archiveBytes + replacementBytes + requiredBytes,
				"available_bytes":                requiredBytes + 100, "space_ok": true,
			},
			"replacement": map[string]any{
				"counts":      map[string]any{"entries": int64(0), "comments": int64(0), "media": int64(0)},
				"media_bytes": int64(0), "data_bytes": replacementBytes,
			},
			"warnings": []string{}, "expires_at": expiresAt.UTC().Format(time.RFC3339Nano), "staging_token": token,
		},
		"meta": map[string]any{"request_id": "req_import"},
	}
}

type failingBackupWriter struct{}

func (failingBackupWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func malformedBackupZIP(t *testing.T, names ...string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, name := range names {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
