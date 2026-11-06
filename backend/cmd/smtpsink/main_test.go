package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"chatisp/internal/platform/mail"
)

// The double must speak enough SMTP for the real sender, keep what it receives
// and refuse delivery on demand.
func TestSinkAcceptsRealSenderAndCanRefuse(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "validation"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certFile)
	s := &sink{tlsConfig: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}}
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
