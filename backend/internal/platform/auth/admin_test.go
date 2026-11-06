package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/mail"
)

func TestSuspendAndReactivatePreservesDataAndAudits(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAdminService(t)
	password := "senha-correta-10"
	super := seedAccount(t, "admin", password)
	markPlatformSuperadmin(t, super.user)
	actor := loginPrincipal(t, svc, super.email, password)

	limit := 4
	created, err := svc.CreateProvider(ctx, actor, "America/Sao_Paulo", PlanLimits{MaxAgents: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "active" || created.ID == uuid.Nil {
		t.Fatalf("provedor criado: %+v", created)
	}

	member := account{tenant: created.ID, user: uuid.New(), membership: uuid.New()}
	member.email = member.user.String() + "@example.com"
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: member.user, Email: member.email, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, created.ID, func(ctx context.Context) error {
		if _, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID: member.membership, UserID: member.user, Role: "admin", Status: "active",
		}); err != nil {
			return err
		}
		if _, err := testStore.CreateAuditLog(ctx, db.NewAuditLog{
			MembershipID: &member.membership,
			ActorUserID:  &member.user,
			Action:       "member.add",
			TargetType:   "membership",
			TargetID:     &member.membership,
			Result:       "success",
		}); err != nil {
			return err
		}
		return testStore.InsertConversation(ctx, uuid.New(), "resumo-de-atendimento-preservado")
	}); err != nil {
		t.Fatal(err)
	}
	setPassword(t, member.user, password)
	providerSession := loginIssued(t, svc, member.email, password)
	if countLiveSessions(t, created.ID, member.user) != 1 {
		t.Fatal("sessão operacional não abriu")
	}

	suspended, err := svc.SuspendProvider(ctx, actor, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if suspended.Status != "suspended" {
		t.Fatalf("status: %s", suspended.Status)
	}
	if _, err := svc.Authenticate(ctx, providerSession.Token, providerSession.CSRFToken, false); !errors.Is(err, ErrUnauthenticated) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("acesso operacional após suspensão: %v", err)
	}
	if countLiveSessions(t, created.ID, member.user) != 0 {
		t.Fatal("suspensão não derrubou a sessão")
	}
	if _, err := svc.ListMemberships(ctx, providerSession.Principal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operação com principal antigo: %v", err)
	}
	assertProviderData(t, created.ID, member.membership, "resumo-de-atendimento-preservado")
	assertProviderPlatformAudit(t, actor.UserID, created.ID, "tenant.create")
	assertProviderPlatformAudit(t, actor.UserID, created.ID, "tenant.suspend")

	if _, err := svc.SuspendProvider(ctx, actor, created.ID); !errors.Is(err, ErrTenantState) {
		t.Fatalf("segunda suspensão: %v", err)
	}
	// Simulate a session left live by an older interrupted suspension.
	restoreLiveSessionForTest(t, providerSession.Principal.SessionID)
	active, err := svc.ReactivateProvider(ctx, actor, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != "active" {
		t.Fatalf("reativação: %s", active.Status)
	}
	assertProviderData(t, created.ID, member.membership, "resumo-de-atendimento-preservado")
	assertProviderPlatformAudit(t, actor.UserID, created.ID, "tenant.reactivate")
	if _, err := svc.Authenticate(ctx, providerSession.Token, providerSession.CSRFToken, false); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("sessão antiga após reativação: %v", err)
	}
	again := loginIssued(t, svc, member.email, password)
	if again.Principal.TenantID != created.ID {
		t.Fatalf("novo login: %s", again.Principal.TenantID)
	}

	other := seedAccount(t, "agent", password)
	if err := testStore.WithTenant(ctx, other.tenant, func(ctx context.Context) error {
		rows, err := testStore.ListConversationSummaries(ctx)
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			t.Fatalf("outro provedor leu atendimento: %v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledUserLosesSessions(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAdminService(t)
	password := "senha-correta-10"
	adminAccount := seedAccount(t, "admin", password)
	agentID := uuid.New()
	agentEmail := agentID.String() + "@example.com"
	agentMembership := uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: agentID, Email: agentEmail, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var createdAt time.Time
	if err := testStore.WithTenant(ctx, adminAccount.tenant, func(ctx context.Context) error {
		row, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID: agentMembership, UserID: agentID, Role: "agent", Status: "active",
		})
		createdAt = row.CreatedAt
		return err
	}); err != nil {
		t.Fatal(err)
	}
	setPassword(t, agentID, password)
	admin := loginPrincipal(t, svc, adminAccount.email, password)
	agent := loginIssued(t, svc, agentEmail, password)
	if countLiveSessions(t, adminAccount.tenant, agentID) != 1 {
		t.Fatal("sessão do atendente não abriu")
	}
	if countLiveSessions(t, adminAccount.tenant, adminAccount.user) != 1 {
		t.Fatal("sessão do administrador não abriu")
	}

	if err := svc.SetMembershipStatus(ctx, admin, agentMembership, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, agent.Token, agent.CSRFToken, false); !errors.Is(err, ErrUnauthenticated) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("sessão do usuário desativado: %v", err)
	}
	if countLiveSessions(t, adminAccount.tenant, agentID) != 0 {
		t.Fatal("desativação não derrubou as sessões do usuário")
	}
	if countLiveSessions(t, adminAccount.tenant, adminAccount.user) != 1 {
		t.Fatal("desativação derrubou a sessão de outro usuário")
	}
	if err := testStore.WithTenant(ctx, adminAccount.tenant, func(ctx context.Context) error {
		row, err := testStore.FindMembership(ctx, agentMembership)
		if err != nil {
			return err
		}
		if row.Status != "disabled" || row.UserID != agentID || !row.CreatedAt.Equal(createdAt) {
			t.Fatalf("histórico do vínculo: %+v criado %s", row, createdAt)
		}
		logs, err := testStore.ListAuditLogs(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, log := range logs {
			if log.Action == "membership.deactivate" && log.TargetID != nil && *log.TargetID == agentMembership {
				found = true
			}
		}
		if !found {
			t.Fatal("desativação sem auditoria do provedor")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate a session left live by an older interrupted disable.
	restoreLiveSessionForTest(t, agent.Principal.SessionID)
	if err := svc.SetMembershipStatus(ctx, admin, agentMembership, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, agent.Token, agent.CSRFToken, false); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("sessão antiga após reativação: %v", err)
	}
	restored := loginIssued(t, svc, agentEmail, password)
	if restored.Principal.MembershipID != agentMembership || restored.Principal.Role != "agent" {
		t.Fatalf("reativação: %+v", restored.Principal)
	}
}

func restoreLiveSessionForTest(t *testing.T, id uuid.UUID) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `UPDATE sessions SET revoked_at=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCreationWaitsForMembershipTransition(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAdminService(t)
	a := seedAccount(t, "agent", "senha-correta-10")
	identity, found, err := svc.loadIdentity(ctx, a.email)
	if err != nil || !found {
		t.Fatalf("identidade: %v", err)
	}
	result := make(chan error, 1)
	err = testStore.WithTenant(ctx, a.tenant, func(ctx context.Context) error {
		if err := testStore.LockTenant(ctx); err != nil {
			return err
		}
		go func() {
			_, err := svc.issueSession(context.Background(), a.user, a.email, db.LoginMembership{TenantID: a.tenant, MembershipID: a.membership, Role: "agent"}, "", "full", identity.PasswordHash, db.MFARecord{})
			result <- err
		}()
		select {
		case err := <-result:
			t.Fatalf("sessão criada durante transição: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		_, err := testStore.UpdateMembershipStatus(ctx, a.membership, "disabled")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sessão criada após desativação: %v", err)
	}
	if countLiveSessions(t, a.tenant, a.user) != 0 {
		t.Fatal("sessão permaneceu após desativação")
	}
}

func TestVerifiedPasswordCannotIssueSessionAfterReset(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAdminService(t)
	a := seedAccount(t, "agent", "senha-correta-10")
	identity, found, err := svc.loadIdentity(ctx, a.email)
	if err != nil || !found {
		t.Fatalf("identidade: %v", err)
	}
	if ok, err := VerifyPassword("senha-correta-10", identity.PasswordHash); err != nil || !ok {
		t.Fatal("senha inicial não conferiu")
	}
	// Model a reset completing after password verification but before session insertion.
	setPassword(t, a.user, "senha-nova-segura")
	if _, err := svc.issueAndAudit(ctx, identity, identity.Memberships[0], db.MFARecord{}, a.email, "full", false, false); err == nil {
		t.Fatal("senha já substituída abriu sessão")
	}
}

func TestTenantAuthUsesOnePoolConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(testURLs.App)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := db.NewStore(pool, testPools.Platform)
	ready, release, done := make(chan struct{}, 2), make(chan struct{}), make(chan error, 2)
	for i := 0; i < 2; i++ {
		tenant := uuid.New()
		go func() {
			done <- store.WithTenant(ctx, tenant, func(ctx context.Context) error {
				if err := store.LockTenant(ctx); err != nil {
					return err
				}
				ready <- struct{}{}
				<-release
				return store.WithAuthInTenant(ctx, func(ctx context.Context) error {
					_, err := store.VerifiedPasswordCurrent(ctx, uuid.New(), "not-a-credential", db.MFARecord{})
					return err
				})
			})
		}()
	}
	<-ready
	<-ready
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestProviderTransitionDoesNotWaitForSaturatedAppPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := seedAccount(t, "agent", "senha-correta-10")
	config, err := pgxpool.ParseConfig(testURLs.App)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := db.NewStore(pool, testPools.Platform)
	ready, done := make(chan struct{}, 2), make(chan error, 2)
	err = store.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := store.SetTenantStatus(ctx, a.tenant, "suspended"); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			go func() {
				done <- store.WithTenant(context.Background(), a.tenant, func(c context.Context) error {
					ready <- struct{}{}
					return store.LockTenant(c)
				})
			}()
		}
		<-ready
		<-ready
		return store.RevokePlatformTenantSessions(ctx, a.tenant, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPasswordResetWaitsForSessionAndRevokesIt(t *testing.T) {
	ctx := context.Background()
	svc, box := newAdminService(t)
	a := seedAccount(t, "agent", "senha-correta-10")
	identity, found, err := svc.loadIdentity(ctx, a.email)
	if err != nil || !found {
		t.Fatalf("identidade: %v", err)
	}
	if err := svc.RequestPasswordReset(ctx, a.email, "203.0.113.44"); err != nil {
		t.Fatal(err)
	}
	token := lastLine(box.Snapshot()[0].Text)
	resetDone := make(chan error, 1)
	sessionID := uuid.New()
	_, tokenHash, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	_, csrfHash, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithAuth(ctx, func(ctx context.Context) error {
		ok, err := testStore.VerifiedPasswordCurrent(ctx, a.user, identity.PasswordHash, db.MFARecord{})
		if err != nil || !ok {
			t.Fatalf("hash válido: %v %v", ok, err)
		}
		go func() {
			resetDone <- svc.ConfirmPasswordReset(context.Background(), token, "senha-nova-segura", "203.0.113.45")
		}()
		select {
		case err := <-resetDone:
			t.Fatalf("reset não aguardou o login: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		now := svc.now()
		return testStore.InsertSession(ctx, db.NewSession{ID: sessionID, TokenHash: tokenHash, CSRFHash: csrfHash, UserID: a.user, TenantID: a.tenant, MembershipID: a.membership, Now: now, IdleExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-resetDone; err != nil {
		t.Fatal(err)
	}
	if countLiveSessions(t, a.tenant, a.user) != 0 {
		t.Fatal("reset deixou sessão concorrente ativa")
	}
}

func TestAgentLimitBlocksExtraInvites(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAdminService(t)
	password := "senha-correta-10"
	adminAccount := seedAccount(t, "admin", password)
	setTenantLimits(t, adminAccount.tenant, `{"max_agents":1}`)
	admin := loginPrincipal(t, svc, adminAccount.email, password)

	if _, err := svc.Invite(ctx, admin, uuid.NewString()+"@example.com", "supervisor"); err != nil {
		t.Fatalf("supervisor não consome vaga de atendente: %v", err)
	}
	first := uuid.NewString() + "@example.com"
	if _, err := svc.Invite(ctx, admin, first, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Invite(ctx, admin, uuid.NewString()+"@example.com", "agent"); !errors.Is(err, ErrPlanLimit) {
		t.Fatalf("convite excedente: %v", err)
	}

	rows, err := svc.ListMemberships(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var agentMembership uuid.UUID
	for _, row := range rows {
		if row.Role == "agent" && row.Status == "invited" {
			agentMembership = row.ID
		}
	}
	if agentMembership == uuid.Nil {
		t.Fatal("convite de atendente não ficou registrado")
	}
	if err := svc.SetMembershipStatus(ctx, admin, agentMembership, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Invite(ctx, admin, uuid.NewString()+"@example.com", "agent"); err != nil {
		t.Fatalf("vaga liberada: %v", err)
	}
	if err := svc.SetMembershipStatus(ctx, admin, agentMembership, "active"); !errors.Is(err, ErrPlanLimit) {
		t.Fatalf("reativação excedente: %v", err)
	}
}

func TestSuperadminCannotReadTenantAttendance(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAdminService(t)
	password := "senha-correta-10"
	super := seedAccount(t, "supervisor", password)
	markPlatformSuperadmin(t, super.user)
	actor := loginPrincipal(t, svc, super.email, password)
	if !actor.IsSuperadmin {
		t.Fatal("sessão não reconheceu o superadmin")
	}
	marker := "conversa-secreta-" + uuid.NewString()
	if err := testStore.WithTenant(ctx, super.tenant, func(ctx context.Context) error {
		return testStore.InsertConversation(ctx, uuid.New(), marker)
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	if jsonContains(raw, marker) {
		t.Fatal("principal do superadmin carregou atendimento")
	}
	listed, err := svc.ListProviders(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if jsonContains(body, marker) {
		t.Fatal("lista de provedores devolveu atendimento")
	}
	one, err := svc.GetProvider(ctx, actor, super.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if one.Timezone == "" || one.Status == "" {
		t.Fatalf("cadastro: %+v", one)
	}
	view, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	if jsonContains(view, marker) {
		t.Fatal("consulta de provedor devolveu atendimento")
	}
	if _, err := testPools.Platform.Exec(ctx, `SELECT summary FROM conversations`); !isPermission(err) {
		t.Fatalf("plataforma leu atendimento: %v", err)
	}
	if _, err := svc.ListProviders(ctx, Principal{UserID: super.user, Role: "admin", TenantID: super.tenant}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("papel de administrador leu a plataforma: %v", err)
	}
}

func newAdminService(t *testing.T) (*Service, *mail.Fake) {
	t.Helper()
	box := &mail.Fake{}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc, err := New(Options{
		Store:  testStore,
		Mail:   box,
		Policy: testPolicy(),
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, box
}

func loginPrincipal(t *testing.T, svc *Service, email, password string) Principal {
	t.Helper()
	issued := loginIssued(t, svc, email, password)
	principal, err := svc.Authenticate(context.Background(), issued.Token, issued.CSRFToken, true)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func loginIssued(t *testing.T, svc *Service, email, password string) IssuedSession {
	t.Helper()
	issued, err := svc.Login(context.Background(), email, password, "", "203.0.113.50")
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func markPlatformSuperadmin(t *testing.T, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `UPDATE users SET is_superadmin = true WHERE id = $1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("superadmin: %d", tag.RowsAffected())
	}
}

