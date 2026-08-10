package ginhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestDoctorHTTPSummaryOwnerAuthStrictInputAndRedaction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CANARY_ABSOLUTE_DATA_PATH")
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: root, Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if err := database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, writeErr := queries.ExecContext(context.Background(), `INSERT INTO entries
(id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'CANARY_PRIVATE_POST_BODY', 1, 1, 1, 'CANARY_PRIVATE_POST_BODY')`)
		return writeErr
	}); err != nil {
		t.Fatal(err)
	}
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CURRENT.next"), []byte("CANARY_STAGING_TOKEN_HASH\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "secrets.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	reporter, err := doctor.New(doctor.Options{DataDir: root, ActiveDatabase: database})
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	engine, err := New(Config{
		Assets: testAssets(), OwnerAuth: manager, Doctor: reporter,
		RequestID: func() string { return "req_doctor" },
		Log: func(level, event string, fields map[string]any) {
			logs = append(logs, fmt.Sprintf("%s:%s:%v", level, event, fields))
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := performRequest(engine, ownerRequest(http.MethodGet, "/api/v1/owner/doctor", ""))
	if unauthorized.Code != http.StatusUnauthorized || decodeError(t, unauthorized.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("unauthorized=%d %s", unauthorized.Code, unauthorized.Body.String())
	}

	request := ownerRequest(http.MethodGet, "/api/v1/owner/doctor", "")
	request.Header.Set("Authorization", "Bearer "+credential.Token)
	response := performRequest(engine, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("summary=%d %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data doctor.Report  `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if err := doctor.ValidateReport(envelope.Data, doctor.ModeSummary); err != nil {
		t.Fatal(err)
	}
	if envelope.Meta["request_id"] != "req_doctor" || findHTTPDoctorCheck(t, envelope.Data, doctor.CheckCurrentNextState).Status != doctor.StatusWarning ||
		findHTTPDoctorCheck(t, envelope.Data, doctor.CheckSecretPermissions).Status != doctor.StatusFail {
		t.Fatalf("summary=%#v meta=%#v", envelope.Data, envelope.Meta)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	allOutput := string(encoded) + strings.Join(logs, "\n")
	for _, canary := range []string{credential.Token, root, "CANARY_ABSOLUTE_DATA_PATH", "CANARY_PRIVATE_POST_BODY", "CANARY_STAGING_TOKEN_HASH"} {
		if strings.Contains(allOutput, canary) {
			t.Fatalf("HTTP summary or logs leaked %q: %s", canary, allOutput)
		}
	}
	for _, check := range envelope.Data.Checks {
		if check.ID == doctor.CheckCurrentGeneration {
			if _, exists := check.Evidence["generation_id"]; exists {
				t.Fatalf("summary exposed generation topology: %#v", check)
			}
		}
	}

	for _, path := range []string{"/api/v1/owner/doctor?deep=true", "/api/v1/owner/doctor?unknown=1"} {
		strict := ownerRequest(http.MethodGet, path, "")
		strict.Header.Set("Authorization", "Bearer "+credential.Token)
		strictResponse := performRequest(engine, strict)
		if strictResponse.Code != http.StatusBadRequest || decodeError(t, strictResponse.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict query %s = %d %s", path, strictResponse.Code, strictResponse.Body.String())
		}
	}
	body := ownerRequest(http.MethodGet, "/api/v1/owner/doctor", `{"deep":true}`)
	body.Header.Set("Authorization", "Bearer "+credential.Token)
	bodyResponse := performRequest(engine, body)
	if bodyResponse.Code != http.StatusBadRequest || decodeError(t, bodyResponse.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("strict body = %d %s", bodyResponse.Code, bodyResponse.Body.String())
	}
}

type panicDoctorReporter struct{}

func (panicDoctorReporter) Run(context.Context, doctor.Mode) (doctor.Report, error) {
	panic("CANARY_WRAPPED_SECRET_PANIC")
}

type cancelledDoctorReporter struct{}

func (cancelledDoctorReporter) Run(context.Context, doctor.Mode) (doctor.Report, error) {
	return doctor.Report{}, errors.Join(errors.New("wrapped diagnostic cancellation"), context.DeadlineExceeded)
}

func TestDoctorHTTPWrappedCancellationIsTimeout(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	engine, err := New(Config{Assets: testAssets(), OwnerAuth: fixture.manager, Doctor: cancelledDoctorReporter{}})
	if err != nil {
		t.Fatal(err)
	}
	request := ownerRequest(http.MethodGet, "/api/v1/owner/doctor", "")
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(engine, request)
	if response.Code != http.StatusRequestTimeout || decodeError(t, response.Body).Error.Code != "TIMEOUT" {
		t.Fatalf("wrapped cancellation response=%d %s", response.Code, response.Body.String())
	}
}

func TestDoctorHTTPPanicIsRedactedAndConfigRequiresAuth(t *testing.T) {
	if _, err := New(Config{Assets: testAssets(), Doctor: panicDoctorReporter{}}); err == nil {
		t.Fatal("doctor route without owner auth was accepted")
	}
	fixture := newOwnerTestFixture(t, false)
	var logs []string
	engine, err := New(Config{
		Assets: testAssets(), OwnerAuth: fixture.manager, Doctor: panicDoctorReporter{},
		RequestID: func() string { return "req_doctor_panic" },
		Log: func(level, event string, fields map[string]any) {
			logs = append(logs, fmt.Sprintf("%s:%s:%v", level, event, fields))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ownerRequest(http.MethodGet, "/api/v1/owner/doctor", "")
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(engine, request)
	if response.Code != http.StatusInternalServerError || decodeError(t, response.Body).Error.Code != "INTERNAL" {
		t.Fatalf("panic response=%d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String()+strings.Join(logs, "\n"), "CANARY_WRAPPED_SECRET_PANIC") {
		t.Fatal("panic secret leaked through response or logs")
	}
}

func findHTTPDoctorCheck(t *testing.T, report doctor.Report, id string) doctor.Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("missing check %s", id)
	return doctor.Check{}
}
