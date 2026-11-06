package main

import (
	"context"
	"net"
	"testing"
	"time"

	"chatisp/internal/platform/mail"
)

// The double must speak enough SMTP for the real sender, keep what it receives
// and refuse delivery on demand.
func TestSinkAcceptsRealSenderAndCanRefuse(t *testing.T) {
	s := &sink{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	sender := mail.NewSender(ln.Addr().String(), "", "", "chat@validacao.example")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sender.Send(ctx, mail.Message{To: "alguem@example.com", Subject: "Convite", Text: "Abra: https://x/convite?token=abc\nabc\n"}); err != nil {
		t.Fatal(err)
	}
	if len(s.messages) != 1 || s.messages[0].To != "alguem@example.com" || s.messages[0].Subject != "Convite" || s.messages[0].Text != "Abra: https://x/convite?token=abc\nabc\n" {
		t.Fatalf("mensagens: %+v", s.messages)
	}
	s.reject.Store(true)
	if err := sender.Send(ctx, mail.Message{To: "outro@example.com", Subject: "Convite", Text: "x\n"}); err == nil {
		t.Fatal("modo de falha entregou a mensagem")
	}
	if len(s.messages) != 1 {
		t.Fatalf("mensagem recusada foi guardada: %d", len(s.messages))
	}
}
