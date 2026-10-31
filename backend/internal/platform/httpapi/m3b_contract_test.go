package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"chatisp/internal/attendance/service"
	"chatisp/internal/collision"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/mail"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestM3BRoutesRegisteredWithoutDependencies(t *testing.T) {
	const origin = "https://painel.chatisp.test"
	api := New(Dependencies{TrustedOrigins: []string{origin}})
	item := "/api/panel/inboxes/11111111-1111-1111-1111-111111111111/conversations/22222222-2222-2222-2222-222222222222"
	for _, path := range []string{item + "/assignment-candidates", item + "/annotations", item + "/collision", "/api/panel/queue"} {
		w := httptest.NewRecorder()
		api.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d, want 503: %s", path, w.Code, w.Body.String())
		}
	}
	deleteTag := api.Huma.OpenAPI().Paths["/api/panel/annotation-tags/{tag_id}"].Delete
	if deleteTag.RequestBody != nil && deleteTag.RequestBody.Required {
		t.Fatal("deleting a tag must not require a request body")
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/panel/annotation-tags/11111111-1111-1111-1111-111111111111", nil)
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	api.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete without body: got %d, want 503: %s", w.Code, w.Body.String())
	}
}

func TestM3BRegisteredAssignmentAndSectorRevocation(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	cookie, csrf, _ := panelLogin(t, nil, agent.email, "senha-correta-10")
	sectorA, sectorB, connector, inbox, contact, identity, conversation, otherIdentity, otherConversation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3)`, []any{tenant, connector, connector.String()}},
			{`INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$3,'A')`, []any{tenant, inbox, connector}},
			{`INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'A'),($1,$3,'B')`, []any{tenant, sectorA, sectorB}},
			{`INSERT INTO sector_inboxes VALUES ($1,$2,$4),($1,$3,$4)`, []any{tenant, sectorA, sectorB, inbox}},
			{`INSERT INTO sector_memberships VALUES ($1,$2,$3)`, []any{tenant, sectorA, agent.membership}},
			{`INSERT INTO contacts (tenant_id,id,name) VALUES ($1,$2,'A')`, []any{tenant, contact}},
			{`INSERT INTO contact_channel_identities (tenant_id,id,contact_id,connector_id,external_id) VALUES ($1,$2,$3,$4,'a'),($1,$5,$3,$4,'b')`, []any{tenant, identity, contact, connector, otherIdentity}},
			{`INSERT INTO conversations (tenant_id,id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES ($1,$2,$3,$4,$5,$6,'A','202601',1)`, []any{tenant, conversation, inbox, sectorA, contact, identity}},
			{`INSERT INTO conversations (tenant_id,id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number,assignee_id,controller) VALUES ($1,$2,$3,$4,$5,$6,'B','202601',1,$7,'human')`, []any{tenant, otherConversation, inbox, sectorA, contact, otherIdentity, agent.membership}},
			{`INSERT INTO assignment_capacities (tenant_id,membership_id,capacity) VALUES ($1,$2,1)`, []any{tenant, agent.membership}},
		}
		for _, s := range statements {
			if _, err := tx.Exec(ctx, s.q, s.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://painel.chatisp.test"
	api := New(Dependencies{Auth: panelService(t, &mail.Fake{}), Attendance: service.New(panelStore, nil), Collision: &collision.Service{DB: panelStore}, TrustedOrigins: []string{origin}})
	item := fmt.Sprintf("/api/panel/inboxes/%s/conversations/%s", inbox, conversation)
	call := func(method, path string, body any, want int) {
		t.Helper()
		w := panelCall(api.Handler, method, path, "198.51.100.40", body, cookie, csrf, origin, "")
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
	}
	call("GET", item+"/assignment-candidates", nil, 200)
	call("GET", item+"/annotations", nil, 200)
	call("GET", item+"/collision", nil, 200)
	call("GET", "/api/panel/queue", nil, 200)
	queue := panelCall(api.Handler, "GET", "/api/panel/queue?tab=unassigned", "198.51.100.40", nil, cookie, csrf, origin, "")
	if queue.Code != 200 {
		t.Fatalf("unassigned queue: %d: %s", queue.Code, queue.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(queue.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("unassigned queue: %v, %s", err, queue.Body.String())
	}
	if assignee, present := page.Items[0]["assignee_id"]; !present || assignee != nil {
		t.Fatalf("assignee_id: present=%v value=%v", present, assignee)
	}
	schema := api.Huma.OpenAPI().Components.Schemas.SchemaFromRef("#/components/schemas/RealtimeStoreQueueItem")
	if schema == nil || !schema.Properties["assignee_id"].Nullable {
		t.Fatal("queue assignee_id schema must permit null")
	}
	call("POST", item+"/assume", map[string]any{"expected_version": 1}, 409)
	call("POST", item+"/transfer-member", map[string]any{"expected_version": 1, "client_request_id": "capacity", "target_id": agent.membership}, 409)
	call("POST", item+"/transfer-sector", map[string]any{"expected_version": 2, "client_request_id": "stale", "target_id": sectorB}, 409)
	call("POST", item+"/transfer-sector", map[string]any{"expected_version": 1, "client_request_id": "sector", "target_id": sectorB}, 200)
	call("GET", item+"/history", nil, 403)
	call("GET", item+"/annotations", nil, 403)
}
