package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/pipeline"
	"chatisp/internal/attendance/service"
	"chatisp/internal/businesshours"
	"chatisp/internal/cannedreplies"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/contactdocs"
	"chatisp/internal/lifecycle"
	"chatisp/internal/notifications"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	panelapi "chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/runtime"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/presence"
	"chatisp/internal/realtime"
	"chatisp/internal/sectors"
	"chatisp/internal/triage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
)

type demoInfo struct {
	Email, Password, App, Secret, Waba, Phone string
	M3B                                       *m3bInfo `json:",omitempty"`
	M4A                                       *m4aInfo `json:",omitempty"`
}

// runDemo owns a fresh embedded cluster and stops the runtime before removing it.
func runDemo(ctx context.Context, apiListener, metricsListener, fakeListener net.Listener, ready chan<- demoInfo) error {
	return runDemoMode(ctx, apiListener, metricsListener, fakeListener, ready, false, false)
}

func runDemoMode(ctx context.Context, apiListener, metricsListener, fakeListener net.Listener, ready chan<- demoInfo, m3b, m4a bool) error {
	for _, key := range []string{"CHATISP_TEST_DATABASE_URL", "CHATISP_DATABASE_URL", "CHATISP_APP_DATABASE_URL", "CHATISP_PLATFORM_DATABASE_URL", "CHATISP_INGRESS_DATABASE_URL", "CHATISP_JOBS_DATABASE_URL"} {
		if _, ok := os.LookupEnv(key); ok {
			return fmt.Errorf("%s definido: demo recusa DSNs preexistentes", key)
		}
	}
	for _, l := range []net.Listener{apiListener, metricsListener, fakeListener} {
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok || !addr.IP.IsLoopback() {
			return errors.New("demo exige listeners loopback")
		}
	}
	env, err := testdb.Start(ctx)
	if err != nil {
		return err
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		return err
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		return err
	}
	password := uuid.NewString()
	for i, q := range []string{`SELECT format('CREATE ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text)`, `SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`, `SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`} {
		var stmt string
		if i == 0 {
			err = conn.QueryRow(ctx, q, password).Scan(&stmt)
		} else {
			err = conn.QueryRow(ctx, q).Scan(&stmt)
		}
		if err == nil {
			_, err = conn.Exec(ctx, stmt)
		}
		if err != nil {
			conn.Close(ctx)
			return err
		}
	}
	conn.Close(ctx)
	dir, err := db.MigrationsDir()
	if err != nil {
		return err
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		return err
	}
	owner, err := db.OpenPools(ctx, db.RoleURLs{Owner: urls.Owner})
	if err != nil {
		return err
	}
	err = jobs.Migrate(ctx, owner.Owner)
	owner.Close()
	if err != nil {
		return err
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		return err
	}
	defer pools.Close()
	u, err := url.Parse(env.AdminURL)
	if err != nil {
		return err
	}
	u.User = url.UserPassword("chatisp_jobs", password)
	pc, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return err
	}
	pc.MaxConns = 4
	wp, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return err
	}
	defer wp.Close()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(key), "v1")
	if err != nil {
		return err
	}
	store := db.NewStore(pools.App, pools.Platform)
	identity, err := auth.New(auth.Options{Store: store, Keys: ring, Logger: logging.New("info", nil)})
	if err != nil {
		return err
	}
	channels, err := configuration.New(store, pools.Ingress, ring)
	if err != nil {
		return err
	}
	info := demoInfo{Email: "agente-" + uuid.NewString() + "@example.test", Password: uuid.NewString(), App: "sim-app-" + uuid.NewString(), Secret: uuid.NewString(), Waba: "sim-waba-" + uuid.NewString(), Phone: "5511999990001"}
	adminEmail, adminPassword := uuid.NewString()+"@example.test", uuid.NewString()
	timezone, limits := "UTC", json.RawMessage(`{}`)
	if m4a {
		// The tenant timezone is the only clock the hours module reads.
		timezone = "America/Sao_Paulo"
		limits = json.RawMessage(`{"attachment_plan_bytes":1048576,"attachment_reserve_bytes":65536,"attachment_file_bytes":1048576}`)
	}
	tenant, agent, admin, member, adminMember := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err = store.WithPlatform(ctx, func(c context.Context) error {
		if _, e := store.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: timezone, Status: "active", Limits: limits}); e != nil {
			return e
		}
		for _, user := range []db.NewUser{{ID: agent, Email: info.Email, Status: "active"}, {ID: admin, Email: adminEmail, Status: "active"}} {
			if _, e := store.CreateUser(c, user); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err = store.WithTenant(ctx, tenant, func(c context.Context) error {
		for _, m := range []db.NewMembership{{ID: member, UserID: agent, Role: "agent", Status: "active"}, {ID: adminMember, UserID: admin, Role: "admin", Status: "active"}} {
			if _, e := store.CreateMembership(c, m); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return err
	}
	hash, err := auth.HashPassword(info.Password)
	if err != nil {
		return err
	}
	adminHash, err := auth.HashPassword(adminPassword)
	if err != nil {
		return err
	}
	if err = store.WithAuth(ctx, func(c context.Context) error {
		if e := store.SetPasswordHash(c, agent, hash); e != nil {
			return e
		}
		return store.SetPasswordHash(c, admin, adminHash)
	}); err != nil {
		return err
	}
	issued, err := identity.Login(ctx, adminEmail, adminPassword, "", "127.0.0.1")
	if err != nil {
		return err
	}
	actor, err := identity.Authenticate(ctx, issued.Token, issued.CSRFToken, true)
	if err != nil {
		return err
	}
	enrollment, err := identity.BeginEnrollment(ctx, actor)
	if err != nil {
		return err
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		return err
	}
	if err = identity.ConfirmEnrollment(ctx, actor, code); err != nil {
		return err
	}
	configured, err := channels.Configure(ctx, auth.HashSecret(issued.Token), configuration.Manual{InboxName: "SIMULAÇÃO Atendimento", DisplayNumber: "+5511999990001", AppID: info.App, AppSecret: info.Secret, WABAID: info.Waba, PhoneNumberID: info.Phone, AccessToken: uuid.NewString()})
	if err != nil {
		return err
	}
	sectorService := sectors.New(store)
	principal := sectors.Principal{TenantID: tenant, UserID: admin, MembershipID: adminMember, Role: "admin"}
	sector, err := sectorService.Create(ctx, principal, "SIMULAÇÃO Atendimento")
	if err != nil {
		return err
	}
	if err = sectorService.Bind(ctx, principal, sector.ID, configured.InboxID, "inbox", true); err != nil {
		return err
	}
	if err = sectorService.Bind(ctx, principal, sector.ID, member, "membership", true); err != nil {
		return err
	}
	if err = sectorService.SetDefault(ctx, principal, sector.ID, configured.InboxID); err != nil {
		return err
	}
	if m3b || m4a {
		info.M3B, err = seedM3B(ctx, store, ring, sectorService, principal, sector.ID, configured.InboxID, member)
		if err != nil {
			return err
		}
	}
	var (
		cannedSvc     cannedreplies.Service
		policySvc     *channelpolicy.Service
		attachmentSvc *attachments.Service
		hoursSvc      businesshours.Service
		fakeHandler   http.Handler = handler()
		stopM4A       func()
	)
	if m4a {
		cannedSvc, policySvc, attachmentSvc, hoursSvc, fakeHandler, info.M4A, stopM4A, err = prepareM4A(ctx, store, ring, tenant, admin, adminMember, configured.InboxID, adminEmail)
		if err != nil {
			return err
		}
		defer stopM4A()
	} else {
		// M3B (and other non-M4A demo modes) still serve GET /api/panel/attendant-signature.
		// A zero Service returns 503 and the composer aborts before send.
		cannedSvc = cannedreplies.New(store)
	}
	inserts, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		return err
	}
	attendance := service.New(store, inserts)
	server := channels.Server()
	factory, err := pipeline.NewDriverFactory(pipeline.DriverFactoryConfig{Simulation: true, StandInURL: "http://" + fakeListener.Addr().String()})
	if err != nil {
		return err
	}
	handlers, err := pipeline.NewWorkers(pipeline.WorkersConfig{DB: store, IngressPool: pools.Ingress, JobsClient: inserts, AttendanceService: attendance, RouteResolver: server, MaterialResolver: server, CredentialReporter: server, DriverFactory: factory})
	if err != nil {
		return err
	}
	worker, err := jobs.New(wp, handlers, jobs.WithTestTiming(10*time.Millisecond, jobs.Backoff{}, 50*time.Millisecond))
	if err != nil {
		return err
	}
	wh, err := pipeline.NewWebhookHandler(pipeline.WebhookConfig{AppResolver: server, VerifyTokens: pipeline.VerifyTokenFunc(func(context.Context, string) (string, error) { return "sim-verify-token", nil }), IngressPool: pools.Ingress, JobsClient: inserts})
	if err != nil {
		return err
	}
	notifier := realtime.NewPGNotifier(ctx, pools.App, logging.New("info", nil))
	defer notifier.Close()
	adapter := realtime.NewAuthAdapter(identity, store)
	origins := []string{"http://localhost:5174", "http://127.0.0.1:5174"}
	if m3b || m4a {
		origins = append(origins, "https://localhost:5174")
	}
	rt := realtime.NewHandler(realtime.Options{TrustedOrigins: origins, Authenticator: adapter, Attendance: attendance, InboxResolver: adapter, Notifier: notifier, DB: store})
	fake := &http.Server{Handler: fakeHandler, ReadHeaderTimeout: 5 * time.Second}
	go fake.Serve(fakeListener)
	defer fake.Shutdown(context.Background())
	if ready != nil {
		ready <- info
	}
	return runtime.Run(ctx, runtime.Options{Mode: config.ModeAll, Config: config.Config{LogLevel: "info", ShutdownTimeout: 15 * time.Second}, HTTPListener: apiListener, MetricsListener: metricsListener, Auth: identity, Attendance: attendance, Channels: channels, Sectors: sectorService, Contacts: contactdirectory.Service{DB: store, Visibility: panelapi.ContactVisibility{}}, Notifications: notifications.New(store), Realtime: rt, Webhook: wh, CannedReplies: cannedSvc, ChannelPolicy: policySvc, Attachments: attachmentSvc, BusinessHours: hoursSvc, Documents: contactdocs.New(store, ring), Presence: presence.New(store), Lifecycle: lifecycle.New(store), Triage: triage.New(store), Store: store, JobsClient: worker, TrustedOrigins: origins, ReadyChecks: []func(context.Context) error{pools.App.Ping, pools.Platform.Ping, pools.Ingress.Ping, wp.Ping}})
}

// panelProxy forwards API upgrades directly to the real server, and assets to Vite.
func panelProxy(api, vite *url.URL) http.Handler {
	a, v := httputil.NewSingleHostReverseProxy(api), httputil.NewSingleHostReverseProxy(vite)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			a.ServeHTTP(w, r)
			return
		}
		v.ServeHTTP(w, r)
	})
}

