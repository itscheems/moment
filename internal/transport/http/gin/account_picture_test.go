package ginhttp

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

func testPictureBytes(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func pictureUpload(t *testing.T, path, filename string, contents []byte, session *http.Cookie, csrf string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(CSRFHeaderName, csrf)
	request.AddCookie(session)
	return request
}

// signInMember completes a federated sign-in against a fixture engine and
// returns the member's session, CSRF token and account.
func signInMember(t *testing.T, fixture commentHTTPFixture) (*http.Cookie, string, domain.User) {
	t.Helper()
	flow, state := startOAuthFlowOn(t, fixture.engine)
	session := sessionCookieFrom(performRequest(fixture.engine, callbackRequest(state, "code", flow)))
	if session == nil {
		t.Fatal("sign-in issued no session")
	}
	current := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	current.AddCookie(session)
	csrf := decodeCSRF(t, performRequest(fixture.engine, current))

	accounts, err := fixture.accountService.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			return session, csrf, account
		}
	}
	t.Fatal("sign-in created no member account")
	return nil, "", domain.User{}
}

// A member can replace the picture a provider guessed for them, but only once
// somebody has admitted them.
//
// This is the one thing on an account's own profile that approval gates. Every
// other field is text an input validator bounds; this writes bytes to the disk,
// and the whole point of the approval mode is that an account nobody has looked
// at yet cannot consume anything.
func TestAccountPictureUploadRequiresApproval(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	session, csrf, member := signInMember(t, fixture)
	if member.Status != domain.AccountPending {
		t.Fatalf("member landed as %q, want pending", member.Status)
	}

	pending := performRequest(fixture.engine,
		pictureUpload(t, "/api/v1/account/avatar", "portrait.png", testPictureBytes(t), session, csrf))
	if pending.Code != http.StatusForbidden {
		t.Fatalf("pending upload = %d, want 403: %s", pending.Code, pending.Body.String())
	}
	if !strings.Contains(pending.Body.String(), string(domain.CodeAccountNotApproved)) {
		t.Fatalf("pending upload body = %s", pending.Body.String())
	}

	if _, err := fixture.accountService.SetStatus(context.Background(), member.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}
	approved := performRequest(fixture.engine,
		pictureUpload(t, "/api/v1/account/avatar", "portrait.png", testPictureBytes(t), session, csrf))
	if approved.Code != http.StatusOK {
		t.Fatalf("approved upload = %d, want 200: %s", approved.Code, approved.Body.String())
	}

	// The picture is assigned in the same request that ingested it, which is
	// what keeps a member from adding files nothing points at.
	stored, err := fixture.accountService.Account(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AvatarMediaID == nil {
		t.Fatal("an accepted upload left the account with no picture")
	}
	if !strings.Contains(approved.Body.String(), *stored.AvatarMediaID) {
		t.Fatalf("response did not report the assigned picture: %s", approved.Body.String())
	}
}

func TestAccountPictureUploadRefusesWhatIsNotAPicture(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	session, csrf, member := signInMember(t, fixture)
	if _, err := fixture.accountService.SetStatus(context.Background(), member.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}

	t.Run("something larger than a portrait is refused before it is read", func(t *testing.T) {
		oversized := bytes.Repeat([]byte{0x00}, maxAccountPictureBytes+1)
		response := performRequest(fixture.engine,
			pictureUpload(t, "/api/v1/account/avatar", "huge.png", oversized, session, csrf))
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized upload = %d, want 413: %s", response.Code, response.Body.String())
		}
	})

	t.Run("bytes that are not an image at all are refused", func(t *testing.T) {
		response := performRequest(fixture.engine,
			pictureUpload(t, "/api/v1/account/avatar", "notes.txt", []byte("this is not a picture"), session, csrf))
		if response.Code < 400 {
			t.Fatalf("text upload = %d, want a refusal: %s", response.Code, response.Body.String())
		}
		stored, err := fixture.accountService.Account(context.Background(), member.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.AvatarMediaID != nil {
			t.Fatal("a refused upload was assigned anyway")
		}
	})

	t.Run("the general media route stays owner-only", func(t *testing.T) {
		request := pictureUpload(t, "/api/v1/owner/media", "portrait.png", testPictureBytes(t), session, csrf)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
			t.Fatalf("member reached the media pool = %d: %s", response.Code, response.Body.String())
		}
	})
}

// Sessions were only ever added. Every sign-in wrote a row and nothing removed
// one, so the table grew for the life of the deployment rather than with the
// number of people using it.
func TestExpiredSessionsAreSweptAndLiveOnesAreNot(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	owner, err := fixture.accountService.OwnerAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	live, err := fixture.accountService.StartSession(context.Background(), owner.ID,
		"11111111111111111111111111111111", "web", now.Add(12*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	// Expired, but inside the grace window that keeps "when did this device
	// last sign in" answerable for a while after it stopped working.
	recent, err := fixture.accountService.StartSession(context.Background(), owner.ID,
		"22222222222222222222222222222222", "web", now.Add(-time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := fixture.accountService.StartSession(context.Background(), owner.ID,
		"33333333333333333333333333333333", "cli", now.Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}

	removed, err := fixture.accountService.SweepExpiredSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("swept %d sessions, want only the long-expired one", removed)
	}

	remaining, err := fixture.accountService.Sessions(context.Background(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := make(map[string]bool, len(remaining))
	for _, session := range remaining {
		kept[session.ID] = true
	}
	if !kept[live.ID] || !kept[recent.ID] {
		t.Fatalf("a session that should have been kept was swept: %+v", kept)
	}
	if kept[stale.ID] {
		t.Fatal("a long-expired session survived the sweep")
	}
}
