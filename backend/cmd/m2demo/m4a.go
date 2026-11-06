package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/businesshours"
	"chatisp/internal/cannedreplies"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	m4aDriverTemplate = "sim-driver-reinicio"
	m4aFakeTemplate   = "sim-fake-aviso"
)

// m4aInfo is the opt-in simulation fixture. Tokens are ephemeral and local.
type m4aInfo struct {
	ControlURL, ControlToken, Timezone string
	DriverTemplateID, FakeTemplateID   string
	Admin, Outsider, OtherTenant       m3bActor
	Simulation                         string
}

type m4aClock struct {
	mu      sync.Mutex
	now     time.Time
	base    string
	keys    map[string]bool
	bodies  map[string][]byte
	tickets map[string]m4aTicket
	calls   atomic.Int64
}

type m4aTicket struct {
	body []byte
	exp  time.Time
	name string
}

func newM4AClock() *m4aClock {
	return &m4aClock{now: time.Now(), keys: map[string]bool{}, bodies: map[string][]byte{}, tickets: map[string]m4aTicket{}}
}

func (s *m4aClock) advance(d time.Duration) {
	s.mu.Lock()
	s.now = s.now.Add(d)
	s.mu.Unlock()
}

func (s *m4aClock) Create(_ context.Context, key string, r io.ReadSeeker, _ int64) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys[key] {
		return "", attachments.ErrConflict
	}
	s.keys[key] = true
	s.bodies[key+"@v1"] = body
	return "v1", nil
}

