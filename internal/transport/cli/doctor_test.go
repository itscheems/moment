package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/runtimefs"
)

func TestDoctorCLIDirectHealthyFailureAndHelp(t *testing.T) {
	application := momentTestApplication()
	dataDir := t.TempDir()
	initialized := execute(application, "", "owner", "init", "--data-dir", dataDir, "--json")
	if initialized.exit != ExitSuccess {
		t.Fatalf("owner init = %#v", initialized)
	}
	var credential struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(initialized.stdout), &credential); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"doctor", "--data-dir", dataDir, "--json"},
		{"doctor", "--deep", "--data-dir", dataDir, "--json"},
	} {
		output := execute(application, "", args...)
		if output.exit != ExitSuccess {
			t.Fatalf("%v = %#v", args, output)
		}
		document := decodeOneDocument(t, output.stdout)
		data := document["data"].(map[string]any)
		if document["ok"] != true || data["schema_version"] != float64(1) || data["status"] != "healthy" || strings.Contains(output.stdout+output.stderr, credential.Data.Token) {
			t.Fatalf("%v document=%#v stderr=%q", args, document, output.stderr)
		}
	}

	if err := os.WriteFile(filepath.Join(dataDir, "CURRENT"), []byte("../CANARY_PATH_ESCAPE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := execute(application, "", "doctor", "--deep", "--data-dir", dataDir, "--json")
	if failed.exit != ExitIntegrity {
		t.Fatalf("failure = %#v", failed)
	}
	document := decodeOneDocument(t, failed.stdout)
	if document["ok"] != false || document["error"].(map[string]any)["code"] != "DATA_INTEGRITY_FAILED" || document["data"] == nil || strings.Contains(failed.stdout+failed.stderr, "CANARY_PATH_ESCAPE") {
		t.Fatalf("failure document=%#v stderr=%q", document, failed.stderr)
	}

	help := execute(application, "", "doctor", "--help")
	for _, required := range []string{"--deep", "local/direct only", "Read-only", "moment doctor --deep --data-dir ./moment-data --json"} {
		if !strings.Contains(help.stdout, required) {
			t.Fatalf("help missing %q:\n%s", required, help.stdout)
		}
	}
}

func TestDoctorCLIAPIDeepUnsupportedAndStrictReport(t *testing.T) {
	application := momentTestApplication()
	dataDir := t.TempDir()
	if output := execute(application, "", "owner", "init", "--data-dir", dataDir, "--json"); output.exit != ExitSuccess {
		t.Fatal(output)
	}
	service, err := doctor.New(doctor.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), doctor.ModeSummary)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "CANARY_OWNER_TOKEN")

	validServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/owner/doctor" || request.Header.Get("Authorization") != "Bearer CANARY_OWNER_TOKEN" {
			t.Errorf("request = %s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": report, "meta": map[string]any{"request_id": "req_doctor"}})
	}))
	defer validServer.Close()

	valid := execute(application, "", "doctor", "--url", validServer.URL, "--json")
	if valid.exit != ExitSuccess || decodeOneDocument(t, valid.stdout)["ok"] != true {
		t.Fatalf("valid = %#v", valid)
	}
	largeReport := report
	largeReport.Checks = append([]doctor.Check(nil), report.Checks...)
	const largeAvailable = uint64(9_223_372_036_854_775_931)
	for index := range largeReport.Checks {
		if largeReport.Checks[index].ID == doctor.CheckDiskSpace {
			evidence := make(map[string]any, len(largeReport.Checks[index].Evidence))
			for key, value := range largeReport.Checks[index].Evidence {
				evidence[key] = value
			}
			evidence["available_bytes"] = largeAvailable
			largeReport.Checks[index].Evidence = evidence
		}
	}
	largeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": largeReport, "meta": map[string]any{"request_id": "req_large"}})
	}))
	large := execute(application, "", "doctor", "--url", largeServer.URL, "--json")
	largeServer.Close()
	if large.exit != ExitSuccess || !strings.Contains(large.stdout, `"available_bytes":9223372036854775931`) {
		t.Fatalf("large integer response = %#v", large)
	}
	unsupported := execute(application, "", "doctor", "--deep", "--url", validServer.URL, "--json")
	if unsupported.exit != ExitInvalidInput || decodeOneDocument(t, unsupported.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("unsupported = %#v", unsupported)
	}

	validReportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	validData := `{"data":` + string(validReportJSON) + `,"meta":{"request_id":"req_doctor"}}`
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{name: "created status", contentType: "application/json", body: validData, status: 201},
		{name: "no content status", contentType: "application/json", status: 204},
		{name: "missing data", contentType: "application/json", body: `{"meta":{"request_id":"req_doctor"}}`, status: 200},
		{name: "missing meta", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `}`, status: 200},
		{name: "null meta", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":null}`, status: 200},
		{name: "empty meta", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":{}}`, status: 200},
		{name: "empty request id", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":{"request_id":"  "}}`, status: 200},
		{name: "wrong request id", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":{"request_id":123}}`, status: 200},
		{name: "extra meta", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":{"request_id":"req_doctor","extra":true}}`, status: 200},
		{name: "duplicate outer data", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"data":` + string(validReportJSON) + `,"meta":{"request_id":"req_doctor"}}`, status: 200},
		{name: "duplicate outer meta", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":{"request_id":"req_doctor"},"meta":{"request_id":"req_doctor"}}`, status: 200},
		{name: "duplicate meta request id", contentType: "application/json", body: `{"data":` + string(validReportJSON) + `,"meta":{"request_id":"req_first","request_id":"req_doctor"}}`, status: 200},
		{name: "duplicate report field", contentType: "application/json", body: strings.Replace(validData, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), status: 200},
		{name: "duplicate check field", contentType: "application/json", body: strings.Replace(validData, `"id":"data_dir"`, `"id":"data_dir","id":"data_dir"`, 1), status: 200},
		{name: "duplicate evidence field", contentType: "application/json", body: strings.Replace(validData, `"evidence":{`, `"evidence":{"duplicate":true,"duplicate":false,`, 1), status: 200},
		{name: "unknown evidence field", contentType: "application/json", body: strings.Replace(validData, `"readable":true`, `"readable":true,"absolute_path":"CANARY_PATH"`, 1), status: 200},
		{name: "negative evidence integer", contentType: "application/json", body: strings.Replace(validData, `"applied_version":9`, `"applied_version":-1`, 1), status: 200},
		{name: "fractional evidence integer", contentType: "application/json", body: strings.Replace(validData, `"applied_version":9`, `"applied_version":1.5`, 1), status: 200},
		{name: "string evidence integer", contentType: "application/json", body: strings.Replace(validData, `"applied_version":9`, `"applied_version":"9"`, 1), status: 200},
		{name: "null data", contentType: "application/json", body: `{"data":null,"meta":{"request_id":"req_doctor"}}`, status: 200},
		{name: "empty data", contentType: "application/json", body: `{"data":{},"meta":{"request_id":"req_doctor"}}`, status: 200},
		{name: "unknown envelope", contentType: "application/json", body: `{"data":{},"meta":{"request_id":"req_doctor"},"extra":true}`, status: 200},
		{name: "unknown report", contentType: "application/json", body: `{"data":` + strings.TrimSuffix(string(validReportJSON), "}") + `,"extra":true},"meta":{"request_id":"req_doctor"}}`, status: 200},
		{name: "wrong schema", contentType: "application/json", body: strings.Replace(validData, `"schema_version":1`, `"schema_version":2`, 1), status: 200},
		{name: "wrong content type", contentType: "text/plain", body: validData, status: 200},
		{name: "non UTF-8 charset", contentType: "application/json; charset=latin1", body: validData, status: 200},
		{name: "unexpected content type parameter", contentType: "application/json; version=1", body: validData, status: 200},
		{name: "invalid UTF-8", contentType: "application/json", body: string([]byte{0xff, 0xfe}), status: 200},
		{name: "wrong status", contentType: "application/json", body: `{"error":{"code":"NOT_READY","message":"not ready","details":{},"request_id":"req"}}`, status: 503},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.contentType)
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			output := execute(application, "", "doctor", "--url", server.URL, "--json")
			if output.exit == ExitSuccess {
				t.Fatalf("invalid response succeeded: %s", output.stdout)
			}
			document := decodeOneDocument(t, output.stdout)
			if document["ok"] != false || strings.Contains(output.stdout+output.stderr, "CANARY_OWNER_TOKEN") {
				t.Fatalf("output=%#v document=%#v", output, document)
			}
		})
	}
}