func demo(args []string) error {
	if len(args) != 0 {
		return errors.New("uso: m2demo demo")
	}
	listeners := make([]net.Listener, 0, 3)
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:9090", "127.0.0.1:18081", "127.0.0.1:5174"} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			for _, x := range listeners {
				x.Close()
			}
			return err
		}
		listeners = append(listeners, l)
	}
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	apiURL, _ := url.Parse("http://127.0.0.1:8080")
	viteURL, _ := url.Parse("http://127.0.0.1:5173")
	proxy := &http.Server{Handler: panelProxy(apiURL, viteURL), ReadHeaderTimeout: 5 * time.Second}
	go proxy.Serve(listeners[3])
	defer proxy.Close()
	ready := make(chan demoInfo, 1)
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, listeners[0], listeners[1], listeners[2], ready) }()
	select {
	case info := <-ready:
		fmt.Printf("SIMULAÇÃO local: login %s senha %s\nPainel: npm --prefix web run dev -- --host 127.0.0.1 --port 5173 --strictPort; abra http://127.0.0.1:5174\nInjetar (outro terminal): go -C backend run ./cmd/m2demo receive -app %s -secret %s -waba %s -phone %s\nCtrl+C encerra e remove o banco descartável.\n", info.Email, info.Password, info.App, info.Secret, info.Waba, info.Phone)
		return <-done
	case err := <-done:
		return err
	}
}
