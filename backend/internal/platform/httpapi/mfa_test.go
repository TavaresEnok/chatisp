package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
)

func TestAdminWithoutMFACanUseProtectedRoutes(t *testing.T) {
	password := "senha-correta-10"
	admin := seedPanelUser(t, "admin", password)
	agent := seedPanelUser(t, "agent", password)
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = 11
	}
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(raw), "v1")
	if err != nil {
		t.Fatal(err)
	}
	box := &mail.Fake{}
	svc, err := auth.New(auth.Options{
		Store:  panelStore,
		Mail:   box,
		Logger: logging.New("info", io.Discard),
		Now:    time.Now,
		Keys:   ring,
	})
	if err != nil {
		t.Fatal(err)
	}
	api := New(Dependencies{Auth: svc, Logger: logging.New("info", io.Discard)})
	origin := "https://painel.chatisp.test"

	login := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.40", map[string]string{
		"email": admin.email, "password": password,
	}, "", "", origin, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var body struct {
		CSRFToken             string `json:"csrf_token"`
		MFAEnrollmentRequired bool   `json:"mfa_enrollment_required"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.MFAEnrollmentRequired {
		t.Fatal("cadastro de MFA deve ser opcional")
	}
	cookie, csrfCookie := panelCookies(t, login)
	if body.CSRFToken != csrfCookie {
		t.Fatal("csrf divergente")
	}
	blocked := panelCall(api.Handler, http.MethodPost, "/api/panel/invitations", "198.51.100.41", map[string]string{
		"email": uuid.NewString() + "@example.com", "role": "agent",
	}, cookie, body.CSRFToken, origin, "")
	if blocked.Code != http.StatusCreated {
		t.Fatalf("rota protegida: %d %s", blocked.Code, blocked.Body.String())
	}

	agentLogin := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.42", map[string]string{
		"email": agent.email, "password": password,
	}, "", "", origin, "")
	if agentLogin.Code != http.StatusOK || strings.Contains(agentLogin.Body.String(), `"mfa_enrollment_required":true`) {
		t.Fatalf("atendente: %d %s", agentLogin.Code, agentLogin.Body.String())
	}

	started := panelCall(api.Handler, http.MethodPost, "/api/panel/mfa/enrollment", "198.51.100.43", map[string]string{}, cookie, body.CSRFToken, origin, "")
	if started.Code != http.StatusOK {
		t.Fatalf("enrolamento: %d %s", started.Code, started.Body.String())
	}
	var enrollment struct {
		Secret        string   `json:"secret"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &enrollment); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	confirmed := panelCall(api.Handler, http.MethodPost, "/api/panel/mfa/enrollment/confirm", "198.51.100.44", map[string]string{
		"code": code,
	}, cookie, body.CSRFToken, origin, "")
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmação: %d %s", confirmed.Code, confirmed.Body.String())
	}
	created := panelCall(api.Handler, http.MethodPost, "/api/panel/invitations", "198.51.100.45", map[string]string{
		"email": uuid.NewString() + "@example.com", "role": "agent",
	}, cookie, body.CSRFToken, origin, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("convite após MFA: %d %s", created.Code, created.Body.String())
	}

	missing := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.46", map[string]string{
		"email": admin.email, "password": password,
	}, "", "", origin, "")
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("sem código: %d %s", missing.Code, missing.Body.String())
	}
	first := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.47", map[string]string{
		"email": admin.email, "password": password, "code": enrollment.RecoveryCodes[0],
	}, "", "", origin, "")
	if first.Code != http.StatusOK {
		t.Fatalf("recuperação: %d %s", first.Code, first.Body.String())
	}
	second := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.48", map[string]string{
		"email": admin.email, "password": password, "code": enrollment.RecoveryCodes[0],
	}, "", "", origin, "")
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reuso: %d %s", second.Code, second.Body.String())
	}
}

func seedPanelUser(t *testing.T, role, password string) panelAccount {
	t.Helper()
	userID := uuid.New()
	item := panelAccount{email: userID.String() + "@example.com"}
	tenantID := uuid.New()
	ctx := context.Background()
	err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := panelStore.CreateTenant(ctx, db.NewTenant{
			ID: tenantID, Timezone: "America/Sao_Paulo", Status: "active",
		}); err != nil {
			return err
		}
		_, err := panelStore.CreateUser(ctx, db.NewUser{ID: userID, Email: item.email, Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = panelStore.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := panelStore.CreateMembership(ctx, db.NewMembership{UserID: userID, Role: role, Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	err = panelStore.WithAuth(ctx, func(ctx context.Context) error {
		return panelStore.SetPasswordHash(ctx, userID, hash)
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}
