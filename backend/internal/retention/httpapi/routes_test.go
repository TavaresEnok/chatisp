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

func TestHTTPExportAndPurgeKeepRows(t *testing.T) {
	s := seedTenant(t)
	ctx := context.Background()
	conv := uuid.New()
	message := uuid.New()
	opened := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'closed','human',$4,'P-92','202611',1,d.sector_id,$5,$6
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$7
			ORDER BY i.id LIMIT 1
		`, conv, s.tenant, s.inbox, s.agent.MembershipID, opened, opened.Add(time.Hour), s.contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind)
			VALUES ($1,$2,$3,1,$4,$5,'queda','resolved','restabelecido',$6,$7,'human')
		`, s.tenant, uuid.New(), conv, opened, opened.Add(time.Hour), s.agent.UserID, s.agent.MembershipID); err != nil {
			return err
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at)
			VALUES ($1,$2,$3,$4,'inbound','customer','public','texto-interno','received',$5,$5)
		`, message, s.tenant, conv, connector, opened)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, s.tenant)
	router := chi.NewRouter()
	Register(router, Dependencies{
		Auth:  stubAuth{p: auth.Principal{UserID: s.admin.UserID, TenantID: s.tenant, MembershipID: s.admin.MembershipID, Role: "admin"}},
		Store: testStore, TrustedOrigins: []string{"https://painel.test"},
	})
	exportReq := httptest.NewRequest(http.MethodGet, "/api/panel/contacts/"+s.contact.String()+"/demands?format=csv", nil)
	exportReq.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	exportRec := httptest.NewRecorder()
	router.ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusOK || !strings.Contains(exportRec.Body.String(), retention.DemandLabel) || strings.Contains(exportRec.Body.String(), "texto-interno") || !strings.Contains(exportRec.Body.String(), "P-92") {
		t.Fatalf("exportação http %d %s", exportRec.Code, exportRec.Body.String())
	}
	purgeReq := httptest.NewRequest(http.MethodPost, "/api/panel/retention/purge", nil)
	purgeReq.Header.Set("Origin", "https://painel.test")
	purgeReq.Header.Set("X-CSRF-Token", "csrf")
	purgeReq.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	purgeRec := httptest.NewRecorder()
	router.ServeHTTP(purgeRec, purgeReq)
	if purgeRec.Code != http.StatusConflict || !strings.Contains(purgeRec.Body.String(), `"deleted":false`) || !strings.Contains(purgeRec.Body.String(), `"refused":true`) {
		t.Fatalf("purge http %d %s", purgeRec.Code, purgeRec.Body.String())
	}
	if after := countRows(t, s.tenant); before != after {
		t.Fatalf("http apagou: antes %+v depois %+v", before, after)
	}

	agent := httptest.NewRequest(http.MethodGet, "/api/panel/retention", nil)
	agent.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	agentRec := httptest.NewRecorder()
	agentRouter := chi.NewRouter()
	Register(agentRouter, Dependencies{
		Auth:  stubAuth{p: auth.Principal{UserID: s.outsider.UserID, TenantID: s.tenant, MembershipID: s.outsider.MembershipID, Role: "agent"}},
		Store: testStore,
	})
	agentRouter.ServeHTTP(agentRec, agent)
	if agentRec.Code != http.StatusForbidden {
		t.Fatalf("atendente na política: %d %s", agentRec.Code, agentRec.Body.String())
	}
}

type rowCount struct{ messages, attachments, conversations, cycles int }

func countRows(t *testing.T, tenant uuid.UUID) rowCount {
	t.Helper()
	var out rowCount
	err := testStore.WithTenantTx(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM messages WHERE tenant_id=$1),
				(SELECT count(*) FROM attachments WHERE tenant_id=$1),
				(SELECT count(*) FROM conversations WHERE tenant_id=$1),
				(SELECT count(*) FROM conversation_cycles WHERE tenant_id=$1)
		`, tenant).Scan(&out.messages, &out.attachments, &out.conversations, &out.cycles)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type stubAuth struct{ p auth.Principal }

func (s stubAuth) Authenticate(context.Context, string, string, bool) (auth.Principal, error) {
	return s.p, nil
}

func TestHTTPMetaUsageIsAdminOrSupervisorOnlyAndHasNoMoney(t *testing.T) {
	s := seedTenant(t)
	for _, tc := range []struct {
		role string
		want int
	}{{"admin", http.StatusOK}, {"agent", http.StatusForbidden}} {
		router := chi.NewRouter()
		Register(router, Dependencies{
			Auth:  stubAuth{p: auth.Principal{UserID: s.admin.UserID, TenantID: s.tenant, MembershipID: s.admin.MembershipID, Role: tc.role}},
			Store: testStore, TrustedOrigins: []string{"https://painel.test"},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/panel/meta-usage?from=2026-11-01&to=2026-11-05", nil)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s: http %d %s", tc.role, rec.Code, rec.Body.String())
		}
		if tc.want == http.StatusOK {
			body := rec.Body.String()
			if !strings.Contains(body, `"rows":[]`) || !strings.Contains(body, "Não há valor em dinheiro") || strings.Contains(body, "price") || strings.Contains(body, "amount") {
				t.Fatalf("corpo: %s", body)
			}
		}
	}
	router := chi.NewRouter()
	Register(router, Dependencies{Auth: stubAuth{p: auth.Principal{UserID: s.admin.UserID, TenantID: s.tenant, MembershipID: s.admin.MembershipID, Role: "admin"}}, Store: testStore})
	bad := httptest.NewRequest(http.MethodGet, "/api/panel/meta-usage?from=2026-11-05&to=2026-11-01", nil)
	bad.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("período inválido: %d", rec.Code)
	}
}
