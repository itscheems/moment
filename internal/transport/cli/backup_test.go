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

	"github.com/moment/moment/internal/archive"
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
