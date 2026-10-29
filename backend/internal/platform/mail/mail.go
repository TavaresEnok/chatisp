// Package mail sends transactional messages for invites and password recovery.
// It is not an inbox and it does not log message bodies.
package mail

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// Message is one transactional e-mail. Text may contain a one-time token.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Sender delivers a single message.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Unavailable refuses to send. It is the default when SMTP is not configured.
type Unavailable struct{}

// Send reports that delivery is not configured.
func (Unavailable) Send(context.Context, Message) error {
	return errors.New("envio de e-mail indisponível")
}

// Fake records messages in memory for tests.
type Fake struct {
	mu       sync.Mutex
	Messages []Message
	Err      error
}

// Send appends the message. It does not write a log.
func (f *Fake) Send(_ context.Context, msg Message) error {
	if f == nil {
		return errors.New("envio de e-mail indisponível")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Messages = append(f.Messages, msg)
	return nil
}

// Snapshot returns a copy of the recorded messages.
func (f *Fake) Snapshot() []Message {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.Messages))
	copy(out, f.Messages)
	return out
}

// Validate checks addresses and the subject before anything is handed to SMTP.
func Validate(msg Message) error {
	if !safeAddress(msg.To) {
		return errors.New("destinatário inválido")
	}
	if strings.TrimSpace(msg.Subject) == "" || strings.ContainsAny(msg.Subject, "\r\n") || len(msg.Subject) > 200 {
		return errors.New("assunto inválido")
	}
	if strings.TrimSpace(msg.Text) == "" || strings.ContainsRune(msg.Text, 0) || len(msg.Text) > 8000 {
		return errors.New("mensagem inválida")
	}
	return nil
}

func safeAddress(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 320 || strings.ContainsAny(value, " \t\r\n<>") {
		return false
	}
	at := strings.IndexByte(value, '@')
	return at > 0 && at == strings.LastIndexByte(value, '@') && at < len(value)-1
}
