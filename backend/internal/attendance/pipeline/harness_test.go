package pipeline

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
)

var (
	testEnv           *testdb.Env
	testPools         *db.Pools
	testStore         *db.Store
	testJobs          *jobs.Client
	testJobsPool      *pgxpool.Pool
	testRing          *crypto.Ring
	testConfigSvc     *configuration.Service
	testConfigServer  *configuration.Server
	testAttendanceSvc service.Service
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}

	secrets, err := db.NewRoleSecrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papéis: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha no bootstrap: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	dir, err := db.MigrationsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	jobsPassword, err := randomPassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao gerar senha jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	if err := ensureJobsRole(ctx, env.AdminURL, jobsPassword); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao criar papel jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	jobsURL, err := withUser(env.AdminURL, "chatisp_jobs", jobsPassword)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao gerar jobs URL: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	jobsPool, err := pgxpool.New(ctx, jobsURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir jobs pool: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	pools, err := db.OpenPools(ctx, db.RoleURLs{
		Owner:    urls.Owner,
		App:      urls.App,
		Platform: urls.Platform,
		Ingress:  urls.Ingress,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	if err := jobs.Migrate(ctx, pools.Owner); err != nil {
		fmt.Fprintf(os.Stderr, "falha na migração River: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}

	jobsClient, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao instanciar jobs client: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(key), "v1")
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao instanciar crypto ring: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}

	storeInstance := db.NewStore(pools.App, pools.Platform)
	cfgSvc, err := configuration.New(storeInstance, pools.Ingress, ring)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao instanciar configuration service: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}

	attendanceSvc := service.New(storeInstance, jobsClient)

	testEnv = env
	testPools = pools
	testStore = storeInstance
	testJobs = jobsClient
	testRing = ring
	testConfigSvc = cfgSvc
	testConfigServer = cfgSvc.Server()
	testAttendanceSvc = attendanceSvc
	testJobsPool = jobsPool

	code := m.Run()

	jobsPool.Close()
	pools.Close()
	env.Close()
	os.Exit(code)
}

func withUser(raw, user, password string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String(), nil
}

func ensureJobsRole(ctx context.Context, adminURL, password string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'chatisp_jobs')`).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := conn.QueryRow(ctx, `
		SELECT format('%s ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text, $2::text)
	`, verb, password).Scan(&stmt); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return err
	}
	for _, query := range []string{
		`SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`,
		`SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`,
	} {
		var grant string
		if err := conn.QueryRow(ctx, query).Scan(&grant); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, grant); err != nil {
			return err
		}
	}
	return nil
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type seededTenant struct {
	tenantID    uuid.UUID
	userID      uuid.UUID
	memberID    uuid.UUID
	appRowID    uuid.UUID
	wabaRowID   uuid.UUID
	phoneRowID  uuid.UUID
	connectorID uuid.UUID
	inboxID     uuid.UUID
	appID       string
	wabaID      string
	phoneID     string
	appSecret   string
	token       string
}

func seedTenantWithMeta(t *testing.T, appID, wabaID, phoneID, token, appSecret string) seededTenant {
	t.Helper()
	ctx := context.Background()

	st := seededTenant{
		tenantID:    uuid.New(),
		userID:      uuid.New(),
		memberID:    uuid.New(),
		appRowID:    uuid.New(),
		wabaRowID:   uuid.New(),
		phoneRowID:  uuid.New(),
		connectorID: uuid.New(),
		inboxID:     uuid.New(),
		appID:       appID,
		wabaID:      wabaID,
		phoneID:     phoneID,
		appSecret:   appSecret,
		token:       token,
	}

	// Create tenant and user in platform scope
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       st.tenantID,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
			Limits:   json.RawMessage(`{}`),
		}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{
			ID:     st.userID,
			Email:  fmt.Sprintf("user-%s@example.com", st.userID),
			Status: "active",
		})
		return err
	}); err != nil {
		t.Fatalf("falha ao criar tenant/user: %v", err)
	}

	// Create membership in tenant scope
	if err := testStore.WithTenant(ctx, st.tenantID, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID:     st.memberID,
			UserID: st.userID,
			Role:   "admin",
			Status: "active",
		})
		return err
	}); err != nil {
		t.Fatalf("falha ao criar membership: %v", err)
	}

	// Configure through the real service so every fixture uses the canonical registry.
	hash := make([]byte, 32)
	csrf := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(csrf); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		sealed, err := testRing.Encrypt([]byte("totp-teste-local-123456"), crypto.Context{TenantID: st.tenantID.String(), Entity: "user", EntityID: st.userID.String(), Field: "totp_secret"})
		if err != nil {
			return err
		}
		if err = testStore.BeginMFA(ctx, st.userID, st.tenantID, sealed.Ciphertext, sealed.Version); err != nil {
			return err
		}
		if ok, err := testStore.ConfirmMFA(ctx, st.userID, now); err != nil || !ok {
			return fmt.Errorf("confirmar MFA: %v", err)
		}
		return testStore.InsertSession(ctx, db.NewSession{ID: uuid.New(), TokenHash: hash, UserID: st.userID, TenantID: st.tenantID, MembershipID: st.memberID, CSRFHash: csrf, Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	}); err != nil {
		t.Fatalf("falha ao criar sessão: %v", err)
	}
	if len(token) < 8 {
		token += "-fixture-token"
		st.token = token
	}
	if len(appSecret) < 8 {
		appSecret += "-fixture-secret"
		st.appSecret = appSecret
	}
	box, err := testConfigSvc.Configure(ctx, hash, configuration.Manual{InboxName: "Caixa Meta", DisplayNumber: "+" + phoneID, AppID: appID, WABAID: wabaID, PhoneNumberID: phoneID, AppSecret: appSecret, AccessToken: token})
	if err != nil {
		t.Fatalf("falha ao configurar meta: %v", err)
	}
	st.connectorID, st.inboxID = box.ConnectorID, box.InboxID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `WITH s AS (INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Padrão',true)), i AS (INSERT INTO sector_inboxes VALUES ($1,$2,$3)), m AS (INSERT INTO sector_memberships VALUES ($1,$2,$4)) INSERT INTO inbox_sector_defaults VALUES ($1,$3,$2)`, st.tenantID, uuid.New(), st.inboxID, st.memberID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT p.id, w.id, a.id FROM meta_phone_numbers p JOIN meta_wabas w ON w.tenant_id=p.tenant_id AND w.id=p.meta_waba_id JOIN meta_apps a ON a.tenant_id=w.tenant_id AND a.id=w.meta_app_id WHERE p.tenant_id=$1 AND p.phone_number_id=$2`, st.tenantID, phoneID).Scan(&st.phoneRowID, &st.wabaRowID, &st.appRowID)
	}); err != nil {
		t.Fatalf("falha ao consultar vínculo meta: %v", err)
	}

	return st
}

