package ginhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestPersonalImportHTTPBindsAccountAndPreservesExistingData(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	session, csrf, member := signInMember(t, fixture)
	ctx := context.Background()
	if _, err := fixture.accountService.SetStatus(ctx, member.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}
	exporter, err := archive.NewExporter(archive.Options{DataDir: fixture.runtime.Root, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := fixture.accountService.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var zip bytes.Buffer
	if _, err = exporter.ExportAccount(ctx, &zip, owner.ID); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Assets: testAssets(), SessionSigner: fixture.manager, Accounts: fixture.accountService, Archive: exporter})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, sha string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(zip.Bytes()))
		req.Header.Set("Content-Type", "application/zip")
		req.AddCookie(session)
		req.Header.Set(CSRFHeaderName, csrf)
		if sha != "" {
			req.Header.Set("X-Moment-Archive-SHA256", sha)
		}
		return req
	}
	plan := performRequest(engine, request("/api/v1/account/import/plan", ""))
	if plan.Code != 200 {
		t.Fatalf("plan=%d %s", plan.Code, plan.Body.String())
	}
	data := decodeDataMap(t, plan)
	hash := data["archive_sha256"].(string)
	if data["account_id"] != member.ID {
		t.Fatal("preview selected another account")
	}
	bad := performRequest(engine, request("/api/v1/account/import/restore", strings.Repeat("a", 64)))
	if bad.Code == 200 {
		t.Fatal("changed archive accepted")
	}
	selected := performRequest(engine, request("/api/v1/account/import/plan?account_id="+owner.ID, ""))
	if selected.Code != 400 {
		t.Fatalf("account selection=%d", selected.Code)
	}
	noCSRF := request("/api/v1/account/import/restore", hash)
	noCSRF.Header.Del(CSRFHeaderName)
	if got := performRequest(engine, noCSRF); got.Code != 403 {
		t.Fatalf("missing CSRF=%d %s", got.Code, got.Body.String())
	}
	for attempt := 0; attempt < 2; attempt++ {
		got := performRequest(engine, request("/api/v1/account/import/restore", hash))
		if got.Code != 200 {
			t.Fatalf("restore=%d %s", got.Code, got.Body.String())
		}
		added := decodeDataMap(t, got)["added"].(map[string]any)["entries"].(float64)
		if added != float64(1-attempt) {
			t.Fatalf("unexpected additions on attempt %d: %v", attempt, added)
		}
	}
	if err := fixture.database.ReadTx(ctx, func(q sqlitestore.Queries) error {
		var own, other int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM entries WHERE author_user_id=?`, member.ID).Scan(&own); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM entries WHERE author_user_id=?`, owner.ID).Scan(&other); err != nil {
			return err
		}
		if own != 1 || other != 1 {
			t.Fatalf("original dataset changed: imported=%d original=%d", own, other)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
