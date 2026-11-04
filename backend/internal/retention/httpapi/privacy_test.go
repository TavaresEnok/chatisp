package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/retention"
)

func TestHTTPPrivacyExportAndDeletion(t *testing.T) {
	s := seedTenant(t)
	ctx := context.Background()
	opened := time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)
	conv := uuid.New()
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE contacts SET phone=$3, notes='nota-http' WHERE tenant_id=$1 AND id=$2
		`, s.tenant, s.contact, "+5511977776666"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'closed','human',$7,'P-HTTP','202611',1,d.sector_id,$4,$5
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$6
			ORDER BY i.id LIMIT 1
		`, conv, s.tenant, s.inbox, opened, opened.Add(time.Hour), s.contact, s.admin.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind)
			VALUES ($1,$2,$3,1,$4,$5,'sinal','resolved','normalizado',$6,$7,'human')
		`, s.tenant, uuid.New(), conv, opened, opened.Add(time.Hour), s.admin.UserID, s.admin.MembershipID); err != nil {
			return err
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,ingested_at)
			VALUES ($1,$2,$3,$4,'inbound','customer','public','texto-http','received',$5)
		`, uuid.New(), s.tenant, conv, connector, opened)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	Register(router, Dependencies{
		Auth:  stubAuth{p: auth.Principal{UserID: s.admin.UserID, TenantID: s.tenant, MembershipID: s.admin.MembershipID, Role: "admin"}},
		Store: testStore, TrustedOrigins: []string{"https://painel.test"},
	})
	exportReq := httptest.NewRequest(http.MethodGet, "/api/panel/contacts/"+s.contact.String()+"/privacy-export", nil)
	exportReq.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	exportRec := httptest.NewRecorder()
	router.ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusOK || !strings.Contains(exportRec.Body.String(), retention.PrivacyExportLabel) || !strings.Contains(exportRec.Body.String(), "texto-http") || !strings.Contains(exportRec.Body.String(), "P-HTTP") {
		t.Fatalf("exportação %d %s", exportRec.Code, exportRec.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/panel/contacts/"+s.contact.String()+"/privacy-deletion", strings.NewReader(`{"confirm":"apagar"}`))
	bad.Header.Set("Origin", "https://painel.test")
	bad.Header.Set("X-CSRF-Token", "csrf")
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("confirmação %d %s", badRec.Code, badRec.Body.String())
	}
	ok := httptest.NewRequest(http.MethodPost, "/api/panel/contacts/"+s.contact.String()+"/privacy-deletion", strings.NewReader(`{"confirm":"excluir titular"}`))
	ok.Header.Set("Origin", "https://painel.test")
	ok.Header.Set("X-CSRF-Token", "csrf")
	ok.Header.Set("Content-Type", "application/json")
	ok.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, ok)
	if okRec.Code != http.StatusOK || !strings.Contains(okRec.Body.String(), `"erased":true`) || !strings.Contains(okRec.Body.String(), `"automatic_purge":false`) || !strings.Contains(okRec.Body.String(), "P-HTTP") {
		t.Fatalf("exclusão %d %s", okRec.Code, okRec.Body.String())
	}
	var body string
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND conversation_id=$2`, s.tenant, conv).Scan(&body)
	}); err != nil || body != "" {
		t.Fatalf("corpo %q %v", body, err)
	}
	agentRouter := chi.NewRouter()
	Register(agentRouter, Dependencies{
		Auth:  stubAuth{p: auth.Principal{UserID: s.agent.UserID, TenantID: s.tenant, MembershipID: s.agent.MembershipID, Role: "agent"}},
		Store: testStore, TrustedOrigins: []string{"https://painel.test"},
	})
	agentReq := httptest.NewRequest(http.MethodGet, "/api/panel/contacts/"+s.contact.String()+"/privacy-export", nil)
	agentReq.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	agentRec := httptest.NewRecorder()
	agentRouter.ServeHTTP(agentRec, agentReq)
	if agentRec.Code != http.StatusForbidden {
		t.Fatalf("atendente %d %s", agentRec.Code, agentRec.Body.String())
	}
}
