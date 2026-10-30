// Package emaildelivery adapts transactional mail providers behind one sender.
package emaildelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Budget reserves durable capacity before a provider receives a message.
type Budget interface {
	ReserveEmailBudget(context.Context, string, int, int, time.Time) (bool, error)
	ExhaustEmailBudget(context.Context, string, string, int) error
}

type Provider struct {
	Name, Key, From, Endpoint string
	Daily, Monthly            int
}
type Sender struct {
	Providers []Provider
	Budget    Budget
	Client    *http.Client
	Log       func(string, string, map[string]any)
}

// NewFromEnvironment keeps credentials in process configuration, never site data.
func NewFromEnvironment(budget Budget) *Sender {
	sender := &Sender{Budget: budget, Client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if key := strings.TrimSpace(os.Getenv("MOMENT_RESEND_API_KEY")); key != "" {
		from := strings.TrimSpace(os.Getenv("MOMENT_RESEND_FROM"))
		if from == "" {
			from = "Moment <onboarding@resend.dev>"
		}
		sender.Providers = append(sender.Providers, Provider{Name: "resend", Key: key, From: from, Endpoint: "https://api.resend.com", Daily: 100, Monthly: 3000})
	}
	if key := strings.TrimSpace(os.Getenv("MOMENT_BREVO_API_KEY")); key != "" {
		from := strings.TrimSpace(os.Getenv("MOMENT_BREVO_FROM"))
		if from != "" {
			sender.Providers = append(sender.Providers, Provider{Name: "brevo", Key: key, From: from, Endpoint: "https://api.brevo.com", Daily: 300})
		}
	}
	return sender
}

func (sender *Sender) SendVerification(ctx context.Context, to, code, id string) error {
	if sender == nil || sender.Budget == nil {
		return errors.New("email delivery is not configured")
	}
	client := sender.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	digest := sha256.Sum256([]byte(id))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	token := hex.EncodeToString(digest[:16])
	id = token[:8] + "-" + token[8:12] + "-" + token[12:16] + "-" + token[16:20] + "-" + token[20:]
	for _, provider := range sender.Providers {
		allowed, err := sender.Budget.ReserveEmailBudget(ctx, provider.Name, provider.Daily, provider.Monthly, time.Now())
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		// Brevo accepts over-quota transactional mail into a backlog. Check its own
		// remaining credits as well as our reservations before submitting an OTP.
		if provider.Name == "brevo" {
			available, err := brevoAvailable(ctx, client, provider)
			if err != nil {
				continue
			}
			if !available {
				continue
			}
		}
		path := "/emails"
		payload := map[string]any{"from": provider.From, "to": []string{to}, "subject": "Moment · 邮箱验证码", "text": fmt.Sprintf("你的 Moment 验证码是 %s。\n\n验证码 10 分钟内有效，请勿与他人分享。如果不是你本人操作，请忽略这封邮件。", code)}
		if provider.Name == "brevo" {
			path = "/v3/smtp/email"
			payload = map[string]any{"sender": map[string]string{"name": "Moment", "email": provider.From}, "to": []map[string]string{{"email": to}}, "subject": payload["subject"], "textContent": payload["text"], "headers": map[string]string{"idempotencyKey": id}}
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.Endpoint+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		if provider.Name == "resend" {
			request.Header.Set("Authorization", "Bearer "+provider.Key)
			request.Header.Set("Idempotency-Key", id)
		} else {
			request.Header.Set("api-key", provider.Key)
		}
		response, err := client.Do(request)
		// A lost response may hide an accepted send. Do not submit it to another
		// provider: provider-scoped idempotency cannot deduplicate across companies.
		if err != nil {
			if sender.Log != nil {
				sender.Log("warn", "email_delivery_unknown", map[string]any{"provider": provider.Name})
			}
			return errors.New("email delivery outcome is unknown")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 65536))
		_ = response.Body.Close()
		var receipt struct {
			ID        string `json:"id"`
			MessageID string `json:"messageId"`
			Name      string `json:"name"`
		}
		decoded := json.Unmarshal(data, &receipt) == nil
		if response.StatusCode >= 200 && response.StatusCode < 300 && readErr == nil && decoded && (receipt.ID != "" || receipt.MessageID != "") {
			return nil
		}
		if sender.Log != nil {
			sender.Log("warn", "email_delivery_refused", map[string]any{"provider": provider.Name, "status": response.StatusCode})
		}
		if provider.Name == "resend" && response.StatusCode == 429 {
			period := ""
			limit := 0
			switch receipt.Name {
			case "daily_quota_exceeded":
				period = time.Now().UTC().Format("2006-01-02")
				limit = provider.Daily
			case "monthly_quota_exceeded":
				period = time.Now().UTC().Format("2006-01")
				limit = provider.Monthly
			}
			if period != "" {
				if err := sender.Budget.ExhaustEmailBudget(ctx, provider.Name, period, limit); err != nil {
					return err
				}
			}
		}
		if response.StatusCode == 429 || response.StatusCode == 401 || response.StatusCode == 403 {
			continue
		}
		return errors.New("email provider did not confirm delivery")
	}
	return errors.New("no email delivery capacity is available")
}

func brevoAvailable(ctx context.Context, client *http.Client, provider Provider) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.Endpoint+"/v3/account", nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("api-key", provider.Key)
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return false, errors.New("email credit status unavailable")
	}
	var account struct {
		Plan []struct {
			Type        string `json:"type"`
			CreditsType string `json:"creditsType"`
			Credits     int    `json:"credits"`
		} `json:"plan"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&account); err != nil {
		return false, err
	}
	for _, plan := range account.Plan {
		if plan.Type != "sms" && plan.CreditsType == "sendLimit" && plan.Credits > 0 {
			return true, nil
		}
	}
	return false, nil
}
