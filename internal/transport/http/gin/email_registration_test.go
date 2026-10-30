package ginhttp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/moment/moment/internal/app"
)

type memoryVerificationSender struct{ store *memoryAccounts }

func (sender memoryVerificationSender) SendVerification(_ context.Context, email, code, _ string) error {
	sender.store.mu.Lock()
	defer sender.store.mu.Unlock()
	sender.store.emailCodes[email] = code
	return nil
}
func (store *memoryAccounts) InsertRegistrationChallenge(_ context.Context, c app.RegistrationChallenge) error {
	for id, old := range store.emailChallenges {
		if old.Email == c.Email {
			old.Consumed = true
			store.emailChallenges[id] = old
		}
	}
	store.emailChallenges[c.ID] = c
	return nil
}
func (store *memoryAccounts) RegistrationChallenge(_ context.Context, key string, proof bool) (app.RegistrationChallenge, bool, error) {
	if !proof {
		c, ok := store.emailChallenges[key]
		return c, ok, nil
	}
	for _, c := range store.emailChallenges {
		if hex.EncodeToString(c.ProofHash) == key {
			return c, true, nil
		}
	}
	return app.RegistrationChallenge{}, false, nil
}
func (store *memoryAccounts) SaveRegistrationChallenge(_ context.Context, c app.RegistrationChallenge) error {
	store.emailChallenges[c.ID] = c
	return nil
}
func (store *memoryAccounts) RegistrationRequestBudget(_ context.Context, email, requester string, since int64) (int, int, int64, error) {
	a, b := 0, 0
	var last int64
	for _, c := range store.emailChallenges {
		if c.RequestedAtMS < since {
			continue
		}
		if c.Email == email {
			a++
			if c.RequestedAtMS > last {
				last = c.RequestedAtMS
			}
		}
		if c.RequesterHash == requester {
			b++
		}
	}
	return a, b, last, nil
}

func verifiedRegistrationBody(t *testing.T, fixture authenticationTestFixture, username, password string) string {
	t.Helper()
	email := username + "@example.test"
	response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register/email", string(mustJSON(t, map[string]string{"email": email}))))
	if response.Code != http.StatusOK {
		t.Fatalf("request code: %d %s", response.Code, response.Body.String())
	}
	id := decodeDataMap(t, response)["challenge_id"].(string)
	response = performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register/email/verify", string(mustJSON(t, map[string]string{"challenge_id": id, "code": fixture.store.emailCodes[email]}))))
	if response.Code != http.StatusOK {
		t.Fatalf("verify code: %d %s", response.Code, response.Body.String())
	}
	token := decodeDataMap(t, response)["verification_token"].(string)
	bytes, err := json.Marshal(map[string]string{"username": username, "password": password, "verification_token": token})
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

func TestRegistrationRequiresEmailProofAndAllowsEmailPasswordLogin(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	result := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"reader","password":"password123"}`))
	if result.Code != http.StatusBadRequest || decodeError(t, result.Body).Error.Code != "VERIFICATION_INVALID" {
		t.Fatalf("unverified signup=%d %s", result.Code, result.Body.String())
	}
	body := verifiedRegistrationBody(t, fixture, "reader", "password123")
	result = performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", body))
	if result.Code != http.StatusCreated {
		t.Fatalf("signup=%d %s", result.Code, result.Body.String())
	}
	request := jsonRequest(http.MethodGet, "/api/v1/account/identities", "")
	request.AddCookie(sessionCookieFrom(result))
	listed := performRequest(fixture.engine, request)
	var identityList struct {
		Data []identityView `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &identityList); err != nil || listed.Code != http.StatusOK || len(identityList.Data) != 0 {
		t.Fatalf("local email appeared as third-party login: %d %s", listed.Code, listed.Body.String())
	}
	result = performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", `{"username":"READER@example.test","password":"password123"}`))
	if result.Code != http.StatusOK {
		t.Fatalf("email login=%d %s", result.Code, result.Body.String())
	}
	result = performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", `{"username":"reader@example.test","password":"incorrect"}`))
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password=%d", result.Code)
	}
}
