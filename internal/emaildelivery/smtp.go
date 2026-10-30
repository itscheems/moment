package emaildelivery

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type smtpConfig struct {
	address, username, password string
	tlsConfig                   *tls.Config
}

// sendSMTPVerification uses implicit TLS before sending any credentials.
// Success means the SMTP server acknowledged DATA, not inbox delivery.
func sendSMTPVerification(ctx context.Context, config smtpConfig, to, code, id string) error {
	from, err := mail.ParseAddress(config.username)
	if err != nil || from.Address != config.username {
		return errors.New("invalid smtp sender address")
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil || recipient.Address != to {
		return errors.New("invalid smtp recipient address")
	}
	host, _, err := net.SplitHostPort(config.address)
	if err != nil {
		return errors.New("invalid smtp server address")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tlsConfig := config.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: tlsConfig}
	conn, err := dialer.DialContext(ctx, "tcp", config.address)
	if err != nil {
		return errors.New("smtp connection failed")
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return errors.New("smtp deadline failed")
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("smtp greeting failed")
	}
	defer func() { _ = client.Close() }()
	if err := client.Auth(smtp.PlainAuth("", config.username, config.password, host)); err != nil {
		return errors.New("smtp authentication failed")
	}
	if err := client.Mail(from.Address); err != nil {
		return errors.New("smtp sender refused")
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return errors.New("smtp recipient refused")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("smtp message refused")
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n你的 Moment 验证码是 %s。\r\n\r\n验证码 10 分钟内有效，请勿与他人分享。如果不是你本人操作，请忽略这封邮件。\r\n",
		(&mail.Address{Name: "Moment", Address: from.Address}).String(), recipient.String(),
		mime.QEncoding.Encode("UTF-8", "Moment · 邮箱验证码"), time.Now().Format(time.RFC1123Z),
		id, strings.SplitN(from.Address, "@", 2)[1], code)
	if _, err := writer.Write([]byte(message)); err != nil {
		return errors.New("smtp delivery outcome is unknown")
	}
	if err := writer.Close(); err != nil {
		return errors.New("smtp delivery was not confirmed")
	}
	// QUIT is cleanup; a lost QUIT reply does not undo acknowledged DATA.
	return nil
}
