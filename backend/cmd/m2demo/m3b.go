package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
)

// Only the opt-in disposable fixture emits these ephemeral session secrets.
type m3bActor struct {
	Token, CSRF  string
	User, Member uuid.UUID
	Role         string
}
type m3bInfo struct {
	Tenant, Inbox, Source, Destination uuid.UUID
	Actors                             []m3bActor
}

func seedM3B(ctx context.Context, store *db.Store, ring *crypto.Ring, svc sectors.Service, p sectors.Principal, source, inbox, first uuid.UUID) (*m3bInfo, error) {
	dest, err := svc.Create(ctx, p, "SIMULAÇÃO Destino M3B")
	if err != nil {
		return nil, err
	}
	if err = svc.Bind(ctx, p, dest.ID, inbox, "inbox", true); err != nil {
		return nil, err
	}
	info := &m3bInfo{Tenant: p.TenantID, Inbox: inbox, Source: source, Destination: dest.ID}
	// First agent sees source only; second sees both; supervisor has its normal policy.
	for i, role := range []string{"agent", "agent", "supervisor"} {
		a := m3bActor{Token: uuid.NewString(), CSRF: uuid.NewString(), User: uuid.New(), Member: uuid.New(), Role: role}
		if err = store.WithPlatform(ctx, func(c context.Context) error {
			_, e := store.CreateUser(c, db.NewUser{ID: a.User, Email: a.User.String() + "@example.test", Status: "active"})
			return e
		}); err != nil {
			return nil, err
		}
		if err = store.WithTenant(ctx, p.TenantID, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: a.Member, UserID: a.User, Role: role, Status: "active"})
			return e
		}); err != nil {
			return nil, err
		}
		if i < 2 {
			if err = svc.Bind(ctx, p, source, a.Member, "membership", true); err != nil {
				return nil, err
			}
		}
		if i == 1 {
			if err = svc.Bind(ctx, p, dest.ID, a.Member, "membership", true); err != nil {
				return nil, err
			}
		}
		now, session := time.Now(), uuid.New()
		if err = store.WithAuth(ctx, func(c context.Context) error {
			sealed, e := ring.Encrypt([]byte("simulation-secret"), crypto.Context{TenantID: p.TenantID.String(), Entity: "user", EntityID: a.User.String(), Field: "totp_secret"})
			if e != nil {
				return e
			}
			if e = store.BeginMFA(c, a.User, p.TenantID, sealed.Ciphertext, sealed.Version); e != nil {
				return e
			}
			if _, e = store.ConfirmMFA(c, a.User, now); e != nil {
				return e
			}
			if e = store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(a.Token), UserID: a.User, TenantID: p.TenantID, MembershipID: a.Member, CSRFHash: auth.HashSecret(a.CSRF), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}); e != nil {
				return e
			}
			return store.SetSessionAssurance(c, session, "full")
		}); err != nil {
			return nil, err
		}
		info.Actors = append(info.Actors, a)
	}
	// Remove the ordinary demo agent from candidates in this opt-in scenario.
	if err = svc.Bind(ctx, p, source, first, "membership", false); err != nil {
		return nil, err
	}
	return info, nil
}

func m3bCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost M3B disposable"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, err
}

func m3bDemo(args []string) error {
	if len(args) != 0 {
		return errors.New("uso: m2demo m3b (fixture descartável loopback)")
	}
	return m3bDemoWith(false)
}

func m3bDemoWith(m4a bool) error {
	cert, err := m3bCertificate()
	if err != nil {
		return err
	}
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:9090", "127.0.0.1:18081", "127.0.0.1:5174"} {
		l, e := net.Listen("tcp", addr)
		if e != nil {
			return e
		}
		listeners = append(listeners, l)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	api, _ := url.Parse("http://127.0.0.1:8080")
	vite, _ := url.Parse("http://127.0.0.1:5173")
	// ServeTLS negotiates HTTP/2. A raw TLS listener stays on HTTP/1.1, and three
	// panel contexts then stall behind Chromium's per-host connection cap.
	proxy := &http.Server{Handler: panelProxy(api, vite), ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	go proxy.ServeTLS(listeners[3], "", "")
	defer proxy.Close()
	ready, done := make(chan demoInfo, 1), make(chan error, 1)
	go func() { done <- runDemoMode(ctx, listeners[0], listeners[1], listeners[2], ready, true, m4a) }()
	select {
	case info := <-ready:
		if err = json.NewEncoder(os.Stdout).Encode(info); err != nil {
			stop()
			<-done
			return err
		}
		return <-done
	case err = <-done:
		return err
	}
}
