// Package emaildelivery sends registration verification mail through Gmail SMTP.
package emaildelivery

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

// Budget reserves durable daily capacity before a message is sent.
type Budget interface {
	ReserveEmailBudget(context.Context, int, time.Time) (bool, error)
}

type Sender struct {
	Username, Password string
	Budget             Budget
	Log                func(string, string, map[string]any)
}

// NewFromEnvironment keeps credentials in process configuration, never site data.
func NewFromEnvironment(budget Budget) *Sender {
	return &Sender{
		Username: strings.TrimSpace(os.Getenv("MOMENT_GMAIL_USERNAME")),
		Password: strings.Join(strings.Fields(os.Getenv("MOMENT_GMAIL_APP_PASSWORD")), ""),
		Budget:   budget,
	}
}

func (sender *Sender) SendVerification(ctx context.Context, to, code, id string) error {
	if sender == nil || sender.Budget == nil || sender.Username == "" || sender.Password == "" {
		return errors.New("email delivery is not configured")
	}
	allowed, err := sender.Budget.ReserveEmailBudget(ctx, 500, time.Now())
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("daily email delivery capacity is exhausted")
	}
	err = sendSMTPVerification(ctx, smtpConfig{
		address: "smtp.gmail.com:465", username: sender.Username, password: sender.Password,
	}, to, code, id)
	if err != nil && sender.Log != nil {
		sender.Log("warn", "email_delivery_failed", map[string]any{"provider": "gmail", "reason": err.Error()})
	}
	// A lost DATA acknowledgment may conceal an accepted message. Do not retry.
	return err
}
