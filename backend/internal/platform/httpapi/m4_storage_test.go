package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"chatisp/internal/attachments"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestUnconfiguredStorageRefusesAuthenticatedUpload(t *testing.T) {
	ctx := context.Background()
	const origin = "https://painel.chatisp.test"
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{"attachment_plan_bytes":1048576,"attachment_reserve_bytes":4096,"attachment_file_bytes":1048576}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	inbox, connector, sector, conv, contact, identity := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3)`, []any{tenant, connector, connector.String()}},
			{`INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$3,'Caixa')`, []any{tenant, inbox, connector}},
			{`INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'A')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES ($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES ($1,$2,$3)`, []any{tenant, sector, agent.membership}},
			{`INSERT INTO contacts (tenant_id,id,name) VALUES ($1,$2,'A')`, []any{tenant, contact}},
			{`INSERT INTO contact_channel_identities (tenant_id,id,contact_id,connector_id,external_id) VALUES ($1,$2,$3,$4,'a')`, []any{tenant, identity, contact, connector}},
			{`INSERT INTO conversations (tenant_id,id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES ($1,$2,$3,$4,$5,$6,'P','202601',1)`, []any{tenant, conv, inbox, sector, contact, identity}},
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement.q, statement.args...); err != nil {
				return err
			}
		}
		var plan, reserve, file int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((limits->>'attachment_plan_bytes')::bigint, 0),
			       COALESCE((limits->>'attachment_reserve_bytes')::bigint, 0),
			       COALESCE((limits->>'attachment_file_bytes')::bigint, 0)
			  FROM tenants
			 WHERE id = $1
			   AND id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`, tenant).Scan(&plan, &reserve, &file); err != nil {
			return err
		}
		if plan <= 0 || reserve < 0 || file <= 0 {
			t.Fatalf("limites do tenant: %d %d %d", plan, reserve, file)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cookie, csrf, _ := panelLogin(t, nil, agent.email, "senha-correta-10")
	limits := func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (attachments.Limits, error) {
		var plan, reserve, file int64
		err := tx.QueryRow(ctx, `
			SELECT COALESCE((limits->>'attachment_plan_bytes')::bigint, 0),
			       COALESCE((limits->>'attachment_reserve_bytes')::bigint, 0),
			       COALESCE((limits->>'attachment_file_bytes')::bigint, 0)
			  FROM tenants
			 WHERE id = $1
			   AND id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`, tenantID).Scan(&plan, &reserve, &file)
		if err != nil || plan <= 0 || file <= 0 || reserve < 0 {
			return attachments.Limits{}, attachments.ErrStorage
		}
		return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: reserve, FileBytes: file}, nil
	}
	api := New(Dependencies{
		Auth:           panelService(t, &mail.Fake{}),
		Attachments:    &attachments.Service{DB: panelStore, Storage: attachments.UnavailableStorage(), Limits: limits},
		TrustedOrigins: []string{origin},
		Logger:         logging.New("error", io.Discard),
	})
	path := "/api/panel/inboxes/" + inbox.String() + "/conversations/" + conv.String() + "/attachments"
	response := panelCall(api.Handler, http.MethodPost, path, "198.51.100.60", map[string]any{
		"request_id": uuid.New(), "name": "nota.txt", "bytes": 4,
	}, cookie, csrf, origin, "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("registro sem storage: %d %s", response.Code, response.Body.String())
	}
	get := panelCall(api.Handler, http.MethodGet, path+"/"+uuid.NewString(), "198.51.100.60", nil, cookie, csrf, origin, "")
	if get.Code != http.StatusServiceUnavailable {
		t.Fatalf("leitura sem storage: %d %s", get.Code, get.Body.String())
	}
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var attachmentsCount, budgets int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1`, tenant).Scan(&attachmentsCount); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachment_budgets WHERE tenant_id=$1`, tenant).Scan(&budgets); err != nil {
			return err
		}
		if attachmentsCount != 0 || budgets != 0 {
			t.Fatalf("storage ausente reservou capacidade: anexos=%d orçamentos=%d", attachmentsCount, budgets)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
