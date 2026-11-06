package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
)

func TestAdminMustEnrollBeforePanelAndRecoveryIsSingleUse(t *testing.T) {
	admin := seedAccount(t, "admin", "senha-correta-10")
	agent := seedAccount(t, "agent", "senha-correta-10")
	ring := singleRing(t, "v1")
	svc := newMFAService(t, ring, time.Now)

	issued, err := svc.Login(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.80")
	if err != nil {
		t.Fatal(err)
	}
	if !issued.EnrollmentRequired {
		t.Fatal("administrador entrou no painel sem MFA")
	}
	actor, err := svc.Authenticate(context.Background(), issued.Token, issued.CSRFToken, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListSessions(context.Background(), actor); !errors.Is(err, ErrEnrollmentRequired) {
		t.Fatalf("rota protegida: %v", err)
	}
	if _, err := svc.Invite(context.Background(), actor, "pessoa@example.com", "agent"); !errors.Is(err, ErrEnrollmentRequired) {
		t.Fatalf("convite: %v", err)
	}

	agentSession, err := svc.Login(context.Background(), agent.email, "senha-correta-10", "", "203.0.113.81")
	if err != nil {
		t.Fatal(err)
	}
	if agentSession.EnrollmentRequired {
		t.Fatal("atendente foi obrigado a enrolar MFA")
	}

	enrollment, err := svc.BeginEnrollment(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	assertCiphertextStored(t, admin.user, enrollment.Secret, "v1", false)
	if err := svc.ConfirmEnrollment(context.Background(), actor, "000000"); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("código ruim: %v", err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmEnrollment(context.Background(), actor, code); err != nil {
		t.Fatal(err)
	}
	assertCiphertextStored(t, admin.user, enrollment.Secret, "v1", true)
	actor, err = svc.Authenticate(context.Background(), issued.Token, issued.CSRFToken, true)
	if err != nil {
		t.Fatal(err)
	}
	if actor.EnrollmentOnly {
		t.Fatal("sessão continuou presa ao enrolamento")
	}
	if _, err := svc.ListSessions(context.Background(), actor); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Login(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.82"); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("login sem código: %v", err)
	}
	recovery := enrollment.RecoveryCodes[0]
	withRecovery, err := svc.LoginCode(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.83", recovery)
	if err != nil {
		t.Fatal(err)
	}
	if withRecovery.EnrollmentRequired {
		t.Fatal("código de recuperação não abriu o painel")
	}
	if _, err := svc.LoginCode(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.84", recovery); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("reuso do código: %v", err)
	}
	fresh, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LoginCode(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.85", fresh); err != nil {
		t.Fatal(err)
	}

	other := seedAccount(t, "admin", "senha-correta-10")
	markSuperadmin(t, other.user)
	otherSvc := newMFAService(t, ring, time.Now)
	otherLogin, err := otherSvc.Login(context.Background(), other.email, "senha-correta-10", "", "203.0.113.86")
	if err != nil || !otherLogin.EnrollmentRequired {
		t.Fatalf("superadmin: %v", err)
	}
	otherActor, err := otherSvc.Authenticate(context.Background(), otherLogin.Token, otherLogin.CSRFToken, true)
	if err != nil {
		t.Fatal(err)
	}
	otherEnroll, err := otherSvc.BeginEnrollment(context.Background(), otherActor)
	if err != nil {
		t.Fatal(err)
	}
	otherCode, err := totp.GenerateCode(otherEnroll.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := otherSvc.ConfirmEnrollment(context.Background(), otherActor, otherCode); err != nil {
		t.Fatal(err)
	}
	if _, err := otherSvc.LoginCode(context.Background(), other.email, "senha-correta-10", "", "203.0.113.87", otherEnroll.RecoveryCodes[0]); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenant(context.Background(), admin.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{UserID: other.user, Role: "agent", Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	priorIdentity, found, err := otherSvc.loadIdentity(context.Background(), other.email)
	if err != nil || !found {
		t.Fatalf("identidade MFA: %v", err)
	}
	priorMFA, err := otherSvc.mfaRecord(context.Background(), other.user)
	if err != nil {
		t.Fatal(err)
	}
	verifiedCode, err := totp.GenerateCode(otherEnroll.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherSvc.verifyFactor(context.Background(), other.user, priorIdentity.Memberships[0], priorMFA, verifiedCode, "203.0.113.99"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetMFA(context.Background(), actor, other.user); err != nil {
		t.Fatal(err)
	}
	if _, err := otherSvc.issueAndAudit(context.Background(), priorIdentity, priorIdentity.Memberships[0], priorMFA, other.email, "full", false, false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("fator redefinido abriu sessão: %v", err)
	}
	resetLogin, err := otherSvc.Login(context.Background(), other.email, "senha-correta-10", other.tenant.String(), "203.0.113.88")
	if err != nil || !resetLogin.EnrollmentRequired {
		t.Fatalf("depois do reset: %v enrolamento=%v", err, resetLogin.EnrollmentRequired)
	}

	assertAuditActions(t, admin.tenant, "mfa.enroll", "mfa.recovery", "mfa.reset")
	assertPlatformAudit(t, "mfa.enroll", "mfa.recovery", "mfa.reset")

	// A valid second factor cannot revive a password verified before a reset.
	identity, found, err := svc.loadIdentity(context.Background(), admin.email)
	if err != nil || !found {
		t.Fatalf("identidade: %v", err)
	}
	setPassword(t, admin.user, "senha-nova-segura")
	fresh, err = totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.finishLogin(context.Background(), identity, identity.Memberships[0], admin.email, "203.0.113.89", fresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("MFA após troca de senha: %v", err)
	}
}

func TestMFAAttemptLimit(t *testing.T) {
	admin := seedAccount(t, "admin", "senha-correta-10")
	clk := newClock()
	svc := newMFAService(t, singleRing(t, "v1"), clk.Now)
	issued, err := svc.Login(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.90")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.Authenticate(context.Background(), issued.Token, "", false)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := svc.BeginEnrollment(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := svc.ConfirmEnrollment(context.Background(), actor, "000000"); !errors.Is(err, ErrInvalidMFA) {
			t.Fatalf("tentativa: %v", err)
		}
	}
	code, err := totp.GenerateCode(enrollment.Secret, clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmEnrollment(context.Background(), actor, code); !errors.Is(err, ErrLocked) {
		t.Fatalf("limite: %v", err)
	}
	clk.Advance(time.Minute)
	code, err = totp.GenerateCode(enrollment.Secret, clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmEnrollment(context.Background(), actor, code); err != nil {
		t.Fatal(err)
	}
}

func TestKeyRotationKeepsReadingOldTOTP(t *testing.T) {
	admin := seedAccount(t, "admin", "senha-correta-10")
	v1 := bytes.Repeat([]byte{3}, 32)
	v2 := bytes.Repeat([]byte{9}, 32)
	first, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(v1), "v1")
	if err != nil {
		t.Fatal(err)
	}
	svc := newMFAService(t, first, time.Now)
	issued, err := svc.Login(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.91")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.Authenticate(context.Background(), issued.Token, "", false)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := svc.BeginEnrollment(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmEnrollment(context.Background(), actor, code); err != nil {
		t.Fatal(err)
	}
	rotated, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(v1)+",v2:"+base64.StdEncoding.EncodeToString(v2), "v2")
	if err != nil {
		t.Fatal(err)
	}
	next := newMFAService(t, rotated, time.Now)
	fresh, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := next.LoginCode(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.92", fresh); err != nil {
		t.Fatal(err)
	}
}

func newMFAService(t *testing.T, ring *crypto.Ring, now func() time.Time) *Service {
	t.Helper()
	svc, err := New(Options{Store: testStore, Policy: testPolicy(), Now: now, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func singleRing(t *testing.T, version string) *crypto.Ring {
	t.Helper()
	raw := bytes.Repeat([]byte{7}, 32)
	ring, err := crypto.Parse(version+":"+base64.StdEncoding.EncodeToString(raw), version)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func assertCiphertextStored(t *testing.T, userID uuid.UUID, secret, version string, confirmed bool) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var cipher []byte
	var gotVersion string
	var confirmedAt *time.Time
	if err := conn.QueryRow(ctx, `
		SELECT totp_ciphertext, totp_key_version, totp_confirmed_at
		FROM users WHERE id = $1
	`, userID).Scan(&cipher, &gotVersion, &confirmedAt); err != nil {
		t.Fatal(err)
	}
	if gotVersion != version || bytes.Contains(cipher, []byte(secret)) {
		t.Fatalf("ciphertext expôs o segredo ou a versão %s", gotVersion)
	}
	if confirmed != (confirmedAt != nil) {
		t.Fatalf("confirmação: %v", confirmedAt)
	}
}

func markSuperadmin(t *testing.T, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE users SET is_superadmin = true WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
}

func assertAuditActions(t *testing.T, tenantID uuid.UUID, actions ...string) {
	t.Helper()
	var logs []db.AuditLog
	err := testStore.WithTenant(context.Background(), tenantID, func(ctx context.Context) error {
		var err error
		logs, err = testStore.ListAuditLogs(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range logs {
		if row.Result == "success" {
			seen[row.Action] = true
		}
	}
	for _, action := range actions {
		if !seen[action] {
			t.Fatalf("auditoria sem %s: %#v", action, logs)
		}
	}
}

func assertPlatformAudit(t *testing.T, actions ...string) {
	t.Helper()
	var logs []db.PlatformAuditLog
	err := testStore.WithPlatform(context.Background(), func(ctx context.Context) error {
		var err error
		logs, err = testStore.ListPlatformAuditLogs(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range logs {
		if row.Result == "success" {
			seen[row.Action] = true
		}
	}
	for _, action := range actions {
		if !seen[action] {
			t.Fatalf("auditoria de plataforma sem %s: %#v", action, logs)
		}
	}
}
