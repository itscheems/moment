package ginhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
)

type importInspectorFunc func(context.Context, io.Reader, uint64) (archive.Inspection, error)

func (function importInspectorFunc) Inspect(ctx context.Context, input io.Reader, epoch uint64) (archive.Inspection, error) {
	return function(ctx, input, epoch)
}

type importInspectorPlanner struct {
	inspect importInspectorFunc
	plan    func(context.Context, io.Reader, uint64) (archive.ImportPlan, error)
}

func (value importInspectorPlanner) Inspect(ctx context.Context, input io.Reader, epoch uint64) (archive.Inspection, error) {
	return value.inspect(ctx, input, epoch)
}

func (value importInspectorPlanner) Plan(ctx context.Context, input io.Reader, epoch uint64) (archive.ImportPlan, error) {
	return value.plan(ctx, input, epoch)
}

type fakeImportCommitter struct {
	commit func(context.Context, string, uint64) (archive.CommitResult, error)
	status func(context.Context) (archive.CommitStatus, error)
}

type fakeArchiveImportCommitter struct {
	fakeImportCommitter
	restore func(context.Context, io.Reader, string, uint64) (archive.CommitResult, error)
}

type fakeMergeImportCommitter struct {
	fakeImportCommitter
	planMerge   func(context.Context, io.Reader, uint64) (archive.MergeInspection, error)
	commitMerge func(context.Context, string, uint64, archive.MergeSelection) (archive.CommitResult, error)
}

func (fake fakeMergeImportCommitter) InspectMergeArchive(ctx context.Context, input io.Reader, epoch uint64) (archive.MergeInspection, error) {
	return fake.planMerge(ctx, input, epoch)
}

func (fake fakeMergeImportCommitter) CommitMerge(ctx context.Context, token string, epoch uint64, selection archive.MergeSelection) (archive.CommitResult, error) {
	return fake.commitMerge(ctx, token, epoch, selection)
}

func (fake fakeArchiveImportCommitter) CommitArchive(ctx context.Context, input io.Reader, expectedArchiveSHA string, epoch uint64) (archive.CommitResult, error) {
	return fake.restore(ctx, input, expectedArchiveSHA, epoch)
}

func (fake fakeImportCommitter) Commit(ctx context.Context, token string, epoch uint64) (archive.CommitResult, error) {
	return fake.commit(ctx, token, epoch)
}

func (fake fakeImportCommitter) Status(ctx context.Context) (archive.CommitStatus, error) {
	return fake.status(ctx)
}