func setTenantLimits(t *testing.T, tenantID uuid.UUID, limits string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `UPDATE tenants SET limits = $2::jsonb WHERE id = $1`, tenantID, limits)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("limites: %d", tag.RowsAffected())
	}
}

func countLiveSessions(t *testing.T, tenantID, userID uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM sessions
		WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, tenantID, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertProviderData(t *testing.T, tenantID, membershipID uuid.UUID, summary string) {
	t.Helper()
	ctx := context.Background()
	err := testStore.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		row, err := testStore.FindMembership(ctx, membershipID)
		if err != nil {
			return err
		}
		if row.Status != "active" || row.TenantID != tenantID {
			t.Fatalf("vínculo após suspensão: %+v", row)
		}
		logs, err := testStore.ListAuditLogs(ctx)
		if err != nil {
			return err
		}
		if len(logs) == 0 {
			t.Fatal("auditoria do provedor foi apagada")
		}
		rows, err := testStore.ListConversationSummaries(ctx)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0] != summary {
			t.Fatalf("atendimento: %v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertProviderPlatformAudit(t *testing.T, actor, tenantID uuid.UUID, action string) {
	t.Helper()
	target := tenantID.String()
	err := testStore.WithPlatform(context.Background(), func(ctx context.Context) error {
		rows, err := testStore.ListPlatformAuditLogs(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Actor == actor.String() && row.Action == action && row.TargetType == "tenant" && row.TargetID != nil && *row.TargetID == target && row.Result == "success" {
				return nil
			}
		}
		t.Fatalf("auditoria de plataforma ausente: %s %s", action, target)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func jsonContains(raw []byte, part string) bool {
	return len(part) > 0 && containsText(string(raw), part)
}

func containsText(msg, part string) bool {
	return len(msg) >= len(part) && (msg == part || indexText(msg, part) >= 0)
}

func indexText(msg, part string) int {
	for i := 0; i+len(part) <= len(msg); i++ {
		if msg[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
