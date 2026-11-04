package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type m7Box struct {
	Sector, Inbox, SectorName, InboxName string
	Cycles, Messages                     int
}

// m7Info is the opt-in disposable report fixture. Session tokens are ephemeral.
type m7Info struct {
	Admin, Restricted                    m3bActor
	Alfa, Beta                           m7Box
	From, To, Timezone, PanelOrigin, API string
	MetricsURL                           string
	ControlURL, ControlToken             string
	HeatWeekday, HeatHour                int
}

func m7Demo(args []string) error {
	if len(args) != 0 {
		return errors.New("uso: m2demo m7 (fixture descartável loopback)")
	}
	vitePort := os.Getenv("CHATISP_E2E_VITE_PORT")
	viteNumber, err := strconv.Atoi(vitePort)
	if err != nil || viteNumber < 1 || viteNumber > 65535 {
		return errors.New("CHATISP_E2E_VITE_PORT inválida")
	}
	vite, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(viteNumber))
	if err != nil {
		return err
	}
	cert, err := m7Certificate()
	if err != nil {
		return err
	}
	listeners := make([]net.Listener, 0, 4)
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	for range 4 {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return e
		}
		listeners = append(listeners, l)
	}
	panel := listeners[3].Addr().(*net.TCPAddr)
	origin := "https://127.0.0.1:" + strconv.Itoa(panel.Port)
	if err = os.Setenv("CHATISP_M7_PANEL_ORIGIN", origin); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	api, err := url.Parse("http://" + listeners[0].Addr().String())
	if err != nil {
		return err
	}
	proxy := &http.Server{Handler: panelProxy(api, vite), ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	go func() { _ = proxy.ServeTLS(listeners[3], "", "") }()
	defer proxy.Close()
	ready, done := make(chan demoInfo, 1), make(chan error, 1)
	go func() {
		done <- runDemoMode(ctx, listeners[0], listeners[1], listeners[2], ready, false, false, false, true)
	}()
	select {
	case info := <-ready:
		if info.M7 == nil || info.M7.PanelOrigin != origin {
			stop()
			<-done
			return errors.New("fixture M7 incompleta")
		}
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

func m7Certificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1 M7 disposable"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, err
}

func seedM7(ctx context.Context, store *db.Store, ring *crypto.Ring, svc sectors.Service, principal sectors.Principal, tenant, admin, adminMember uuid.UUID) (*m7Info, error) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return nil, err
	}
	now := time.Now().In(loc)
	base := now.Add(-2 * time.Hour)
	base = time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), 10, 0, 0, loc)
	alfaOpen := []time.Time{base, base.Add(15 * time.Minute)}
	betaOpen := base.Add(30 * time.Minute)
	info := &m7Info{
		Timezone:    "America/Sao_Paulo",
		From:        base.Format("2006-01-02"),
		To:          base.Format("2006-01-02"),
		HeatWeekday: int(base.Weekday()),
		HeatHour:    base.Hour(),
		Alfa:        m7Box{SectorName: "Setor Alfa M7", InboxName: "Caixa Alfa M7", Cycles: 2, Messages: 4},
		Beta:        m7Box{SectorName: "Setor Beta M7", InboxName: "Caixa Beta M7", Cycles: 1, Messages: 1},
	}
	info.Admin, err = insertFixtureSession(ctx, store, ring, tenant, admin, adminMember, "admin", admin.String()+"@example.test")
	if err != nil {
		return nil, err
	}
	restrictedUser, restrictedMember := uuid.New(), uuid.New()
	if err = store.WithPlatform(ctx, func(c context.Context) error {
		_, e := store.CreateUser(c, db.NewUser{ID: restrictedUser, Email: restrictedUser.String() + "@example.test", Status: "active"})
		return e
	}); err != nil {
		return nil, err
	}
	if err = store.WithTenant(ctx, tenant, func(c context.Context) error {
		_, e := store.CreateMembership(c, db.NewMembership{ID: restrictedMember, UserID: restrictedUser, Role: "agent", Status: "active"})
		return e
	}); err != nil {
		return nil, err
	}
	info.Restricted, err = insertFixtureSession(ctx, store, ring, tenant, restrictedUser, restrictedMember, "agent", restrictedUser.String()+"@example.test")
	if err != nil {
		return nil, err
	}
	alfaSector, err := svc.Create(ctx, principal, info.Alfa.SectorName)
	if err != nil {
		return nil, err
	}
	betaSector, err := svc.Create(ctx, principal, info.Beta.SectorName)
	if err != nil {
		return nil, err
	}
	info.Alfa.Sector = alfaSector.ID.String()
	info.Beta.Sector = betaSector.ID.String()
	alfaInbox, betaInbox := uuid.New(), uuid.New()
	info.Alfa.Inbox = alfaInbox.String()
	info.Beta.Inbox = betaInbox.String()
	alfaConnector, betaConnector := uuid.New(), uuid.New()
	if err = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		for _, item := range []struct {
			id   uuid.UUID
			name string
		}{
			{alfaConnector, "m7-alfa-" + alfaConnector.String()},
			{betaConnector, "m7-beta-" + betaConnector.String()},
		} {
			if _, e := tx.Exec(c, `INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled) VALUES ($1,$2,'meta_cloud',$3,'connected',true)`, item.id, tenant, item.name); e != nil {
				return e
			}
		}
		_, e := tx.Exec(c, `INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1,$2,$3,$4), ($5,$2,$6,$7)`, alfaInbox, tenant, alfaConnector, info.Alfa.InboxName, betaInbox, betaConnector, info.Beta.InboxName)
		return e
	}); err != nil {
		return nil, err
	}
	if err = svc.Bind(ctx, principal, alfaSector.ID, alfaInbox, "inbox", true); err != nil {
		return nil, err
	}
	if err = svc.Bind(ctx, principal, betaSector.ID, betaInbox, "inbox", true); err != nil {
		return nil, err
	}
	if err = svc.SetDefault(ctx, principal, alfaSector.ID, alfaInbox); err != nil {
		return nil, err
	}
	if err = svc.SetDefault(ctx, principal, betaSector.ID, betaInbox); err != nil {
		return nil, err
	}
	if err = svc.Bind(ctx, principal, alfaSector.ID, restrictedMember, "membership", true); err != nil {
		return nil, err
	}
	if err = svc.Bind(ctx, principal, betaSector.ID, restrictedMember, "membership", true); err != nil {
		return nil, err
	}
	contact := uuid.New()
	if err = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(c, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1,$2,'Cliente M7')`, contact, tenant); e != nil {
			return e
		}
		type row struct {
			inbox, sector uuid.UUID
			open          time.Time
			protocol      string
			messages      int
		}
		rows := []row{
			{alfaInbox, alfaSector.ID, alfaOpen[0], "M7-A1", 2},
			{alfaInbox, alfaSector.ID, alfaOpen[1], "M7-A2", 2},
			{betaInbox, betaSector.ID, betaOpen, "M7-B1", 1},
		}
		for _, item := range rows {
			identity := uuid.New()
			conv := uuid.New()
			connector := alfaConnector
			if item.inbox == betaInbox {
				connector = betaConnector
			}
			if _, e := tx.Exec(c, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, "m7-"+identity.String()); e != nil {
				return e
			}
			closed := item.open.Add(10 * time.Minute)
			if _, e := tx.Exec(c, `INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'closed','human',$6,$7,'202611',1,$8,$9,$10)`, conv, tenant, item.inbox, contact, identity, adminMember, item.protocol, item.sector, item.open, closed); e != nil {
				return e
			}
			if _, e := tx.Exec(c, `INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind) VALUES ($1,$2,$3,1,$4,$5,'simulacao','resolved','ciclo sintetico',$6,$7,'human')`, tenant, uuid.New(), conv, item.open, closed, admin, adminMember); e != nil {
				return e
			}
			if _, e := tx.Exec(c, `INSERT INTO conversation_events (tenant_id,id,conversation_id,cycle_number,kind,actor_id,at,version,controller_epoch,controller,assignee_id) VALUES ($1,$2,$3,1,'assumed',$4,$5,2,1,'human',$6)`, tenant, uuid.New(), conv, admin, item.open, adminMember); e != nil {
				return e
			}
			for n := 0; n < item.messages; n++ {
				direction, origin := "inbound", "customer"
				var author any
				if n == 1 {
					direction, origin, author = "outbound", "human", adminMember
				}
				at := item.open.Add(time.Duration(n+1) * time.Minute)
				if _, e := tx.Exec(c, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,author_membership_id,trusted_at,ingested_at) VALUES ($1,$2,$3,$4,$5,$6,'public',$7,$8,$9,$10,$10)`, uuid.New(), tenant, conv, connector, direction, origin, item.protocol+"-"+strconv.Itoa(n), map[bool]string{true: "accepted", false: "received"}[n == 1], author, at); e != nil {
					return e
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	info.ControlURL, info.ControlToken, err = serveM7Control(ctx, svc, principal, betaSector.ID, restrictedMember)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// serveM7Control revoga um vínculo sintético em loopback. O token não sai no log.
func serveM7Control(ctx context.Context, svc sectors.Service, principal sectors.Principal, sector, member uuid.UUID) (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /revoke-beta", func(w http.ResponseWriter, r *http.Request) {
		given := []byte(r.Header.Get("X-M7-Control"))
		if subtle.ConstantTimeCompare(given, []byte(token)) != 1 {
			http.NotFound(w, r)
			return
		}
		if err := svc.Bind(r.Context(), principal, sector, member, "membership", false); err != nil {
			http.Error(w, "revogação indisponível", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shut)
	}()
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String() + "/revoke-beta", token, nil
}
