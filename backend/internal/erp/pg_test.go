package erp

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestERPPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)

	tenant, other := uuid.New(), uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active'),($2,'UTC','active')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	contact, conversation := seedConversation(t, ctx, admin, tenant)
	if _, err = admin.Exec(ctx, `INSERT INTO erp_connections (tenant_id, id, driver, simulated, enabled) VALUES ($1,$2,'ixc',true,false)`, tenant, uuid.New()); err == nil {
		t.Fatal("driver de fornecedor foi aceito")
	}

	sector := uuid.New()
	connID := uuid.New()
	conn := connection(tenant, connID, sector, AllCapabilities())
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	fake := NewFake(AllCapabilities())
	fake.SetAmbiguous("boleto-pg")
	svc, err := New(conn, fake)
	if err != nil {
		t.Fatal(err)
	}
	action, err := svc.Consult(ctx, Principal{TenantID: tenant, Role: "admin", UserID: uuid.New(), Membership: uuid.New()}, Consult{
		Capability: GetPixCode, InvoiceID: "fake-inv-casa-1", Verification: "verificacao-1",
		LocalKey: "boleto-pg", Conversation: conversation, Now: now,
	})
	if err != nil || action.Outcome != OutcomeUncertain {
		t.Fatalf("ação: %+v %v", action, err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		storedConn := conn
		storedConn.SectorID = uuid.Nil
		if err := InsertConnection(ctx, tx, storedConn); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT extra_conn`); err != nil {
			return err
		}
		extra := connection(tenant, uuid.New(), uuid.Nil, DefaultCapabilities())
		extraErr := InsertConnection(ctx, tx, extra)
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT extra_conn`); err != nil {
			return err
		}
		if !errors.Is(extraErr, ErrConflict) {
			return errors.New("segunda conexão")
		}
		if err := CommitAction(ctx, tx, tenant, action); err != nil {
			return err
		}
		loaded, err := LoadActionByKey(ctx, tx, tenant, "boleto-pg")
		if err != nil {
			return err
		}
		if loaded.ID != action.ID || loaded.Outcome != OutcomeUncertain || loaded.Hash != action.Hash || loaded.View.Reason != ReasonUncertain || loaded.View.Capability != GetPixCode {
			return errors.New("ação instável")
		}
		var kind string
		if err = tx.QueryRow(ctx, `SELECT kind FROM conversation_events WHERE tenant_id=$1 AND id=$2`, tenant, action.ID).Scan(&kind); err != nil {
			return err
		}
		if kind != "erp_action:GetPixCode:uncertain:uncertain" {
			return errors.New("linha do tempo " + kind)
		}
		var result, correlation string
		if err = tx.QueryRow(ctx, `SELECT result, correlation_id FROM audit_logs WHERE tenant_id=$1 AND target_id=$2`, tenant, action.ID).Scan(&result, &correlation); err != nil {
			return err
		}
		if result != OutcomeUncertain || correlation != "boleto-pg" {
			return errors.New("auditoria")
		}
		dup := action
		dup.ID = uuid.New()
		if err = CommitAction(ctx, tx, tenant, dup); err != nil {
			return err
		}
		var actions int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='boleto-pg'`, tenant).Scan(&actions); err != nil {
			return err
		}
		if actions != 1 {
			return errors.New("ação duplicada")
		}
		clash := action
		clash.ID = uuid.New()
		clash.Hash[0] ^= 0xff
		if err = CommitAction(ctx, tx, tenant, clash); !errors.Is(err, ErrConflict) {
			return errors.New("conflito de hash")
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='boleto-pg'`, tenant).Scan(&actions); err != nil {
			return errors.New("transação abortada no conflito")
		}
		if actions != 1 {
			return errors.New("conflito gravou outra ação")
		}
		link := Link{
			ID: uuid.New(), ContactID: contact, ConversationID: conversation,
			ExternalCustomerID: "fake-customer-casa", ExternalContractID: "fake-contract-casa",
			ExternalProtocol: "ERP-99", Selected: true,
		}
		if err = InsertLink(ctx, tx, tenant, link); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SAVEPOINT extra_link`); err != nil {
			return err
		}
		second := link
		second.ID = uuid.New()
		second.ExternalCustomerID = "fake-customer-comercio"
		linkErr := InsertLink(ctx, tx, tenant, second)
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT extra_link`); err != nil {
			return err
		}
		if !errors.Is(linkErr, ErrConflict) {
			return errors.New("dois vínculos selecionados")
		}
		rows, err := ListLinks(ctx, tx, tenant, contact)
		if err != nil {
			return err
		}
		chosen, err := Choose(rows)
		if err != nil || chosen.ExternalProtocol != "ERP-99" {
			return errors.New("vínculo")
		}
		local, err := KeepLocalProtocol("P-LOCAL-1", chosen.ExternalProtocol)
		if err != nil || local != "P-LOCAL-1" {
			return errors.New("protocolo local")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	again := NewFake(AllCapabilities())
	restored, err := New(conn, again)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		loaded, err := LoadActionByKey(ctx, tx, tenant, "boleto-pg")
		if err != nil {
			return err
		}
		restored.Restore(loaded)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repeat, err := restored.Consult(ctx, Principal{TenantID: tenant, Role: "admin", UserID: uuid.New(), Membership: uuid.New()}, Consult{
		Capability: GetPixCode, InvoiceID: "fake-inv-casa-1", Verification: "verificacao-1",
		LocalKey: "boleto-pg", Conversation: conversation, Now: now,
	})
	if err != nil || repeat.Outcome != OutcomeUncertain || again.Calls != 0 {
		t.Fatalf("replay: %+v calls=%d err=%v", repeat, again.Calls, err)
	}
	if err = store.WithTenantTx(ctx, other, func(ctx context.Context, tx pgx.Tx) error {
		_, err := LoadActionByKey(ctx, tx, tenant, "boleto-pg")
		if !errors.Is(err, ErrNotFound) {
			return errors.New("vazou para outro tenant")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func seedConversation(t *testing.T, ctx context.Context, admin *pgx.Conn, tenant uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	connector, inbox, sector := uuid.New(), uuid.New(), uuid.New()
	contact, identity, conversation := uuid.New(), uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status) VALUES($1,$2,'meta_cloud',$3,'disconnected')`, connector, tenant, "origin-"+connector.String())
	exec(`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, inbox, tenant, connector)
	exec(`INSERT INTO sectors(tenant_id,id,name,active) VALUES($1,$2,'Suporte',true)`, tenant, sector)
	exec(`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, tenant, sector, inbox)
	exec(`INSERT INTO inbox_sector_defaults(tenant_id,inbox_id,sector_id) VALUES($1,$2,$3)`, tenant, inbox, sector)
	exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Pessoa')`, contact, tenant)
	exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, "wa-"+identity.String())
	exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'open','menu',$7,'202611',1)`, conversation, tenant, inbox, sector, contact, identity, "P-"+conversation.String()[:8])
	return contact, conversation
}