func TestDoctorCLIExitMatrixPreservesPartialReports(t *testing.T) {
	application := momentTestApplication()
	missing := filepath.Join(t.TempDir(), "missing-data-dir")
	ioFailure := execute(application, "", "doctor", "--data-dir", missing, "--json")
	if ioFailure.exit != ExitIO {
		t.Fatalf("IO failure = %#v", ioFailure)
	}
	ioDocument := decodeOneDocument(t, ioFailure.stdout)
	if ioDocument["ok"] != false || ioDocument["data"] == nil || ioDocument["error"].(map[string]any)["code"] != "IO_ERROR" {
		t.Fatalf("IO document = %#v", ioDocument)
	}

	dataDir := t.TempDir()
	if output := execute(application, "", "owner", "init", "--data-dir", dataDir, "--json"); output.exit != ExitSuccess {
		t.Fatal(output)
	}
	locker, err := runtimefs.OpenExistingLocker(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := locker.Acquire(context.Background(), runtimefs.ImportLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := execute(application, "", "doctor", "--deep", "--data-dir", dataDir, "--json")
	if unavailable.exit != ExitUnavailable {
		_ = lease.Release()
		t.Fatalf("unavailable = %#v", unavailable)
	}
	unavailableDocument := decodeOneDocument(t, unavailable.stdout)
	if unavailableDocument["ok"] != false || unavailableDocument["data"] == nil || unavailableDocument["error"].(map[string]any)["code"] != "DEPENDENCY_UNAVAILABLE" {
		_ = lease.Release()
		t.Fatalf("unavailable document = %#v", unavailableDocument)
	}

	timedOut := execute(application, "", "doctor", "--deep", "--data-dir", dataDir, "--timeout", "20ms", "--json")
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if timedOut.exit != ExitTimeout {
		t.Fatalf("timeout = %#v", timedOut)
	}
	if runtime.GOOS != "windows" {
		lockRoot := filepath.Join(dataDir, "locks")
		if err := os.Chmod(lockRoot, 0); err != nil {
			t.Fatal(err)
		}
		permissionFailure := execute(application, "", "doctor", "--deep", "--data-dir", dataDir, "--json")
		if err := os.Chmod(lockRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		permissionDocument := decodeOneDocument(t, permissionFailure.stdout)
		if permissionFailure.exit != ExitIO || permissionDocument["ok"] != false || permissionDocument["error"].(map[string]any)["code"] != "IO_ERROR" || !documentCheckHasStatus(permissionDocument, doctor.CheckInstanceLocks, string(doctor.StatusFail)) {
			t.Fatalf("lock permission failure = %#v document=%#v", permissionFailure, permissionDocument)
		}
	}
	timeoutDocument := decodeOneDocument(t, timedOut.stdout)
	if timeoutDocument["ok"] != false || timeoutDocument["data"] == nil || timeoutDocument["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("timeout document = %#v", timeoutDocument)
	}

	tests := []struct {
		kind doctor.FailureKind
		exit ExitCode
	}{
		{doctor.FailureNone, ExitSuccess},
		{doctor.FailureUnavailable, ExitUnavailable},
		{doctor.FailureIO, ExitIO},
		{doctor.FailureIntegrity, ExitIntegrity},
		{doctor.FailureInternal, ExitInternal},
		{doctor.FailureCancelled, ExitTimeout},
	}
	for _, test := range tests {
		failure := doctorCommandFailure(test.kind)
		if test.kind == doctor.FailureNone {
			if failure != nil {
				t.Fatalf("none failure = %#v", failure)
			}
			continue
		}
		if failure == nil || failure.ExitCode != test.exit {
			t.Fatalf("kind %s = %#v, want exit %d", test.kind, failure, test.exit)
		}
	}
}

func documentCheckHasStatus(document map[string]any, id, status string) bool {
	data, ok := document["data"].(map[string]any)
	if !ok {
		return false
	}
	checks, ok := data["checks"].([]any)
	if !ok {
		return false
	}
	for _, value := range checks {
		check, ok := value.(map[string]any)
		if ok && check["id"] == id && check["status"] == status {
			return true
		}
	}
	return false
}

func TestDoctorCLIAPICancellationKeepsTimeoutClassification(t *testing.T) {
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", "CANARY_OWNER_TOKEN")
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	output := execute(application, "", "doctor", "--url", server.URL, "--timeout", "10ms", "--json")
	if output.exit != ExitTimeout {
		t.Fatalf("API timeout = %#v", output)
	}
	document := decodeOneDocument(t, output.stdout)
	if document["ok"] != false || document["error"].(map[string]any)["code"] != "TIMEOUT" || strings.Contains(output.stdout+output.stderr, "CANARY_OWNER_TOKEN") {
		t.Fatalf("API timeout document = %#v", document)
	}
}

func TestDoctorCLIMapsServerTimeoutToExitNine(t *testing.T) {
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", "CANARY_OWNER_TOKEN")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusRequestTimeout)
		_, _ = writer.Write([]byte(`{"error":{"code":"TIMEOUT","message":"doctor summary did not complete","details":{},"request_id":"req_timeout"}}`))
	}))
	defer server.Close()

	output := execute(application, "", "doctor", "--url", server.URL, "--json")
	if output.exit != ExitTimeout {
		t.Fatalf("server timeout = %#v", output)
	}
	document := decodeOneDocument(t, output.stdout)
	if document["ok"] != false || document["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("server timeout document = %#v", document)
	}
}
