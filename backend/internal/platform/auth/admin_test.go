package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
