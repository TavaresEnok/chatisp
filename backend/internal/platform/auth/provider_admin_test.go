package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A new provider gets its first administrator through a platform invite, without SQL.
func TestSuperadminInvitesFirstProviderAdmin(t *testing.T) {
	ctx := context.Background()
	svc, box := newAdminService(t)
	password := "senha-correta-10"
	super := seedAccount(t, "admin", password)
	markPlatformSuperadmin(t, super.user)
	actor := loginPrincipal(t, svc, super.email, password)

	created, err := svc.CreateProvider(ctx, actor, "America/Sao_Paulo", PlanLimits{})
	if err != nil {
		t.Fatal(err)
	}
	email := "Primeiro.Admin." + uuid.NewString() + "@Example.com"
	box.Messages = nil
	invite, err := svc.InviteProviderAdmin(ctx, actor, created.ID, email)
	if err != nil {
		t.Fatal(err)
	}
	if invite.ExpiresAt.IsZero() {
		t.Fatal("convite sem prazo")
	}
	sent := box.Snapshot()
	if len(sent) != 1 || sent[0].To != strings.ToLower(email) {
		t.Fatalf("e-mail do convite: %+v", sent)
	}
	assertProviderPlatformAudit(t, super.user, created.ID, actionTenantAdminInvite)

	issued, err := svc.AcceptInvite(ctx, lastLine(sent[0].Text), "senha-do-admin-10", "203.0.113.70")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := svc.Authenticate(ctx, issued.Token, issued.CSRFToken, false)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != created.ID || principal.Role != "admin" || principal.IsSuperadmin {
		t.Fatalf("admin do provedor novo: %+v", principal)
	}

	// The invited admin operates only their provider; it cannot use the platform invite.
	if _, err := svc.InviteProviderAdmin(ctx, Principal{UserID: principal.UserID, Role: "admin", TenantID: created.ID, MembershipID: principal.MembershipID}, created.ID, "outro@example.com"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin de provedor usou o convite de plataforma: %v", err)
	}
}

func TestProviderAdminInviteRefusals(t *testing.T) {
	ctx := context.Background()
	svc, box := newAdminService(t)
	password := "senha-correta-10"
	super := seedAccount(t, "admin", password)
	markPlatformSuperadmin(t, super.user)
	actor := loginPrincipal(t, svc, super.email, password)

	if _, err := svc.InviteProviderAdmin(ctx, actor, uuid.New(), "x@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provedor inexistente: %v", err)
	}
	created, err := svc.CreateProvider(ctx, actor, "America/Sao_Paulo", PlanLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.InviteProviderAdmin(ctx, actor, created.ID, "sem-arroba"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("e-mail inválido: %v", err)
	}
	if _, err := svc.SuspendProvider(ctx, actor, created.ID); err != nil {
		t.Fatal(err)
	}
	box.Messages = nil
	if _, err := svc.InviteProviderAdmin(ctx, actor, created.ID, "x@example.com"); !errors.Is(err, ErrTenantState) {
		t.Fatalf("provedor suspenso: %v", err)
	}
	if len(box.Snapshot()) != 0 {
		t.Fatal("convite enviado para provedor suspenso")
	}
	plain := seedAccount(t, "admin", password)
	other := loginPrincipal(t, svc, plain.email, password)
	if _, err := svc.InviteProviderAdmin(ctx, other, created.ID, "x@example.com"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin comum convidou em outro provedor: %v", err)
	}
}
