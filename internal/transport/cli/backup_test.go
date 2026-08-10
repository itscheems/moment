package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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

func TestBackupInspectAPIUploadParityErrorsAndTimeout(t *testing.T) {
	application := momentTestApplication()
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(archivePath, []byte("PK\x03\x04payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	expires := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
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
		_, _ = io.WriteString(writer, `{"data":{"format_version":1,"archive_sha256":"abc","generation_id":"import-1","counts":{"entries":1,"comments":2,"media":3},"time_range":{},"media_bytes":4,"missing_alt":0,"disk":{"archive_bytes":10,"expanded_bytes":20,"staging_bytes":20,"pre_import_backup_bytes":1,"rollback_reserve_bytes":1,"safety_margin_bytes":2,"required_additional_bytes":24,"estimated_peak_footprint_bytes":35,"available_bytes":100,"space_ok":true},"replacement":{"counts":{"entries":0,"comments":0,"media":0},"media_bytes":0,"data_bytes":1},"warnings":[],"expires_at":"2026-08-11T02:00:00Z","staging_token":"stage-token"},"meta":{"request_id":"req_import"}}`)
	}))
	defer server.Close()
	output := execute(application, "", "--url", server.URL, "backup", "inspect", archivePath, "--json")
	if output.exit != ExitSuccess || output.stderr != "" {
		t.Fatalf("API inspect = %#v", output)
	}
	data := commandData(t, output)
	if data["staging_token"] != "stage-token" || data["expires_at"] != expires.Format(time.RFC3339) {
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