func TestImportMergePlanAndCommitStrictOwnerContracts(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	selection := archive.MergeSelection{
		PlanID: strings.Repeat("a", 64), ArchiveSHA256: strings.Repeat("b", 64),
		BaseGeneration: "generation", BaseAuthoritySHA256: strings.Repeat("c", 64),
		Items: []archive.MergeSelectionItem{}, SiteFields: []archive.MergeSiteSelection{},
	}
	committer := fakeMergeImportCommitter{
		fakeImportCommitter: fakeImportCommitter{
			commit: func(context.Context, string, uint64) (archive.CommitResult, error) {
				return archive.CommitResult{}, nil
			},
			status: func(context.Context) (archive.CommitStatus, error) { return archive.CommitStatus{}, nil },
		},
		planMerge: func(_ context.Context, input io.Reader, epoch uint64) (archive.MergeInspection, error) {
			body, _ := io.ReadAll(input)
			if string(body) != "zip-merge" || epoch != 1 {
				t.Fatalf("plan body=%q epoch=%d", body, epoch)
			}
			return archive.MergeInspection{Plan: archive.MergePlan{PlanID: selection.PlanID}, StagingToken: "merge-token"}, nil
		},
		commitMerge: func(_ context.Context, token string, epoch uint64, got archive.MergeSelection) (archive.CommitResult, error) {
			if token != "merge-token" || epoch != 1 || got.PlanID != selection.PlanID {
				t.Fatalf("commit token=%q epoch=%d selection=%#v", token, epoch, got)
			}
			return archive.CommitResult{Status: archive.CommitStatus{Phase: archive.CommitSucceeded}}, nil
		},
	}
	engine, err := New(Config{Assets: testAssets(), OwnerAuth: fixture.manager, ImportCommitter: committer, RequestID: func() string { return "req_merge" }})
	if err != nil {
		t.Fatal(err)
	}
	planRequest := headerRequest(http.MethodPost, "/api/v1/owner/import/merge/plan", fixture.token, strings.NewReader("zip-merge"))
	planRequest.Header.Set("Content-Type", "application/zip")
	planResponse := performRequest(engine, planRequest)
	if planResponse.Code != http.StatusCreated || !strings.Contains(planResponse.Body.String(), `"staging_token":"merge-token"`) {
		t.Fatalf("plan response=%d %s", planResponse.Code, planResponse.Body.String())
	}
	selectionJSON, _ := json.Marshal(selection)
	commitRequest := headerRequest(http.MethodPost, "/api/v1/owner/import/merge/commit", fixture.token, strings.NewReader(`{"staging_token":"merge-token","selection":`+string(selectionJSON)+`}`))
	commitRequest.Header.Set("Content-Type", "application/json")
	commitResponse := performRequest(engine, commitRequest)
	if commitResponse.Code != http.StatusOK || !strings.Contains(commitResponse.Body.String(), `"phase":"succeeded"`) {
		t.Fatalf("commit response=%d %s", commitResponse.Code, commitResponse.Body.String())
	}
	for _, body := range []string{
		`{}`, `{"staging_token":"merge-token"}`, `{"selection":{}}`,
		`{"staging_token":"merge-token","selection":{},"extra":true}`,
		`{"staging_token":"merge-token","staging_token":"again","selection":{}}`,
	} {
		request := headerRequest(http.MethodPost, "/api/v1/owner/import/merge/commit", fixture.token, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if response := performRequest(engine, request); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid merge body %s => %d %s", body, response.Code, response.Body.String())
		}
	}
}

func TestImportCommitStrictOwnerContractAndStatus(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	calls := 0
	fake := fakeImportCommitter{
		commit: func(_ context.Context, token string, epoch uint64) (archive.CommitResult, error) {
			calls++
			if token != "stage-token" || epoch != 1 {
				t.Fatalf("token=%q epoch=%d", token, epoch)
			}
			return archive.CommitResult{Status: archive.CommitStatus{
				OperationID: "import-20260811T010203000Z-0011223344556677", Phase: archive.CommitSucceeded,
				ActiveGeneration: "import-20260811T010203000Z-0011223344556677", PreviousGeneration: "gen-old",
				NewGeneration: "import-20260811T010203000Z-0011223344556677", BackupName: "pre-import.zip", BackupVerified: true,
				StartedAt: now, UpdatedAt: now, CompletedAt: &now,
			}}, nil
		},
		status: func(context.Context) (archive.CommitStatus, error) {
			return archive.CommitStatus{OperationID: "op", Phase: archive.CommitBackingUp, ActiveGeneration: "old", PreviousGeneration: "old", NewGeneration: "op", StartedAt: now, UpdatedAt: now}, nil
		},
	}
	engine, err := New(Config{
		Assets: testAssets(), OwnerAuth: fixture.manager, ImportCommitter: fake,
		Readiness: func(context.Context) error { return nil }, RequestID: func() string { return "req_commit" },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := headerRequest(http.MethodPost, "/api/v1/owner/import/commit", fixture.token, strings.NewReader(`{"staging_token":"stage-token"}`))
	request.Header.Set("Content-Type", "application/json")
	response := performRequest(engine, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"phase":"succeeded"`) || !strings.Contains(response.Body.String(), `"request_id":"req_commit"`) {
		t.Fatalf("commit response=%d %s", response.Code, response.Body.String())
	}
	statusRequest := headerRequest(http.MethodGet, "/api/v1/owner/import/status", fixture.token, nil)
	statusResponse := performRequest(engine, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"phase":"backing_up"`) {
		t.Fatalf("status response=%d %s", statusResponse.Code, statusResponse.Body.String())
	}
	invalidBodies := []string{
		`{}`, `{"staging_token":null}`, `{"staging_token":""}`,
		`{"staging_token":"stage-token","extra":1}`,
		`{"staging_token":"stage-token","staging_token":"again"}`,
		`[{"staging_token":"stage-token"}]`,
	}
	for _, body := range invalidBodies {
		invalid := headerRequest(http.MethodPost, "/api/v1/owner/import/commit", fixture.token, strings.NewReader(body))
		invalid.Header.Set("Content-Type", "application/json")
		result := performRequest(engine, invalid)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %s => %d %s", body, result.Code, result.Body.String())
		}
	}
	query := headerRequest(http.MethodPost, "/api/v1/owner/import/commit?x=1", fixture.token, strings.NewReader(`{"staging_token":"stage-token"}`))
	query.Header.Set("Content-Type", "application/json")
	if result := performRequest(engine, query); result.Code != http.StatusBadRequest {
		t.Fatalf("query status=%d", result.Code)
	}
	wrongType := headerRequest(http.MethodPost, "/api/v1/owner/import/commit", fixture.token, strings.NewReader(`{"staging_token":"stage-token"}`))
	wrongType.Header.Set("Content-Type", "text/plain")
	if result := performRequest(engine, wrongType); result.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status=%d", result.Code)
	}
	if calls != 1 {
		t.Fatalf("invalid requests invoked commit %d times", calls)
	}
}

