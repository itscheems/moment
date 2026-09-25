package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
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

	preparationTimeout := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusGatewayTimeout)
		_, _ = io.WriteString(writer, `{"error":{"code":"EXPORT_PREPARATION_TIMEOUT","message":"archive preparation exceeded its time budget","details":{},"request_id":"req_export_timeout"}}`)
	}))
	preparationTimeoutOutput := execute(application, "", "--url", preparationTimeout.URL, "backup", "export", "--output", filepath.Join(t.TempDir(), "timeout.zip"), "--json")
	preparationTimeout.Close()
	if preparationTimeoutOutput.exit != ExitTimeout || preparationTimeoutOutput.stderr != "" ||
		decodeOneDocument(t, preparationTimeoutOutput.stdout)["error"].(map[string]any)["code"] != "EXPORT_PREPARATION_TIMEOUT" {
		t.Fatalf("preparation timeout = %#v", preparationTimeoutOutput)
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

func TestBackupImportArchiveDryRunHasNoDurableSideEffectsAndPathCommits(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if output := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", archivePath, "--json"); output.exit != ExitSuccess {
		t.Fatalf("source export=%#v", output)
	}
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	currentBefore, err := os.ReadFile(filepath.Join(dataDir, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	tracked := []string{
		filepath.Join(dataDir, "staging", "import"), filepath.Join(dataDir, "staging", "import-status"),
		filepath.Join(dataDir, "backups"), filepath.Join(dataDir, "generations"),
	}
	before := make(map[string][]string, len(tracked))
	for _, root := range tracked {
		before[root] = directoryNames(t, root)
	}
	serve, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	blocked := execute(application, "", "backup", "import", archivePath, "--data-dir", dataDir, "--yes", "--json")
	if blocked.exit != ExitConflict || decodeOneDocument(t, blocked.stdout)["error"].(map[string]any)["code"] != string(domain.CodeInstanceInUse) {
		t.Fatalf("server-running path import=%#v", blocked)
	}
	if err := serve.Release(); err != nil {
		t.Fatal(err)
	}
	for _, root := range tracked {
		if got := directoryNames(t, root); !reflect.DeepEqual(got, before[root]) {
			t.Fatalf("server-blocked path import changed %s", root)
		}
	}
	dryRun := execute(application, "", "backup", "import", archivePath, "--data-dir", dataDir, "--dry-run", "--json")
	if dryRun.exit != ExitSuccess || dryRun.stderr != "" {
		t.Fatalf("archive dry-run=%#v", dryRun)
	}
	data := commandData(t, dryRun)
	if data["dry_run"] != true || data["staging_token"] != nil || data["generation_id"] != nil || data["expires_at"] != nil {
		t.Fatalf("dry-run leaked staging authority=%#v", data)
	}
	currentAfter, _ := os.ReadFile(filepath.Join(dataDir, "CURRENT"))
	if !bytes.Equal(currentBefore, currentAfter) {
		t.Fatal("dry-run changed CURRENT")
	}
	for _, root := range tracked {
		if got := directoryNames(t, root); !reflect.DeepEqual(got, before[root]) {
			t.Fatalf("dry-run changed %s: before=%#v after=%#v", root, before[root], got)
		}
	}
	invalidArchive := filepath.Join(t.TempDir(), "invalid.zip")
	if err := os.WriteFile(invalidArchive, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := execute(application, "", "backup", "import", invalidArchive, "--data-dir", dataDir, "--dry-run", "--json")
	if invalid.exit != ExitIntegrity {
		t.Fatalf("invalid dry-run=%#v", invalid)
	}
	for _, root := range tracked {
		if got := directoryNames(t, root); !reflect.DeepEqual(got, before[root]) {
			t.Fatalf("failed dry-run changed %s: before=%#v after=%#v", root, before[root], got)
		}
	}
	withoutConfirmation := execute(application, "", "backup", "import", archivePath, "--data-dir", dataDir, "--json")
	if withoutConfirmation.exit != ExitInvalidInput {
		t.Fatalf("path import without confirmation=%#v", withoutConfirmation)
	}
	for _, root := range tracked {
		if got := directoryNames(t, root); !reflect.DeepEqual(got, before[root]) {
			t.Fatalf("unconfirmed path import changed %s", root)
		}
	}
	declined := execute(application, "n\n", "backup", "import", archivePath, "--data-dir", dataDir)
	if declined.exit != ExitConflict || declined.stdout != "" || !strings.Contains(declined.stderr, "Import preview\nArchive SHA-256:") ||
		!strings.Contains(declined.stderr, "Incoming:") || !strings.Contains(declined.stderr, "Replacing:") ||
		!strings.Contains(declined.stderr, "Disk:") || !strings.Contains(declined.stderr, "Warnings:") ||
		!strings.Contains(declined.stderr, "Replace the active Moment dataset with this exact archive? [y/N]") {
		t.Fatalf("scoped declined import=%#v", declined)
	}
	for _, root := range tracked {
		if got := directoryNames(t, root); !reflect.DeepEqual(got, before[root]) {
			t.Fatalf("declined scoped import changed %s", root)
		}
	}
	committed := execute(application, "", "backup", "import", archivePath, "--data-dir", dataDir, "--yes", "--json")
	if committed.exit != ExitSuccess || commandData(t, committed)["status"].(map[string]any)["phase"] != string(archive.CommitSucceeded) {
		t.Fatalf("path import=%#v", committed)
	}
}

func TestBackupImportRejectsArchiveFileSwapAfterScopedPrompt(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if output := execute(application, "", "backup", "export", "--data-dir", dataDir, "--output", archivePath, "--json"); output.exit != ExitSuccess {
		t.Fatalf("source export=%#v", output)
	}
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	currentBefore, err := os.ReadFile(filepath.Join(dataDir, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(archivePath), "replacement.zip")
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, body, 0o600); err != nil {
		t.Fatalf("prepare replacement: %v", err)
	}
	inputReader, inputWriter := io.Pipe()
	diagnostics := newPromptSignalWriter("Replace the active Moment dataset with this exact archive? [y/N]")
	var stdout bytes.Buffer
	exit := make(chan ExitCode, 1)
	go func() {
		exit <- application.Execute(context.Background(), []string{"backup", "import", archivePath, "--data-dir", dataDir}, Streams{
			In: inputReader, Out: &stdout, Err: diagnostics,
		})
	}()
	select {
	case <-diagnostics.signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("scope prompt did not arrive: %s", diagnostics.String())
	}
	if err := os.Rename(replacement, archivePath); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(inputWriter, "y\n"); err != nil {
		t.Fatal(err)
	}
	_ = inputWriter.Close()
	select {
	case code := <-exit:
		if code != ExitIntegrity {
			t.Fatalf("file-swap exit=%d stdout=%q stderr=%q", code, stdout.String(), diagnostics.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("file-swap import did not finish")
	}
	currentAfter, err := os.ReadFile(filepath.Join(dataDir, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(currentBefore, currentAfter) {
		t.Fatal("file-swap import changed CURRENT")
	}
}

type promptSignalWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	marker string
	once   sync.Once
	signal chan struct{}
}

func newPromptSignalWriter(marker string) *promptSignalWriter {
	return &promptSignalWriter{marker: marker, signal: make(chan struct{})}
}

func (writer *promptSignalWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	written, err := writer.buffer.Write(data)
	if strings.Contains(writer.buffer.String(), writer.marker) {
		writer.once.Do(func() { close(writer.signal) })
	}
	return written, err
}

func (writer *promptSignalWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func directoryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
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

func TestBackupImportAPIArchivePlanAndSingleRequestRestore(t *testing.T) {
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", "owner-test-token")
	archiveBody := []byte("PK\x03\x04payload")
	archivePath := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(archivePath, archiveBody, 0o600); err != nil {
		t.Fatal(err)
	}
	planEnvelope := validImportPlanAPIEnvelope(int64(len(archiveBody)))
	archiveDigest := sha256.Sum256(archiveBody)
	archiveSHA := hex.EncodeToString(archiveDigest[:])
	planEnvelope["data"].(map[string]any)["archive_sha256"] = archiveSHA
	planBody, _ := json.Marshal(planEnvelope)
	now := time.Date(2026, 8, 11, 7, 30, 0, 0, time.UTC)
	operationID := "import-20260811T073000000Z-0011223344556677"
	commitBody, _ := json.Marshal(map[string]any{
		"data": map[string]any{"status": map[string]any{
			"operation_id": operationID, "phase": "succeeded", "active_generation": operationID,
			"previous_generation": "gen-old", "new_generation": operationID,
			"backup_name": "pre-import.zip", "backup_verified": true,
			"rollback_attempted": false, "rollback_succeeded": false,
			"started_at": now.Format(time.RFC3339Nano), "updated_at": now.Format(time.RFC3339Nano), "completed_at": now.Format(time.RFC3339Nano),
		}},
		"meta": map[string]any{"request_id": "req_restore"},
	})
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil || !bytes.Equal(body, archiveBody) || request.Header.Get("Authorization") != "Bearer owner-test-token" || request.Header.Get("Content-Type") != "application/zip" {
			t.Errorf("upload path=%s body=%q headers=%#v err=%v", request.URL.Path, body, request.Header, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/owner/import/plan":
			_, _ = writer.Write(planBody)
		case "/api/v1/owner/import/restore":
			if request.Header.Get("X-Moment-Archive-SHA256") != archiveSHA {
				t.Errorf("restore expected sha header=%q", request.Header.Get("X-Moment-Archive-SHA256"))
			}
			_, _ = writer.Write(commitBody)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	blocked := execute(application, "", "--url", server.URL, "backup", "import", archivePath, "--no-input", "--json")
	if blocked.exit != ExitInvalidInput || calls.Load() != 0 || blocked.stderr != "" {
		t.Fatalf("no-input import reached plan service: output=%#v calls=%d", blocked, calls.Load())
	}
	plan := execute(application, "", "--url", server.URL, "backup", "import", archivePath, "--dry-run", "--json")
	if plan.exit != ExitSuccess || plan.stderr != "" || commandData(t, plan)["dry_run"] != true || strings.Contains(plan.stdout, "staging_token") {
		t.Fatalf("API plan=%#v", plan)
	}
	declined := execute(application, "n\n", "--url", server.URL, "backup", "import", archivePath)
	if declined.exit != ExitConflict || declined.stdout != "" || calls.Load() != 2 ||
		!strings.Contains(declined.stderr, "Archive SHA-256: "+archiveSHA) || !strings.Contains(declined.stderr, "Replace the active Moment dataset with this exact archive? [y/N]") {
		t.Fatalf("API declined scoped import=%#v calls=%d", declined, calls.Load())
	}
	restored := execute(application, "", "--url", server.URL, "backup", "import", archivePath, "--yes", "--json")
	if restored.exit != ExitSuccess || restored.stderr != "" || commandData(t, restored)["status"].(map[string]any)["active_generation"] != operationID || calls.Load() != 4 {
		t.Fatalf("API restore=%#v", restored)
	}
	mismatch := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/owner/import/plan" {
			_, _ = writer.Write(planBody)
			return
		}
		if request.URL.Path != "/api/v1/owner/import/restore" || request.Header.Get("X-Moment-Archive-SHA256") != archiveSHA {
			t.Errorf("mismatch restore request=%s sha=%q", request.URL.Path, request.Header.Get("X-Moment-Archive-SHA256"))
		}
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(writer, `{"error":{"code":"EXPORT_CHECKSUM_MISMATCH","message":"archive bytes changed after preview","details":{},"request_id":"req_mismatch"}}`)
	}))
	mismatched := execute(application, "", "--url", mismatch.URL, "backup", "import", archivePath, "--yes", "--json")
	mismatch.Close()
	if mismatched.exit != ExitIntegrity || decodeOneDocument(t, mismatched.stdout)["error"].(map[string]any)["code"] != string(domain.CodeExportChecksumMismatch) {
		t.Fatalf("API restore hash mismatch=%#v", mismatched)
	}
	malformedEnvelope := validImportPlanAPIEnvelope(int64(len(archiveBody)))
	malformedEnvelope["data"].(map[string]any)["staging_token"] = "must-not-be-accepted"
	malformedBody, _ := json.Marshal(malformedEnvelope)
	malformed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(malformedBody)
	}))
	failed := execute(application, "", "--url", malformed.URL, "backup", "import", archivePath, "--dry-run", "--json")
	malformed.Close()
	if failed.exit != ExitUnavailable || decodeOneDocument(t, failed.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
		t.Fatalf("malformed plan accepted=%#v", failed)
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
			data["counts"] = json.RawMessage(fmt.Sprintf(`{"entries":%v,"entries":%v,"comments":%v,"media":%v,"users":%v,"articles":%v,"article_drafts":%v,"identities":%v}`,
				counts["entries"], counts["entries"], counts["comments"], counts["media"], counts["users"],
				counts["articles"], counts["article_drafts"], counts["identities"]))
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

// TestImportCountsWireFieldsMatchTheStrictDecoder holds the server's encoding
// of archive.ImportCounts and this client's exact-field-set reader together.
//
// The reader rejects an unknown key and requires every declared one, so a count
// field added on the server without being declared here, an omitempty tag that
// drops a zero count off the wire, or a name declared here that the struct does
// not carry, each turn a healthy server into DEPENDENCY_INVALID_RESPONSE. The
// zero value is the interesting one to encode: it is where omitempty would bite.
func TestImportCountsWireFieldsMatchTheStrictDecoder(t *testing.T) {
	encoded, err := json.Marshal(archive.ImportCounts{})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := decodeExactJSONObject(encoded, importCountsFields...)
	if err != nil || len(fields) != len(importCountsFields) {
		t.Fatalf("archive.ImportCounts encodes as %s, which the strict reader answered %v with %d of %d fields",
			encoded, err, len(fields), len(importCountsFields))
	}
}

// TestDecodeImportInspectionAPIAcceptsServerEncodedCounts runs the real
// server-side type through the client's validation instead of a hand-written
// map, because a hand-written map is exactly what hides a client DTO that has
// drifted from the response it is reading.
func TestDecodeImportInspectionAPIAcceptsServerEncodedCounts(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	envelope := validImportInspectionAPIEnvelope(11, now.Add(importInspectionTokenTTL))
	data := envelope["data"].(map[string]any)
	incoming, err := json.Marshal(archive.ImportCounts{Entries: 1, Comments: 2, Media: 3, Users: 4, Identities: 5})
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := json.Marshal(archive.ImportCounts{Users: 1})
	if err != nil {
		t.Fatal(err)
	}
	data["counts"] = json.RawMessage(incoming)
	data["replacement"].(map[string]any)["counts"] = json.RawMessage(replaced)
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	inspection, _, err := decodeImportInspectionAPI(body, 11, now)
	if err != nil {
		t.Fatalf("server-encoded counts were rejected: %v", err)
	}
	if inspection.Counts != (archive.ImportCounts{Entries: 1, Comments: 2, Media: 3, Users: 4, Identities: 5}) ||
		inspection.Replacement.Counts != (archive.ImportCounts{Users: 1}) {
		t.Fatalf("decoded counts = %#v / %#v", inspection.Counts, inspection.Replacement.Counts)
	}
}

// TestImportPreviewRejectsAnAccountlessArchive pins the one account rule at the
// client boundary: every Moment authority has an owner, so an incoming archive
// that previews as zero accounts is describing a replacement no importer can
// produce, and the preview is not something to show an owner.
func TestImportPreviewRejectsAnAccountlessArchive(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	envelope := validImportInspectionAPIEnvelope(11, now.Add(importInspectionTokenTTL))
	counts := envelope["data"].(map[string]any)["counts"].(map[string]any)
	counts["users"] = int64(0)
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeImportInspectionAPI(body, 11, now); err == nil {
		t.Fatal("an import preview claiming no accounts was accepted")
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
			"counts": map[string]any{
				"entries": int64(1), "comments": int64(2), "media": int64(3),
				"users": int64(4), "articles": int64(0), "article_drafts": int64(0), "identities": int64(5),
			},
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
				"counts": map[string]any{
					"entries": int64(0), "comments": int64(0), "media": int64(0),
					"users": int64(1), "articles": int64(0), "article_drafts": int64(0), "identities": int64(0),
				},
				"media_bytes": int64(0), "data_bytes": replacementBytes,
			},
			"warnings": []string{}, "expires_at": expiresAt.UTC().Format(time.RFC3339Nano), "staging_token": token,
		},
		"meta": map[string]any{"request_id": "req_import"},
	}
}

func validImportPlanAPIEnvelope(archiveBytes int64) map[string]any {
	value := validImportInspectionAPIEnvelope(archiveBytes, time.Now().UTC().Add(importInspectionTokenTTL))
	data := value["data"].(map[string]any)
	delete(data, "generation_id")
	delete(data, "expires_at")
	delete(data, "staging_token")
	data["dry_run"] = true
	return value
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
