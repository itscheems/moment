package emaildelivery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type testBudget struct{ allow bool }

func (b testBudget) ReserveEmailBudget(context.Context, int, time.Time) (bool, error) {
	return b.allow, nil
}

func TestDeliveryRequiresCompleteConfiguration(t *testing.T) {
	for _, sender := range []*Sender{nil, {}, {Budget: testBudget{true}, Username: "sender@example.test"}, {Budget: testBudget{true}, Password: "test-app-password"}} {
		if err := sender.SendVerification(context.Background(), "reader@example.test", "123456", "id"); err == nil || err.Error() != "email delivery is not configured" {
			t.Fatal("incomplete configuration accepted")
		}
	}
}
func TestDailyCapacityRefusesBeforeNetworkIO(t *testing.T) {
	sender := Sender{Username: "sender@example.test", Password: "test-app-password", Budget: testBudget{false}}
	if err := sender.SendVerification(context.Background(), "reader@example.test", "123456", "id"); err == nil || err.Error() != "daily email delivery capacity is exhausted" {
		t.Fatal("exhausted budget did not stop delivery")
	}
}
func TestGmailFailureLogsOnlySanitizedDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var logged []byte
	sender := Sender{Username: "sender@example.test", Password: "private-app-password", Budget: testBudget{true}, Log: func(_, _ string, fields map[string]any) { logged, _ = json.Marshal(fields) }}
	if err := sender.SendVerification(ctx, "reader@example.test", "123456", "challenge"); err == nil {
		t.Fatal("SMTP failure hidden")
	}
	var fields map[string]string
	if json.Unmarshal(logged, &fields) != nil || fields["provider"] != "gmail" || fields["reason"] == "" {
		t.Fatal("missing delivery diagnostics")
	}
	for _, secret := range []string{"private-app-password", "123456", "reader@example.test", "sender@example.test"} {
		if strings.Contains(string(logged), secret) {
			t.Fatal("sensitive delivery data appeared in diagnostics")
		}
	}
}
