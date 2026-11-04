package retention

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestExportDemandsKeepsProtocolAndHidesOtherSectors(t *testing.T) {
	s := seedTenant(t)
	svc := Service{DB: testStore}
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	closed := opened.Add(2 * time.Hour)
	ctx := context.Background()
	closedConv := uuid.New()
	openConv := uuid.New()
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,external_protocol,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'closed','human',$4,'P-77','202611',1,'ERP-1',d.sector_id,$5,$6
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id AND i.external_id LIKE 'wa-%'
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$7
			ORDER BY i.id LIMIT 1
		`, closedConv, s.tenant, s.inbox, s.agent.MembershipID, opened, closed, s.contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind,external_reference)
			VALUES ($1,$2,$3,1,$4,$5,'lentidão','referred_external','cliente encaminhado',$6,$7,'human','OS-9')
		`, s.tenant, uuid.New(), closedConv, opened, closed, s.agent.UserID, s.agent.MembershipID); err != nil {
			return err
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at)
			VALUES ($1,$2,$3,$4,'inbound','customer','public','segredo-da-mensagem','received',$5,$5)
		`, uuid.New(), s.tenant, closedConv, connector, opened); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'open','menu','P-78','202611',1,d.sector_id,$4,$4
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$5 AND i.id <> (SELECT contact_identity_id FROM conversations WHERE tenant_id=$2 AND id=$6)
			LIMIT 1
		`, openConv, s.tenant, s.inbox, opened.Add(time.Minute), s.contact, closedConv); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at) VALUES ($1,$2,$3,1,$4)`, s.tenant, uuid.New(), openConv, opened.Add(time.Minute)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO triage_events (tenant_id,id,conversation_id,input_id,kind,detail)
			VALUES ($1,$2,$3,$4,'routed','{"actions":[{"kind":"record_reason","reason":"financeiro"}]}')
		`, s.tenant, uuid.New(), openConv, uuid.New())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := svc.ExportDemands(ctx, s.admin, s.contact)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Label != DemandLabel || !strings.Contains(doc.Label, "Não é o histórico completo do provedor nem do ERP") {
		t.Fatalf("rótulo: %s", doc.Label)
	}
	if len(doc.Demands) != 2 {
		t.Fatalf("demandas: %+v", doc.Demands)
	}
	first := doc.Demands[0]
	if first.Protocol != "P-77" || first.Reason != "lentidão" || first.Synthesis != "cliente encaminhado" || first.Author != "pessoa:"+s.agent.MembershipID.String() {
		t.Fatalf("demanda fechada: %+v", first)
	}
	if !strings.Contains(first.Forwarding, "encaminhado") || !strings.Contains(first.Forwarding, "OS-9") || !strings.Contains(first.Forwarding, "ERP-1") || !strings.Contains(first.Forwarding, "Não é o histórico do ERP.") {
		t.Fatalf("encaminhamento: %s", first.Forwarding)
	}
	if doc.Demands[1].Protocol != "P-78" || doc.Demands[1].Reason != "financeiro" || doc.Demands[1].Author != "" {
		t.Fatalf("demanda aberta: %+v", doc.Demands[1])
	}
	csv := FormatCSV(doc)
	if strings.Contains(csv, "segredo-da-mensagem") || !strings.Contains(csv, DemandLabel) || !strings.Contains(csv, "P-77") {
		t.Fatalf("csv vazou ou perdeu campos: %s", csv)
	}
	seen, err := svc.ExportDemands(ctx, s.agent, s.contact)
	if err != nil || len(seen.Demands) != 2 {
		t.Fatalf("atendente do setor: %v %+v", err, seen.Demands)
	}
	if _, err := svc.ExportDemands(ctx, s.outsider, s.contact); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outro setor: %v", err)
	}
}