func (s *m4aClock) Read(_ context.Context, key, version string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.bodies[key+"@"+version]
	if !ok {
		return nil, attachments.ErrStorage
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (s *m4aClock) Sign(_ context.Context, key, version, name string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.bodies[key+"@"+version]
	if !ok || ttl <= 0 {
		return "", attachments.ErrStorage
	}
	token := uuid.NewString()
	s.tickets[token] = m4aTicket{body: body, exp: s.now.Add(ttl), name: name}
	return s.base + "/simulation/objects/" + token, nil
}

func (s *m4aClock) Delete(_ context.Context, key, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version == "" {
		for item := range s.bodies {
			if len(item) >= len(key)+1 && item[:len(key)+1] == key+"@" {
				delete(s.bodies, item)
			}
		}
		delete(s.keys, key)
		return nil
	}
	delete(s.bodies, key+"@"+version)
	return nil
}

func (s *m4aClock) serveObject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "simulação", http.StatusMethodNotAllowed)
		return
	}
	token := r.PathValue("token")
	s.mu.Lock()
	ticket, ok := s.tickets[token]
	now := s.now
	s.mu.Unlock()
	if !ok {
		http.Error(w, "simulação: URL desconhecida", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-ChatISP-Simulation", "local-attachment")
	if !now.Before(ticket.exp) {
		http.Error(w, "simulação: URL expirada", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	_, _ = w.Write(ticket.body)
}

func (s *m4aClock) wrapMeta(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		next.ServeHTTP(w, r)
	})
}

func prepareM4A(ctx context.Context, store *db.Store, ring *crypto.Ring, tenant, admin, adminMember, inbox uuid.UUID, adminEmail string) (cannedreplies.Service, *channelpolicy.Service, *attachments.Service, businesshours.Service, http.Handler, *m4aInfo, func(), error) {
	clock := newM4AClock()
	info := &m4aInfo{
		ControlToken: uuid.NewString(), Timezone: "America/Sao_Paulo",
		DriverTemplateID: m4aDriverTemplate, FakeTemplateID: m4aFakeTemplate,
		Simulation: "fixture local; catálogo fake explícito não autoriza despacho; template driver foi semeado na simulação e não veio da Graph",
	}
	adminActor, err := insertFixtureSession(ctx, store, ring, tenant, admin, adminMember, "admin", adminEmail)
	if err != nil {
		return cannedreplies.Service{}, nil, nil, businesshours.Service{}, nil, nil, nil, err
	}
	info.Admin = adminActor
	outsider, err := newFixtureActor(ctx, store, ring, tenant, "agent")
	if err != nil {
		return cannedreplies.Service{}, nil, nil, businesshours.Service{}, nil, nil, nil, err
	}
	info.Outsider = outsider
	otherTenant := uuid.New()
	other, err := newFixtureTenantActor(ctx, store, ring, otherTenant)
	if err != nil {
		return cannedreplies.Service{}, nil, nil, businesshours.Service{}, nil, nil, nil, err
	}
	info.OtherTenant = other
	if err = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(c, `
			INSERT INTO inbox_send_leases (tenant_id, inbox_id, owner_id, lease_version, expires_at)
			VALUES ($1, $2, $3, 1, clock_timestamp() + interval '2 hours')`, tenant, inbox, uuid.New())
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("lease de simulação não foi reservado")
		}
		tag, e = tx.Exec(c, `
			INSERT INTO wa_templates (tenant_id, waba_id, external_id, name, language, category, state, components, sync_source, synced_at)
			SELECT $1, id, $2, 'reinicio_simulacao', 'pt_BR', 'utility', 'APPROVED', '[]'::jsonb, 'driver', clock_timestamp()
			  FROM meta_wabas WHERE tenant_id = $1 AND status = 'active'`, tenant, m4aDriverTemplate)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("template de simulação não foi semeado")
		}
		return nil
	}); err != nil {
		return cannedreplies.Service{}, nil, nil, businesshours.Service{}, nil, nil, nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return cannedreplies.Service{}, nil, nil, businesshours.Service{}, nil, nil, nil, err
	}
	info.ControlURL = "http://" + listener.Addr().String()
	clock.base = info.ControlURL
	mux := http.NewServeMux()
	mux.HandleFunc("GET /simulation/status", func(w http.ResponseWriter, r *http.Request) {
		if !simulationAuthorized(r, info.ControlToken) {
			http.Error(w, "simulação não autorizada", http.StatusForbidden)
			return
		}
		writeSimulation(w, map[string]any{"meta_calls": clock.calls.Load(), "simulation": info.Simulation})
	})
	mux.HandleFunc("POST /simulation/close-window", func(w http.ResponseWriter, r *http.Request) {
		if !simulationAuthorized(r, info.ControlToken) {
			http.Error(w, "simulação não autorizada", http.StatusForbidden)
			return
		}
		var aged int64
		err := store.WithTenantTx(r.Context(), tenant, func(c context.Context, tx pgx.Tx) error {
			tag, e := tx.Exec(c, `
				UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours'
				 WHERE tenant_id = $1 AND direction = 'inbound' AND NOT historical`, tenant)
			if e != nil {
				return e
			}
			aged = tag.RowsAffected()
			return nil
		})
		if err != nil {
			http.Error(w, "simulação não recuou a janela", http.StatusInternalServerError)
			return
		}
		writeSimulation(w, map[string]any{"aged_inbound": aged, "lease_released": false, "meta_calls": clock.calls.Load(), "simulation": "âncora elegível recuada 25h; lease e URLs locais permanecem; clock_timestamp do PostgreSQL não muda"})
	})
	mux.HandleFunc("POST /simulation/expire-window", func(w http.ResponseWriter, r *http.Request) {
		if !simulationAuthorized(r, info.ControlToken) {
			http.Error(w, "simulação não autorizada", http.StatusForbidden)
			return
		}
		var aged int64
		var released bool
		err := store.WithTenantTx(r.Context(), tenant, func(c context.Context, tx pgx.Tx) error {
			tag, e := tx.Exec(c, `
				UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours'
				 WHERE tenant_id = $1 AND direction = 'inbound' AND NOT historical`, tenant)
			if e != nil {
				return e
			}
			aged = tag.RowsAffected()
			tag, e = tx.Exec(c, `
				UPDATE inbox_send_leases SET expires_at = clock_timestamp() - interval '1 second'
				 WHERE tenant_id = $1 AND inbox_id = $2 AND expires_at > clock_timestamp()`, tenant, inbox)
			if e != nil {
				return e
			}
			released = tag.RowsAffected() == 1
			return nil
		})
		if err != nil {
			http.Error(w, "simulação não ajustou o relógio", http.StatusInternalServerError)
			return
		}
		clock.advance(25 * time.Hour)
		writeSimulation(w, map[string]any{"aged_inbound": aged, "lease_released": released, "meta_calls": clock.calls.Load(), "simulation": "relógio da fixture: âncora elegível e URLs locais recuados 25h; clock_timestamp do despacho permanece o do PostgreSQL"})
	})
	mux.HandleFunc("GET /simulation/objects/{token}", clock.serveObject)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	policy := &channelpolicy.Service{DB: store, Driver: channelpolicy.FakeCatalog{Explicit: true, Items: []channelpolicy.Template{{
		ExternalID: m4aFakeTemplate, Name: "aviso_simulacao", Language: "pt_BR", Category: "utility", State: "APPROVED", Components: json.RawMessage(`[]`),
	}}}}
	attachmentsSvc := &attachments.Service{DB: store, Storage: clock, Limits: fixtureAttachmentLimits}
	stop := func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		listener.Close()
	}
	return cannedreplies.New(store), policy, attachmentsSvc, businesshours.New(store), clock.wrapMeta(handler()), info, stop, nil
}

