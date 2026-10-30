package emaildelivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testBudget struct{ allow bool }

func (b testBudget) ReserveEmailBudget(context.Context, string, int, int, time.Time) (bool, error) {
	return b.allow, nil
}

func TestQuotaFallbackPreservesOneChallenge(t *testing.T) {
	var codes []string
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"plan":[{"type":"free","creditsType":"sendLimit","credits":299}]}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/emails" {
			codes = append(codes, body["text"].(string))
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"name":"daily_quota_exceeded"}`))
			return
		}
		codes = append(codes, body["textContent"].(string))
		keys = append(keys, body["headers"].(map[string]any)["idempotencyKey"].(string))
		_, _ = w.Write([]byte(`{"messageId":"accepted"}`))
	}))
	defer server.Close()
	budget := &recordingBudget{testBudget: testBudget{true}}
	sender := Sender{Budget: budget, Providers: []Provider{{Name: "resend", Key: "first", Daily: 100, Monthly: 3000, From: "first@example.test", Endpoint: server.URL}, {Name: "brevo", Key: "second", From: "second@example.test", Endpoint: server.URL}}}
	if err := sender.SendVerification(context.Background(), "reader@example.test", "123456", "challenge-123"); err != nil {
		t.Fatal(err)
	}
	if budget.marked != 1 || budget.limit != 100 || len(budget.period) != 10 {
		t.Fatalf("quota refusal was not remembered: %+v", budget)
	}
	if len(codes) != 2 || codes[0] != codes[1] || keys[0] != keys[1] {
		t.Fatalf("fallback changed challenge: %v %v", codes, keys)
	}
}

func TestUnknownOutcomeDoesNotSendThroughBothProviders(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	sender := Sender{Budget: testBudget{true}, Providers: []Provider{{Name: "resend", Endpoint: server.URL}, {Name: "brevo", Endpoint: server.URL}}}
	if err := sender.SendVerification(context.Background(), "reader@example.test", "123456", "challenge"); err == nil {
		t.Fatal("unknown outcome reported as success")
	}
	if calls != 1 {
		t.Fatalf("sent through %d channels", calls)
	}
}

func TestGmailFailureNeverFallsBackOrLogsCredentials(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var logged []byte
	sender := Sender{Budget: testBudget{true}, Providers: []Provider{
		{Name: "gmail", From: "sender@example.test", Key: "private-app-password"},
		{Name: "resend", Endpoint: server.URL},
	}, Log: func(_, _ string, fields map[string]any) { logged, _ = json.Marshal(fields) }}
	err := sender.SendVerification(ctx, "reader@example.test", "123456", "challenge")
	if err == nil || calls != 0 {
		t.Fatal("SMTP failure was hidden or retried through another provider")
	}
	if string(logged) != `{"provider":"gmail"}` {
		t.Fatalf("unexpected diagnostic fields: %s", logged)
	}
}
func TestBrevoZeroCreditsNeverQueuesVerification(t *testing.T) {
	sends := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sends++
		}
		_, _ = w.Write([]byte(`{"plan":[{"type":"free","creditsType":"sendLimit","credits":0}]}`))
	}))
	defer server.Close()
	sender := Sender{Budget: testBudget{true}, Providers: []Provider{{Name: "brevo", Endpoint: server.URL}}}
	if err := sender.SendVerification(context.Background(), "reader@example.test", "123456", "challenge"); err == nil || sends != 0 {
		t.Fatalf("over-quota queued: %d %v", sends, err)
	}
}

func (testBudget) ExhaustEmailBudget(context.Context, string, string, int) error { return nil }

type recordingBudget struct {
	testBudget
	marked, limit int
	period        string
}

func (budget *recordingBudget) ExhaustEmailBudget(_ context.Context, _, period string, limit int) error {
	budget.marked++
	budget.period = period
	budget.limit = limit
	return nil
}