func TestPurgeRefusesAndDeletesNothing(t *testing.T) {
	s := seedTenant(t)
	svc := Service{DB: testStore}
	ctx := context.Background()
	conv := uuid.New()
	message := uuid.New()
	attachment := uuid.New()
	opened := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'open','menu','P-90','202611',1,d.sector_id,$4,$4
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$5
			ORDER BY i.id LIMIT 1
		`, conv, s.tenant, s.inbox, opened, s.contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at) VALUES ($1,$2,$3,1,$4)`, s.tenant, uuid.New(), conv, opened); err != nil {
			return err
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at)
			VALUES ($1,$2,$3,$4,'inbound','customer','public','corpo-que-fica','received',$5,$5)
		`, message, s.tenant, conv, connector, opened); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO attachments (tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,expires_at)
			VALUES ($1,$2,$3,$4,'outgoing','comprovante.pdf',80,$5,$6)
		`, s.tenant, attachment, conv, uuid.New(), "obj-"+attachment.String(), opened.Add(24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := counts(t, s.tenant)
	result, err := svc.RunPurge(ctx, s.admin)
	if !errors.Is(err, ErrPurgeDisabled) || !result.Refused || result.Deleted || result.Tasks != len(Classes) {
		t.Fatalf("expurgo: %v %+v", err, result)
	}
	after := counts(t, s.tenant)
	if before != after {
		t.Fatalf("censo mudou: antes %+v depois %+v", before, after)
	}
	var body string
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND id=$2`, s.tenant, message).Scan(&body)
	}); err != nil {
		t.Fatal(err)
	}
	if body != "corpo-que-fica" {
		t.Fatalf("corpo: %s", body)
	}
	var refused, deletions int
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM retention_tasks WHERE tenant_id=$1 AND status='refused'`, s.tenant).Scan(&refused); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM deletion_records WHERE tenant_id=$1`, s.tenant).Scan(&deletions)
	}); err != nil {
		t.Fatal(err)
	}
	if refused != len(Classes) || deletions != 0 {
		t.Fatalf("trilha refused=%d deletions=%d", refused, deletions)
	}
	err = testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenant_retention_profiles SET automatic_purge = true WHERE tenant_id=$1`, s.tenant)
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" {
		t.Fatalf("ligar expurgo: %v", err)
	}
	var enabled bool
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT automatic_purge FROM tenant_retention_profiles WHERE tenant_id=$1`, s.tenant).Scan(&enabled)
	}); err != nil || enabled {
		t.Fatalf("perfil depois da recusa: %v enabled=%v", err, enabled)
	}
	if _, err := svc.SavePolicy(ctx, s.admin, Policy{AutomaticPurge: true}); !errors.Is(err, ErrPurgeDisabled) {
		t.Fatalf("salvar com expurgo: %v", err)
	}
}

func TestPlanLimitsBlockNewRowsAndKeepHistory(t *testing.T) {
	s := seedTenant(t)
	svc := Service{DB: testStore}
	ctx := context.Background()
	conv := uuid.New()
	first := uuid.New()
	opened := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'open','menu','P-91','202611',1,d.sector_id,$4,$4
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$5
			ORDER BY i.id LIMIT 1
		`, conv, s.tenant, s.inbox, opened, s.contact); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO attachments (tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,expires_at)
			VALUES ($1,$2,$3,$4,'outgoing','ja-existente.pdf',100,$5,$6)
		`, s.tenant, first, conv, uuid.New(), "obj-"+first.String(), opened.Add(24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	setLimits(t, s.tenant, `{"max_agents":2,"max_numbers":1,"max_storage_bytes":100,"attachment_reserve_bytes":40}`)
	usage, err := svc.PlanUsage(ctx, s.admin)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Numbers != 1 || usage.MaxNumbers == nil || *usage.MaxNumbers != 1 || usage.StorageBytes != 100 || usage.Agents != 2 {
		t.Fatalf("uso: %+v", usage)
	}
	if !strings.Contains(usage.Note, "não apaga o histórico") {
		t.Fatalf("nota: %s", usage.Note)
	}
	connector := uuid.New()
	err = testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled) VALUES ($1,$2,'meta_cloud',$3,'connected',true)`, connector, s.tenant, "origin-"+connector.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1,$2,$3,'Segundo')`, uuid.New(), s.tenant, connector)
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || !strings.Contains(pg.Message, "limite de números do plano atingido") {
		t.Fatalf("número novo: %v", err)
	}
	extra := uuid.New()
	err = testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO attachments (tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,expires_at)
			VALUES ($1,$2,$3,$4,'outgoing','novo.pdf',1,$5,$6)
		`, s.tenant, extra, conv, uuid.New(), "obj-"+extra.String(), opened.Add(24*time.Hour))
		return err
	})
	if !errors.As(err, &pg) || !strings.Contains(pg.Message, "limite de armazenamento do plano atingido") {
		t.Fatalf("upload novo: %v", err)
	}
	incoming := uuid.New()
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO attachments (tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,expires_at)
			VALUES ($1,$2,$3,$4,'incoming','entrada.pdf',40,$5,$6)
		`, s.tenant, incoming, conv, uuid.New(), "obj-"+incoming.String(), opened.Add(24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	var numbers int
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT expected_bytes FROM attachments WHERE tenant_id=$1 AND id=$2`, s.tenant, first).Scan(&bytes); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM inboxes WHERE tenant_id=$1`, s.tenant).Scan(&numbers)
	}); err != nil {
		t.Fatal(err)
	}
	if bytes != 100 || numbers != 1 {
		t.Fatalf("histórico bytes=%d números=%d", bytes, numbers)
	}
	days := 30
	saved, err := svc.SavePolicy(ctx, s.admin, Policy{DisplayName: "Nexo Fibra", ContentDays: &days})
	if err != nil || saved.DisplayName != "Nexo Fibra" || saved.AutomaticPurge || saved.ContentDays == nil || *saved.ContentDays != 30 {
		t.Fatalf("política: %v %+v", err, saved)
	}
}

type rowCensus struct {
	messages, attachments, conversations, cycles int
}

func counts(t *testing.T, tenant uuid.UUID) rowCensus {
	t.Helper()
	var out rowCensus
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
