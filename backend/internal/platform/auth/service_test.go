package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
)

func TestProgressiveLockoutByAccountAndIP(t *testing.T) {
	clk := newClock()
	var logs bytes.Buffer
	svc := newTestService(t, clk, &mail.Fake{}, &logs)
	password := "senha-correta-10"
	wrong := "senha-errada-10"
	one := seedAccount(t, "admin", password)
	other := seedAccount(t, "agent", password)
	ip := "203.0.113.10"
	otherIP := "203.0.113.11"

	for range 3 {
		_, err := svc.Login(context.Background(), one.email, wrong, "", ip)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("falha antes do bloqueio: %v", err)
		}
	}
	_, err := svc.Login(context.Background(), one.email, password, "", ip)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("conta deveria bloquear: %v", err)
	}
	_, err = svc.Login(context.Background(), other.email, password, "", ip)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("IP deveria bloquear outra conta: %v", err)
	}
	_, err = svc.Login(context.Background(), one.email, password, "", otherIP)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("bloqueio da conta deveria seguir o usuário: %v", err)
	}

	clk.Advance(time.Minute)
	_, err = svc.Login(context.Background(), one.email, wrong, "", otherIP)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("depois do primeiro prazo: %v", err)
	}
	clk.Advance(time.Minute)
	_, err = svc.Login(context.Background(), one.email, password, "", otherIP)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("bloqueio progressivo: %v", err)
	}
	clk.Advance(2 * time.Minute)
	issued, err := svc.Login(context.Background(), one.email, password, "", otherIP)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || strings.Contains(logs.String(), password) || strings.Contains(logs.String(), wrong) || strings.Contains(logs.String(), issued.Token) {
		t.Fatal("log ou sessão expôs segredo")
	}
}