func seedSecondBoxForTenant(t *testing.T, st seededTenant, phoneID string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	connectorID := uuid.New()
	inboxID := uuid.New()
	phoneRowID := uuid.New()

	tokenCtx := crypto.Context{
		TenantID: st.tenantID.String(),
		Entity:   "channel_connector",
		EntityID: connectorID.String(),
		Field:    "access_token",
	}
	sealedToken, err := testRing.Encrypt([]byte(st.token), tokenCtx)
	if err != nil {
		t.Fatalf("falha ao cifrar token segunda caixa: %v", err)
	}
	tokenAAD := strings.Join([]string{tokenCtx.TenantID, tokenCtx.Entity, tokenCtx.EntityID, tokenCtx.Field}, "\x1f")

	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled, version)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true, '1')
		`, connectorID, st.tenantID, phoneID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name)
			VALUES ($1, $2, $3, 'Segunda Caixa Meta')
		`, inboxID, st.tenantID, connectorID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `WITH i AS (INSERT INTO sector_inboxes SELECT tenant_id,id,$2 FROM sectors WHERE tenant_id=$1 AND legacy_default RETURNING tenant_id,sector_id,inbox_id) INSERT INTO inbox_sector_defaults SELECT tenant_id,inbox_id,sector_id FROM i`, st.tenantID, inboxID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO meta_phone_numbers (id, tenant_id, meta_waba_id, connector_id, inbox_id, phone_number_id, display_number, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'active')
		`, phoneRowID, st.tenantID, st.wabaRowID, connectorID, inboxID, phoneID, "+"+phoneID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO credential_secrets (id, tenant_id, entity, entity_id, field, key_version, ciphertext, aad)
			VALUES ($1, $2, 'channel_connector', $3, 'access_token', $4, $5, $6)
		`, uuid.New(), st.tenantID, connectorID, sealedToken.Version, sealedToken.Ciphertext, tokenAAD); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("falha ao registrar segunda caixa: %v", err)
	}

	return connectorID, inboxID
}

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