func fixtureAttachmentLimits(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (attachments.Limits, error) {
	var plan, reserve, file int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((limits->>'attachment_plan_bytes')::bigint, 0),
		       COALESCE((limits->>'attachment_reserve_bytes')::bigint, 0),
		       COALESCE((limits->>'attachment_file_bytes')::bigint, 0)
		  FROM tenants WHERE id = $1 AND id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`, tenantID).Scan(&plan, &reserve, &file)
	if err != nil || plan <= 0 || file <= 0 || reserve < 0 {
		return attachments.Limits{}, attachments.ErrStorage
	}
	return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: reserve, FileBytes: file}, nil
}

func simulationAuthorized(r *http.Request, token string) bool {
	got := r.Header.Get("X-Simulation-Token")
	return len(got) == len(token) && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func writeSimulation(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-ChatISP-Simulation", "m4a-fixture")
	_ = json.NewEncoder(w).Encode(body)
}

func newFixtureActor(ctx context.Context, store *db.Store, ring *crypto.Ring, tenant uuid.UUID, role string) (m3bActor, error) {
	actor := m3bActor{Token: uuid.NewString(), CSRF: uuid.NewString(), User: uuid.New(), Member: uuid.New(), Role: role}
	email := actor.User.String() + "@example.test"
	if err := store.WithPlatform(ctx, func(c context.Context) error {
		_, e := store.CreateUser(c, db.NewUser{ID: actor.User, Email: email, Status: "active"})
		return e
	}); err != nil {
		return m3bActor{}, err
	}
	if err := store.WithTenant(ctx, tenant, func(c context.Context) error {
		_, e := store.CreateMembership(c, db.NewMembership{ID: actor.Member, UserID: actor.User, Role: role, Status: "active"})
		return e
	}); err != nil {
		return m3bActor{}, err
	}
	return insertFixtureSession(ctx, store, ring, tenant, actor.User, actor.Member, role, email)
}

func newFixtureTenantActor(ctx context.Context, store *db.Store, ring *crypto.Ring, tenant uuid.UUID) (m3bActor, error) {
	if err := store.WithPlatform(ctx, func(c context.Context) error {
		_, e := store.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)})
		return e
	}); err != nil {
		return m3bActor{}, err
	}
	return newFixtureActor(ctx, store, ring, tenant, "admin")
}

func insertFixtureSession(ctx context.Context, store *db.Store, ring *crypto.Ring, tenant, user, member uuid.UUID, role, email string) (m3bActor, error) {
	actor := m3bActor{Token: uuid.NewString(), CSRF: uuid.NewString(), User: user, Member: member, Role: role}
	now, session := time.Now(), uuid.New()
	err := store.WithAuth(ctx, func(c context.Context) error {
		if e := store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(actor.Token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(actor.CSRF), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}); e != nil {
			return e
		}
		return store.SetSessionAssurance(c, session, "full")
	})
	_ = email
	return actor, err
}

func m4aDemo(args []string) error {
	if len(args) != 0 {
		return errors.New("uso: m2demo m4a (fixture descartável loopback)")
	}
	return m3bDemoWith(true)
}

func m4bDemo(args []string) error {
	if len(args) != 0 {
		return errors.New("uso: m2demo m4b (fixture descartável loopback)")
	}
	return m3bDemoFlags(true, true)
}