func TestSessionIdleAbsoluteAndRevocation(t *testing.T) {
	clk := newClock()
	svc := newTestService(t, clk, &mail.Fake{}, nil)
	password := "senha-correta-10"
	admin := seedAccount(t, "admin", password)
	issued, err := svc.Login(context.Background(), admin.email, password, "", "203.0.113.20")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(9 * time.Minute)
	if _, err := svc.Authenticate(context.Background(), issued.Token, "", false); err != nil {
		t.Fatal(err)
	}
	clk.Advance(11 * time.Minute)
	if _, err := svc.Authenticate(context.Background(), issued.Token, "", false); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("ociosidade: %v", err)
	}

	fresh, err := svc.Login(context.Background(), admin.email, password, "", "203.0.113.21")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(31 * time.Minute)
	if _, err := svc.Authenticate(context.Background(), fresh.Token, "", false); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expiração absoluta: %v", err)
	}

	again, err := svc.Login(context.Background(), admin.email, password, "", "203.0.113.22")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.Authenticate(context.Background(), again.Token, again.CSRFToken, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(context.Background(), again.Token, "csrf-errado", true); !errors.Is(err, ErrCSRF) {
		t.Fatalf("csrf: %v", err)
	}
	if err := svc.RevokeSession(context.Background(), actor, again.Principal.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(context.Background(), again.Token, again.CSRFToken, true); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("sessão revogada: %v", err)
	}
	if err := testStore.WithTenant(context.Background(), actor.TenantID, func(ctx context.Context) error {
		logs, err := testStore.ListAuditLogs(ctx)
		if err != nil {
			return err
		}
		for _, row := range logs {
			if row.Action == "session.revoke" && row.Result == "success" && row.TargetID != nil && *row.TargetID == again.Principal.SessionID {
				return nil
			}
		}
		t.Fatal("revogação de sessão sem auditoria")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInviteAndResetTokensAreSingleUse(t *testing.T) {
	clk := newClock()
	box := &mail.Fake{}
	svc := newTestService(t, clk, box, nil)
	admin := seedAccount(t, "admin", "senha-correta-10")
	actor := Principal{UserID: admin.user, TenantID: admin.tenant, MembershipID: admin.membership, Role: "admin"}
	email := uuid.NewString() + "@example.com"
	if _, err := svc.Invite(context.Background(), actor, email, "agent"); err != nil {
		t.Fatal(err)
	}
	token := lastLine(box.Snapshot()[0].Text)
	if strings.Contains(box.Snapshot()[0].Text, "senha-correta-10") {
		t.Fatal("convite levou senha")
	}
	issued, err := svc.AcceptInvite(context.Background(), token, "senha-inicial-10", "203.0.113.30")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptInvite(context.Background(), token, "senha-inicial-10", "203.0.113.31"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reuso do convite: %v", err)
	}
	if _, err := svc.Authenticate(context.Background(), issued.Token, "", false); err != nil {
		t.Fatal(err)
	}

	box.Messages = nil
	if _, err := svc.Invite(context.Background(), actor, uuid.NewString()+"@example.com", "agent"); err != nil {
		t.Fatal(err)
	}
	expired := lastLine(box.Snapshot()[0].Text)
	clk.Advance(2 * time.Minute)
	if _, err := svc.AcceptInvite(context.Background(), expired, "senha-inicial-10", "203.0.113.32"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("convite expirado: %v", err)
	}
	clk.Advance(-2 * time.Minute)
	if _, err := svc.AcceptInvite(context.Background(), expired, "senha-inicial-10", "203.0.113.33"); err != nil {
		t.Fatalf("expiração não deveria consumir o token: %v", err)
	}

	adminSession, err := svc.Login(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.34")
	if err != nil {
		t.Fatal(err)
	}
	box.Messages = nil
	if err := svc.RequestPasswordReset(context.Background(), admin.email, "203.0.113.44"); err != nil {
		t.Fatal(err)
	}
	reset := lastLine(box.Snapshot()[0].Text)
	if err := svc.ConfirmPasswordReset(context.Background(), reset, "senha-nova-segura", "203.0.113.35"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmPasswordReset(context.Background(), reset, "senha-nova-segura", "203.0.113.36"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reuso do reset: %v", err)
	}
	if _, err := svc.Authenticate(context.Background(), adminSession.Token, "", false); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("reset deveria revogar a sessão antiga: %v", err)
	}
	if _, err := svc.Authenticate(context.Background(), issued.Token, "", false); err != nil {
		t.Fatalf("reset de outra conta revogou o convidado: %v", err)
	}
	if _, err := svc.Login(context.Background(), admin.email, "senha-nova-segura", "", "203.0.113.37"); err != nil {
		t.Fatal(err)
	}

	box.Messages = nil
	if err := svc.RequestPasswordReset(context.Background(), admin.email, "203.0.113.38"); err != nil {
		t.Fatal(err)
	}
	stale := lastLine(box.Snapshot()[0].Text)
	clk.Advance(2 * time.Minute)
	if err := svc.ConfirmPasswordReset(context.Background(), stale, "senha-ainda-mais-nova", "203.0.113.39"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reset expirado: %v", err)
	}
	clk.Advance(-2 * time.Minute)
	if err := svc.ConfirmPasswordReset(context.Background(), stale, "senha-ainda-mais-nova", "203.0.113.40"); err != nil {
		t.Fatalf("expiração do reset consumiu o token: %v", err)
	}

	// Critical events of this flow are in the provider audit log, with no secret in it.
	if err := testStore.WithTenant(context.Background(), admin.tenant, func(ctx context.Context) error {
		logs, err := testStore.ListAuditLogs(ctx)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, row := range logs {
			seen[row.Action+"/"+row.Result] = true
			if strings.Contains(row.Action+row.TargetType, "senha") {
				t.Fatalf("auditoria com texto de senha: %+v", row)
			}
		}
		for _, want := range []string{"invitation.create/success", "invitation.accept/success", "login/success", "password_reset.request/success", "password_reset.confirm/success"} {
			if !seen[want] {
				t.Fatalf("auditoria sem %s; vistos: %v", want, seen)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFailedMailInvalidatesInvite(t *testing.T) {
	clk := newClock()
	failed := &captureFail{}
	svc := newTestService(t, clk, failed, nil)
	admin := seedAccount(t, "admin", "senha-correta-10")
	actor := Principal{UserID: admin.user, TenantID: admin.tenant, MembershipID: admin.membership, Role: "admin"}
	_, err := svc.Invite(context.Background(), actor, uuid.NewString()+"@example.com", "supervisor")
	if !errors.Is(err, ErrMail) {
		t.Fatalf("e-mail: %v", err)
	}
	token := lastLine(failed.msg.Text)
	if _, err := svc.AcceptInvite(context.Background(), token, "senha-inicial-10", "203.0.113.41"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("token após falha de envio: %v", err)
	}
}

func TestLoginRequiresTenantWhenThereAreSeveral(t *testing.T) {
	clk := newClock()
	svc := newTestService(t, clk, &mail.Fake{}, nil)
	password := "senha-correta-10"
	admin := seedAccount(t, "admin", password)
	second := addMembership(t, admin.user, "agent")
	_, err := svc.Login(context.Background(), admin.email, password, "", "203.0.113.50")
	if !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("vários provedores: %v", err)
	}
	issued, err := svc.Login(context.Background(), admin.email, password, second.tenant.String(), "203.0.113.51")
	if err != nil {
		t.Fatal(err)
	}
	if issued.Principal.TenantID != second.tenant || issued.Principal.Role != "agent" {
		t.Fatalf("provedor: %+v", issued.Principal)
	}
}

func TestStoredSessionIsOnlyTheHash(t *testing.T) {
	clk := newClock()
	svc := newTestService(t, clk, &mail.Fake{}, nil)
	admin := seedAccount(t, "admin", "senha-correta-10")
	issued, err := svc.Login(context.Background(), admin.email, "senha-correta-10", "", "203.0.113.60")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(context.Background(), testURLs.Owner)
	if err != nil {
		t.Fatal("falha ao conectar como dono")
	}
	defer conn.Close(context.Background())
	var hash []byte
	if err := conn.QueryRow(context.Background(), `SELECT token_hash FROM sessions WHERE id = $1`, issued.Principal.SessionID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 || string(hash) == issued.Token || !SecretEqual(hash, HashSecret(issued.Token)) {
		t.Fatal("o banco não guardou só o hash da sessão")
	}
}

type captureFail struct {
	msg mail.Message
}

func (c *captureFail) Send(_ context.Context, msg mail.Message) error {
	c.msg = msg
	return errors.New("envio de e-mail indisponível")
}

func newTestService(t *testing.T, clk *clock, sender mail.Sender, logs *bytes.Buffer) *Service {
	t.Helper()
	var target bytes.Buffer
	if logs == nil {
		logs = &target
	}
	svc, err := New(Options{
		Store:  testStore,
		Mail:   sender,
		Logger: logging.New("info", logs),
		Policy: testPolicy(),
		Now:    clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func lastLine(text string) string {
	var last string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			last = line
		}
	}
	return last
}
