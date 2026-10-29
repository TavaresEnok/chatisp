package mail

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestFakeRecordsWithoutLoggingBodies(t *testing.T) {
	fake := &Fake{}
	msg := Message{To: "pessoa@example.com", Subject: "Convite para o Chat ISP", Text: "token-unico\n"}
	if err := fake.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	got := fake.Snapshot()
	if len(got) != 1 || got[0].Text != msg.Text {
		t.Fatalf("mensagens: %#v", got)
	}
	fake.Err = errUnavailable()
	if err := fake.Send(context.Background(), msg); err == nil {
		t.Fatal("esperava falha")
	}
	if len(fake.Snapshot()) != 1 {
		t.Fatal("falha não deveria gravar a mensagem")
	}
}

func TestSMTPRejectsBadSenderWithoutPassword(t *testing.T) {
	const secret = "super-secret-smtp"
	sender := NewSender("", "user", secret, "painel@example.com")
	if _, ok := sender.(Unavailable); !ok {
		t.Fatalf("sem endereço deveria recusar: %T", sender)
	}
	smtp := SMTP{Addr: "127.0.0.1:2525", Username: "user", Password: secret, From: "invalido"}
	err := smtp.Send(context.Background(), Message{To: "pessoa@example.com", Subject: "Assunto", Text: "corpo"})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "corpo") {
		t.Fatalf("erro: %v", err)
	}
	body := string(buildMessage("painel@example.com", Message{To: "pessoa@example.com", Subject: "Assunto", Text: "linha"}))
	if strings.Contains(body, secret) || !strings.Contains(body, "linha") {
		t.Fatal("montagem da mensagem inesperada")
	}
}

func TestSMTPDeliversToSpeakingPeer(t *testing.T) {
	addr := listenSMTP(t, true, nil)
	sender := SMTP{Addr: addr, From: "painel@example.com"}
	err := sender.Send(context.Background(), Message{
		To:      "pessoa@example.com",
		Subject: "Assunto",
		Text:    "linha",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSMTPSilentPeerStopsOnDeadlineAndCancel(t *testing.T) {
	const secret = "smtp-secret-silent"
	msg := Message{To: "pessoa@example.com", Subject: "Assunto", Text: "corpo-secreto"}

	t.Run("prazo", func(t *testing.T) {
		addr := listenSMTP(t, false, nil)
		sender := SMTP{
			Addr:     addr,
			Username: "user",
			Password: secret,
			From:     "painel@example.com",
			timeout:  200 * time.Millisecond,
		}
		started := time.Now()
		err := sender.Send(context.Background(), msg)
		assertMailFailure(t, err, secret, time.Since(started))
	})

	t.Run("cancelamento", func(t *testing.T) {
		accepted := make(chan struct{})
		addr := listenSMTP(t, false, accepted)
		sender := SMTP{
			Addr:     addr,
			Username: "user",
			Password: secret,
			From:     "painel@example.com",
			timeout:  time.Minute,
		}
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		started := time.Now()
		go func() {
			errCh <- sender.Send(ctx, msg)
		}()
		select {
		case <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatal("o servidor não aceitou a conexão")
		}
		cancel()
		select {
		case err := <-errCh:
			assertMailFailure(t, err, secret, time.Since(started))
		case <-time.After(2 * time.Second):
			t.Fatal("o envio ficou preso depois do cancelamento")
		}
	})
}

func assertMailFailure(t *testing.T, err error, secret string, elapsed time.Duration) {
	t.Helper()
	if err == nil {
		t.Fatal("esperava falha")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "corpo-secreto") {
		t.Fatal("erro expôs segredo")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("envio demorou %s", elapsed)
	}
}

func listenSMTP(t *testing.T, greet bool, accepted chan struct{}) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if !greet {
			if accepted != nil {
				close(accepted)
			}
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		serveSMTP(conn)
	}()
	return ln.Addr().String()
}

func serveSMTP(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err := conn.Write([]byte("220 localhost ESMTP\r\n")); err != nil {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			_, _ = conn.Write([]byte("250 hello\r\n"))
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
			_, _ = conn.Write([]byte("250 ok\r\n"))
		case strings.HasPrefix(cmd, "DATA"):
			_, _ = conn.Write([]byte("354 go\r\n"))
			for {
				body, err := reader.ReadString('\n')
				if err != nil || body == ".\r\n" {
					break
				}
			}
			_, _ = conn.Write([]byte("250 queued\r\n"))
		case strings.HasPrefix(cmd, "QUIT"):
			_, _ = conn.Write([]byte("221 bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("250 ok\r\n"))
		}
	}
}

func errUnavailable() error {
	return Unavailable{}.Send(context.Background(), Message{})
}
