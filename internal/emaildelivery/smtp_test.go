package emaildelivery

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

type smtpRecord struct {
	authenticated  bool
	from, to, body string
}

func smtpFixture(t *testing.T, mode string) (smtpConfig, <-chan smtpRecord) {
	t.Helper()
	certServer := httptest.NewTLSServer(nil)
	t.Cleanup(certServer.Close)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", certServer.TLS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	records := make(chan smtpRecord, 1)
	go func() {
		var record smtpRecord
		defer func() { records <- record }()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		wire := textproto.NewConn(conn)
		if mode == "stall" {
			_, _ = wire.ReadLine()
			return
		}
		_ = wire.PrintfLine("220 localhost ESMTP")
		for {
			line, err := wire.ReadLine()
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_ = wire.PrintfLine("250-localhost\r\n250 AUTH PLAIN")
			case strings.HasPrefix(line, "AUTH PLAIN "):
				auth, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
				record.authenticated = string(auth) == "\x00sender@example.test\x00test-app-password"
				if mode == "auth" || !record.authenticated {
					_ = wire.PrintfLine("535 authentication refused")
					return
				}
				_ = wire.PrintfLine("235 authenticated")
			case strings.HasPrefix(line, "MAIL FROM:"):
				record.from = strings.TrimPrefix(line, "MAIL FROM:")
				_ = wire.PrintfLine("250 sender accepted")
			case strings.HasPrefix(line, "RCPT TO:"):
				record.to = strings.TrimPrefix(line, "RCPT TO:")
				if mode == "recipient" {
					_ = wire.PrintfLine("550 recipient refused")
					return
				}
				_ = wire.PrintfLine("250 recipient accepted")
			case line == "DATA":
				_ = wire.PrintfLine("354 send data")
				body, _ := wire.ReadDotBytes()
				record.body = string(body)
				if mode == "lost" {
					return
				}
				_ = wire.PrintfLine("250 queued")
			default:
				_ = wire.PrintfLine("500 unexpected command")
				return
			}
		}
	}()
	return smtpConfig{address: listener.Addr().String(), username: "sender@example.test", password: "test-app-password", tlsConfig: certServer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}, records
}

func TestSMTPVerificationDeliveredOverAuthenticatedTLS(t *testing.T) {
	config, records := smtpFixture(t, "success")
	if err := sendSMTPVerification(context.Background(), config, "reader@example.test", "123456", "challenge-id"); err != nil {
		t.Fatal(err)
	}
	record := <-records
	if !record.authenticated || record.from != "<sender@example.test>" || record.to != "<reader@example.test>" {
		t.Fatalf("incorrect SMTP envelope: %+v", record)
	}
	message, err := mail.ReadMessage(strings.NewReader(record.body))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || subject != "Moment · 邮箱验证码" {
		t.Fatalf("invalid encoded subject: %q", subject)
	}
	body, _ := io.ReadAll(message.Body)
	if !strings.Contains(string(body), "123456") || strings.Contains(record.body, config.password) {
		t.Fatal("code missing or credential leaked into message")
	}
	if message.Header.Get("Message-ID") != "<challenge-id@example.test>" {
		t.Fatal("missing stable message identity")
	}
}

func TestSMTPFailuresDoNotReportAcceptedDelivery(t *testing.T) {
	for _, mode := range []string{"auth", "recipient", "lost"} {
		t.Run(mode, func(t *testing.T) {
			config, records := smtpFixture(t, mode)
			err := sendSMTPVerification(context.Background(), config, "reader@example.test", "123456", "challenge-id")
			if err == nil || strings.Contains(err.Error(), config.password) {
				t.Fatal("failure hidden or credentials exposed")
			}
			record := <-records
			if mode != "lost" && record.body != "" {
				t.Fatal("sent DATA after a refusal")
			}
		})
	}
}

func TestSMTPRejectsUntrustedTLS(t *testing.T) {
	config, records := smtpFixture(t, "success")
	config.tlsConfig = nil
	if err := sendSMTPVerification(context.Background(), config, "reader@example.test", "123456", "id"); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
	<-records
}

func TestSMTPCancellationInterruptsGreeting(t *testing.T) {
	config, records := smtpFixture(t, "stall")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := sendSMTPVerification(ctx, config, "reader@example.test", "123456", "id"); err == nil {
		t.Fatal("accepted stalled delivery")
	}
	if time.Since(start) > time.Second {
		t.Fatal("request deadline was ignored")
	}
	<-records
}

func TestSMTPRejectsHeaderInjectionBeforeConnecting(t *testing.T) {
	config := smtpConfig{username: "sender@example.test"}
	for _, to := range []string{"reader@example.test\r\nBcc: other@example.test", "not-an-address"} {
		if err := sendSMTPVerification(context.Background(), config, to, "123456", "id"); err == nil || err.Error() != "invalid smtp recipient address" {
			t.Fatal("invalid recipient was not rejected before connecting")
		}
	}
}

func TestEnvironmentLoadsOnlyCompleteGmailCredentials(t *testing.T) {
	t.Setenv("MOMENT_GMAIL_USERNAME", " sender@example.test ")
	t.Setenv("MOMENT_GMAIL_APP_PASSWORD", "aaaa bbbb cccc dddd")
	sender := NewFromEnvironment(testBudget{true})
	if sender.Username != "sender@example.test" || sender.Password != "aaaabbbbccccdddd" {
		t.Fatal("Gmail configuration was not loaded")
	}
	t.Setenv("MOMENT_GMAIL_APP_PASSWORD", "")
	sender = NewFromEnvironment(testBudget{true})
	if err := sender.SendVerification(context.Background(), "reader@example.test", "123456", "id"); err == nil {
		t.Fatal("incomplete Gmail configuration was enabled")
	}
}
