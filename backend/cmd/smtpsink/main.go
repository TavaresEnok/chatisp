// Command smtpsink is a DISPOSABLE VALIDATION DOUBLE for the Compose journey.
// It is not part of the product image. It accepts STARTTLS SMTP on -smtp,
// keeps the messages in memory and serves them as JSON on -http so the journey
// can read invitation and password-reset links. POST /mode?reject=1 makes it
// refuse every recipient (delivery-failure test); reject=0 restores delivery.
// It listens on every interface of its container only.
package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type message struct {
	From       string    `json:"from"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
	Text       string    `json:"text"`
	ReceivedAt time.Time `json:"received_at"`
}

type sink struct {
	mu        sync.Mutex
	messages  []message
	reject    atomic.Bool
	tlsConfig *tls.Config
}

func (s *sink) add(m message) {
	s.mu.Lock()
	s.messages = append(s.messages, m)
	s.mu.Unlock()
}

func parse(from, to, raw string) message {
	m := message{From: from, To: to, ReceivedAt: time.Now().UTC(), Text: raw}
	if parsed, err := mail.ReadMessage(strings.NewReader(raw)); err == nil {
		m.Subject = parsed.Header.Get("Subject")
		var b strings.Builder
		sc := bufio.NewScanner(parsed.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			b.WriteString(sc.Text())
			b.WriteString("\n")
		}
		m.Text = b.String()
	}
	return m
}

func (s *sink) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	r := bufio.NewReader(conn)
	say := func(format string, args ...any) { fmt.Fprintf(conn, format+"\r\n", args...) }
	say("220 smtpsink pronto (duplo de validação)")
	var from, to string
	secure := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			say("250-smtpsink")
			if !secure {
				say("250-STARTTLS")
			}
			say("250 8BITMIME")
		case cmd == "STARTTLS" && !secure:
			say("220 inicie TLS")
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			r = bufio.NewReader(conn)
			secure = true
			from, to = "", ""
		case strings.HasPrefix(cmd, "HELO"):
			say("250 smtpsink")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !secure {
				say("530 STARTTLS obrigatório")
				continue
			}
			from = strings.Trim(strings.TrimSpace(line[len("MAIL FROM:"):]), "<>")
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if s.reject.Load() {
				say("554 destinatário recusado (modo de falha do duplo)")
				continue
			}
			to = strings.Trim(strings.TrimSpace(line[len("RCPT TO:"):]), "<>")
			say("250 ok")
		case cmd == "DATA":
			say("354 envie, terminando com ponto")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				l = strings.TrimPrefix(l, ".")
				b.WriteString(l)
			}
			s.add(parse(from, to, strings.ReplaceAll(b.String(), "\r\n", "\n")))
			say("250 aceito")
		case cmd == "RSET":
			from, to = "", ""
			say("250 ok")
		case cmd == "NOOP":
			say("250 ok")
		case cmd == "QUIT":
			say("221 tchau")
			return
		default:
			say("502 comando não implementado")
		}
	}
}

func main() {
	smtpAddr := flag.String("smtp", ":2525", "endereço SMTP")
	httpAddr := flag.String("http", ":8025", "endereço HTTP de leitura")
	certPath := flag.String("cert", "", "certificado TLS do laboratório")
	keyPath := flag.String("key", "", "chave TLS do laboratório")
	flag.Parse()
	cert, err := tls.LoadX509KeyPair(*certPath, *keyPath)
	if err != nil {
		log.Fatal("certificado TLS do laboratório inválido")
	}
	s := &sink{tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	ln, err := net.Listen("tcp", *smtpAddr)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /messages", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		out := append([]message{}, s.messages...)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("POST /mode", func(w http.ResponseWriter, r *http.Request) {
		s.reject.Store(r.URL.Query().Get("reject") == "1")
		fmt.Fprintf(w, "reject=%v\n", s.reject.Load())
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	log.Fatal(http.ListenAndServe(*httpAddr, mux))
}