func TestImportCommitMapsDomainFailure(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	fake := fakeImportCommitter{
		commit: func(context.Context, string, uint64) (archive.CommitResult, error) {
			return archive.CommitResult{}, domain.NewError(domain.CodeImportDiskSpaceInsufficient, "low disk", map[string]any{"required_additional_bytes": 10})
		},
		status: func(context.Context) (archive.CommitStatus, error) {
			return archive.CommitStatus{}, domain.NewError(domain.CodeDataIntegrityFailed, "needs attention", nil)
		},
	}
	engine, err := New(Config{Assets: testAssets(), OwnerAuth: fixture.manager, ImportCommitter: fake})
	if err != nil {
		t.Fatal(err)
	}
	request := headerRequest(http.MethodPost, "/api/v1/owner/import/commit", fixture.token, strings.NewReader(`{"staging_token":"stage-token"}`))
	request.Header.Set("Content-Type", "application/json")
	response := performRequest(engine, request)
	if response.Code != http.StatusInsufficientStorage || decodeError(t, response.Body).Error.Code != string(domain.CodeImportDiskSpaceInsufficient) {
		t.Fatalf("domain response=%d %s", response.Code, response.Body.String())
	}
}

func TestImportInspectOwnerUploadEpochAndEnvelope(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	called := 0
	inspector := importInspectorFunc(func(_ context.Context, input io.Reader, epoch uint64) (archive.Inspection, error) {
		called++
		body, err := io.ReadAll(input)
		if err != nil {
			return archive.Inspection{}, err
		}
		if string(body) != "zip" || epoch != 1 {
			t.Fatalf("body=%q epoch=%d", body, epoch)
		}
		return archive.Inspection{
			FormatVersion: 1, StagingToken: "token", ExpiresAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
			Disk: archive.DiskEstimate{SpaceOK: true}, Warnings: []string{},
		}, nil
	})
	engine, err := New(Config{
		Assets: testAssets(), OwnerAuth: fixture.manager, ImportInspector: inspector,
		Readiness: func(context.Context) error { return nil }, RequestID: func() string { return "req_import" },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", fixture.token, strings.NewReader("zip"))
	request.Header.Set("Content-Type", "application/zip")
	response := performRequest(engine, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"staging_token":"token"`) || !strings.Contains(response.Body.String(), `"request_id":"req_import"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if called != 1 {
		t.Fatalf("calls = %d", called)
	}

	for _, request := range []*http.Request{
		headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", "wrong", strings.NewReader("zip")),
		headerRequest(http.MethodPost, "/api/v1/owner/import/inspect?x=1", fixture.token, strings.NewReader("zip")),
		headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", fixture.token, strings.NewReader("zip")),
	} {
		if request.URL.RawQuery == "" && request.Header.Get("Authorization") == "Bearer "+fixture.token {
			request.Header.Set("Content-Type", "application/json")
		} else {
			request.Header.Set("Content-Type", "application/zip")
		}
		result := performRequest(engine, request)
		if result.Code < 400 {
			t.Fatalf("strict request succeeded: %d %s", result.Code, result.Body.String())
		}
	}
	if called != 1 {
		t.Fatalf("invalid requests invoked inspector: %d", called)
	}
}

func TestImportPlanAndSingleRequestRestoreOwnerContracts(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	id := "import-20260811T013000000Z-0011223344556677"
	inspector := importInspectorPlanner{
		inspect: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, errors.New("inspect must not run")
		}),
		plan: func(_ context.Context, input io.Reader, epoch uint64) (archive.ImportPlan, error) {
			body, err := io.ReadAll(input)
			if err != nil || string(body) != "zip-plan" || epoch != 1 {
				t.Fatalf("plan body=%q epoch=%d err=%v", body, epoch, err)
			}
			return archive.ImportPlan{DryRun: true, FormatVersion: archive.FormatVersion, ArchiveSHA256: strings.Repeat("a", 64), Warnings: []string{}, Disk: archive.DiskEstimate{SpaceOK: true}}, nil
		},
	}
	restoreCalls := 0
	committer := fakeArchiveImportCommitter{
		fakeImportCommitter: fakeImportCommitter{
			commit: func(context.Context, string, uint64) (archive.CommitResult, error) {
				return archive.CommitResult{}, errors.New("token commit must not run")
			},
			status: func(context.Context) (archive.CommitStatus, error) {
				return archive.CommitStatus{}, errors.New("status must not run")
			},
		},
		restore: func(_ context.Context, input io.Reader, expectedArchiveSHA string, epoch uint64) (archive.CommitResult, error) {
			restoreCalls++
			body, err := io.ReadAll(input)
			if err != nil || string(body) != "zip-restore" || expectedArchiveSHA != strings.Repeat("a", 64) || epoch != 1 {
				t.Fatalf("restore body=%q sha=%q epoch=%d err=%v", body, expectedArchiveSHA, epoch, err)
			}
			return archive.CommitResult{Status: archive.CommitStatus{
				OperationID: id, Phase: archive.CommitSucceeded, ActiveGeneration: id, PreviousGeneration: "gen-old", NewGeneration: id,
				BackupName: "pre-import.zip", BackupVerified: true, StartedAt: now, UpdatedAt: now, CompletedAt: &now,
			}}, nil
		},
	}
	engine, err := New(Config{Assets: testAssets(), OwnerAuth: fixture.manager, ImportInspector: inspector, ImportCommitter: committer, RequestID: func() string { return "req_archive" }})
	if err != nil {
		t.Fatal(err)
	}
	planRequest := headerRequest(http.MethodPost, "/api/v1/owner/import/plan", fixture.token, strings.NewReader("zip-plan"))
	planRequest.Header.Set("Content-Type", "application/zip")
	planResponse := performRequest(engine, planRequest)
	if planResponse.Code != http.StatusOK || !strings.Contains(planResponse.Body.String(), `"dry_run":true`) || strings.Contains(planResponse.Body.String(), "staging_token") {
		t.Fatalf("plan response=%d %s", planResponse.Code, planResponse.Body.String())
	}
	restoreRequest := headerRequest(http.MethodPost, "/api/v1/owner/import/restore", fixture.token, strings.NewReader("zip-restore"))
	restoreRequest.Header.Set("Content-Type", "application/zip")
	restoreRequest.Header.Set("X-Moment-Archive-SHA256", strings.Repeat("a", 64))
	restoreResponse := performRequest(engine, restoreRequest)
	if restoreResponse.Code != http.StatusOK || !strings.Contains(restoreResponse.Body.String(), `"phase":"succeeded"`) {
		t.Fatalf("restore response=%d %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	for _, configure := range []func(*http.Request){
		func(*http.Request) {},
		func(request *http.Request) { request.Header.Set("X-Moment-Archive-SHA256", strings.Repeat("A", 64)) },
		func(request *http.Request) {
			request.Header.Add("X-Moment-Archive-SHA256", strings.Repeat("a", 64))
			request.Header.Add("X-Moment-Archive-SHA256", strings.Repeat("a", 64))
		},
	} {
		request := headerRequest(http.MethodPost, "/api/v1/owner/import/restore", fixture.token, strings.NewReader("zip"))
		request.Header.Set("Content-Type", "application/zip")
		configure(request)
		if response := performRequest(engine, request); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid expected archive hash status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if restoreCalls != 1 {
		t.Fatalf("invalid expected archive hash invoked restore: %d", restoreCalls)
	}
	for _, path := range []string{"/api/v1/owner/import/plan?x=1", "/api/v1/owner/import/restore?x=1"} {
		request := headerRequest(http.MethodPost, path, fixture.token, strings.NewReader("zip"))
		request.Header.Set("Content-Type", "application/zip")
		if response := performRequest(engine, request); response.Code != http.StatusBadRequest {
			t.Fatalf("query %s status=%d", path, response.Code)
		}
	}
}

func TestImportInspectMapsLimitsDomainAndCancellation(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	tests := []struct {
		name      string
		inspector ImportInspector
		limit     int64
		body      string
		status    int
		code      string
	}{
		{name: "body limit", limit: 2, body: "zip", inspector: importInspectorFunc(func(_ context.Context, input io.Reader, _ uint64) (archive.Inspection, error) {
			_, err := io.ReadAll(input)
			return archive.Inspection{}, err
		}), status: http.StatusRequestEntityTooLarge, code: "REQUEST_BODY_TOO_LARGE"},
		{name: "disk", body: "z", inspector: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, domain.NewError(domain.CodeImportDiskSpaceInsufficient, "low disk", map[string]any{"required_additional_bytes": 10})
		}), status: http.StatusInsufficientStorage, code: "IMPORT_DISK_SPACE_INSUFFICIENT"},
		{name: "in progress", body: "z", inspector: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, domain.NewError(domain.CodeImportInProgress, "active", nil)
		}), status: http.StatusConflict, code: "IMPORT_IN_PROGRESS"},
		{name: "cancel", body: "z", inspector: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, context.Canceled
		}), status: http.StatusRequestTimeout, code: "CANCELLED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limit := test.limit
			if limit == 0 {
				limit = 1 << 20
			}
			engine, err := New(Config{
				Assets: testAssets(), OwnerAuth: fixture.manager, ImportInspector: test.inspector,
				Readiness: func(context.Context) error { return nil }, Limits: BodyLimits{Archive: limit},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", fixture.token, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/zip")
			response := performRequest(engine, request)
			if response.Code != test.status || decodeError(t, response.Body).Error.Code != test.code {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestImportInspectConfigRequiresOwnerAuth(t *testing.T) {
	inspector := importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
		return archive.Inspection{}, errors.New("must not run")
	})
	if _, err := New(Config{Assets: testAssets(), ImportInspector: inspector}); err == nil {
		t.Fatal("import inspector without owner auth succeeded")
	}
}
