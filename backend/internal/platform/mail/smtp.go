package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// smtpExchangeTimeout bounds the TCP dial and every later SMTP read and write.
// The dialer timeout alone does not cover the greeting or the commands.
const smtpExchangeTimeout = 10 * time.Second

// SMTP sends one message through an already available SMTP service.
// Credentials and message bodies are not written to logs by this type.
type SMTP struct {
	Addr     string
	Username string
	Password string
	From     string
	// timeout overrides smtpExchangeTimeout. Zero keeps the default.
	// Tests use a short value so a silent peer cannot stall the suite.
	timeout time.Duration
}

// NewSender returns an SMTP sender when addr is set, otherwise Unavailable.
func NewSender(addr, username, password, from string) Sender {
	if strings.TrimSpace(addr) == "" {
		return Unavailable{}
	}
	return SMTP{Addr: addr, Username: username, Password: password, From: from}
}

// Send delivers one plaintext message. The returned error never includes the password or the body.
func (s SMTP) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safeAddress(s.From) {
		return errors.New("remetente inválido")
	}
	if err := Validate(msg); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil || host == "" {
		return errors.New("servidor de e-mail inválido")
	}
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	body := buildMessage(s.From, msg)
	conn, err := s.dial(ctx)
	if err != nil {
		return errors.New("falha ao enviar e-mail")
	}
	defer conn.Close()
	// Cancel closes the socket. Otherwise NewClient waits for a greeting
	// that never arrives, and the caller's context cannot unblock it.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("falha ao enviar e-mail")
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return errors.New("falha ao enviar e-mail")
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return errors.New("falha ao enviar e-mail")
		}
	}
	if err := client.Mail(s.From); err != nil {
		return errors.New("falha ao enviar e-mail")
	}
	if err := client.Rcpt(msg.To); err != nil {
		return errors.New("falha ao enviar e-mail")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("falha ao enviar e-mail")
	}
	if _, err := writer.Write(body); err != nil {
		_ = writer.Close()
		return errors.New("falha ao enviar e-mail")
	}
	if err := writer.Close(); err != nil {
		return errors.New("falha ao enviar e-mail")
	}
	_ = client.Quit()
	return nil
}

func (s SMTP) dial(ctx context.Context) (net.Conn, error) {
	deadline := s.exchangeDeadline(ctx)
	budget := time.Until(deadline)
	if budget < 0 {
		budget = 0
	}
	dialer := net.Dialer{Timeout: budget}
	conn, err := dialer.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s SMTP) exchangeDeadline(ctx context.Context) time.Time {
	timeout := s.timeout
	if timeout <= 0 {
		timeout = smtpExchangeTimeout
	}
	deadline := time.Now().Add(timeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		return until
	}
	return deadline
}

func buildMessage(from string, msg Message) []byte {
	text := strings.ReplaceAll(msg.Text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(text)
	return []byte(b.String())
}
