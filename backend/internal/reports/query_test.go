package reports

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPersistedCycleMatchesTheClockRules(t *testing.T) {
	s := seedReportTenant(t)
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	closed := opened.Add(4 * time.Hour)
	reply := opened.Add(30 * time.Minute)
	waitEnd := opened.Add(3*time.Hour + 30*time.Minute)
	now := time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
	conv := uuid.New()
	ctx := context.Background()
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at) SELECT $1,$2,$3,c.id,i.id,'closed','human',$4,'P-1','202611',1,d.sector_id,$5,$6 FROM contacts c JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3 WHERE c.tenant_id=$2 LIMIT 1`, conv, s.tenant, s.inbox, s.agent.MembershipID, opened, closed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind) VALUES ($1,$2,$3,1,$4,$5,'lentidão','resolved','cliente atendido',$6,$7,'human')`, s.tenant, uuid.New(), conv, opened, closed, s.agent.UserID, s.agent.MembershipID); err != nil {
			return err
		}
		for _, event := range []struct {
			kind string
			at   time.Time
		}{
			{"assumed", opened},
			{"wait_started", reply},
			{"wait_ended", waitEnd},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO conversation_events (tenant_id,id,conversation_id,cycle_number,kind,actor_id,at,version,controller_epoch,controller,assignee_id) VALUES ($1,$2,$3,1,$4,$5,$6,2,1,'human',$7)`, s.tenant, uuid.New(), conv, event.kind, s.agent.UserID, event.at, s.agent.MembershipID); err != nil {
				return err
			}
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at) VALUES ($1,$2,$3,$4,'inbound','customer','public','oi','received',$5,$5)`, uuid.New(), s.tenant, conv, connector, opened); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,author_membership_id,trusted_at,ingested_at) VALUES ($1,$2,$3,$4,'outbound','human','public','resposta','accepted',$5,$6,$6)`, uuid.New(), s.tenant, conv, connector, s.agent.MembershipID, reply); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at) SELECT $1,$2,$3,c.id,i.id,'open','menu','P-2','202611',1,d.sector_id,$4,$4 FROM contacts c JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id AND i.external_id LIKE 'wa-%' JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3 WHERE c.tenant_id=$2 AND i.id <> (SELECT contact_identity_id FROM conversations WHERE tenant_id=$2 AND id=$5) LIMIT 1`, uuid.New(), s.tenant, s.inbox, now.Add(-20*time.Minute), conv)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	filter := Filter{FromDate: "2026-11-03", ToDate: "2026-11-03", Group: "day", Now: now}
	got := mustQuery(t, s.admin, filter)
	if got.Cycles != 1 || got.Messages != 2 || sec(got.CloseTotal) != 4*3600 || sec(got.CloseOperational) != 3600 || sec(got.FirstResponse) != 1800 {
		t.Fatalf("persistido ciclos %d msgs %d total %d operacional %d resposta %d", got.Cycles, got.Messages, sec(got.CloseTotal), sec(got.CloseOperational), sec(got.FirstResponse))
	}
	if got.Live.InQueue != 1 || got.Live.Unassigned != 1 || got.Live.WaitAlerts != 1 || got.Live.LongestQueueWaitSeconds != 1200 {
		t.Fatalf("ao vivo %+v", got.Live)
	}
	if !strings.Contains(got.CSV, "encerramento_operacional,1,3600,3600,0") || !strings.Contains(got.CSV, "Não é o histórico completo do provedor nem do ERP") {
		t.Fatal("csv não repetiu a conta")
	}
	if len(got.Heatmap) != 1 || got.Heatmap[0].Hour != 8 || got.Heatmap[0].Weekday != 2 {
		t.Fatalf("calor %+v", got.Heatmap)
	}
	agent := mustQuery(t, s.agent, filter)
	if agent.Cycles != 1 || sec(agent.CloseOperational) != 3600 {
		t.Fatalf("atendente do setor %+v", agent.CloseOperational)
	}
	outside := mustQuery(t, s.outsider, filter)
	if outside.Cycles != 0 || outside.Live.InQueue != 0 || outside.Messages != 0 {
		t.Fatalf("outro setor viu %+v fila %d", outside.Cycles, outside.Live.InQueue)
	}
	denied := filter
	denied.SectorID = s.sector
	if _, err := queryAs(t, s.outsider, denied); !errors.Is(err, ErrForbidden) {
		t.Fatalf("setor alheio: %v", err)
	}
	denied = filter
	denied.InboxID = s.inbox
	if _, err := queryAs(t, s.outsider, denied); !errors.Is(err, ErrForbidden) {
		t.Fatalf("caixa alheia: %v", err)
	}
}

func TestPersistedBotClosureHasNoHumanResponse(t *testing.T) {
	s := seedReportTenant(t)
	opened := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	closed := opened.Add(time.Hour)
	conv := uuid.New()
	if err := testStore.WithTenantTx(context.Background(), s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at) SELECT $1,$2,$3,c.id,i.id,'closed','bot','P-bot','202611',1,d.sector_id,$4,$5 FROM contacts c JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3 WHERE c.tenant_id=$2 LIMIT 1`, conv, s.tenant, s.inbox, opened, closed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind) VALUES ($1,$2,$3,1,$4,$5,'menu','resolved','o bot encerrou',$6,$7,'bot')`, s.tenant, uuid.New(), conv, opened, closed, s.agent.UserID, s.agent.MembershipID); err != nil {
			return err
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at) VALUES ($1,$2,$3,$4,'outbound','bot','public','até logo','accepted',$5,$5)`, uuid.New(), s.tenant, conv, connector, opened.Add(time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got := mustQuery(t, s.admin, Filter{FromDate: "2026-11-03", ToDate: "2026-11-03", Now: closed})
	if got.FirstResponse.Sample != 0 || got.FirstResponse.NotApplicable != 1 || got.FirstResponse.AverageSeconds != nil {
		t.Fatalf("resposta %+v", got.FirstResponse)
	}
	if len(got.Results) != 1 || got.Results[0].Label != "Encerrado pelo bot" {
		t.Fatalf("resultado %+v", got.Results)
	}
	if sec(got.CloseTotal) != 3600 || sec(got.CloseOperational) != 3600 {
		t.Fatalf("tempos %d %d", sec(got.CloseTotal), sec(got.CloseOperational))
	}
}

func mustQuery(t *testing.T, p Principal, f Filter) Report {
	t.Helper()
	got, err := queryAs(t, p, f)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func queryAs(t *testing.T, p Principal, f Filter) (Report, error) {
	t.Helper()
	var got Report
	err := testStore.WithTenantTx(context.Background(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = Query(ctx, tx, p, f)
		return err
	})
	return got, err
}
