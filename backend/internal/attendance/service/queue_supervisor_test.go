package service

import (
	"context"
	"testing"

	"chatisp/internal/attendance/store"
	"github.com/jackc/pgx/v5"
)

// A supervisor reads every sector queue of the tenant without being a member of
// the sector; an agent outside the sector sees nothing; another tenant sees nothing.
func TestQueueSupervisorSeesAllSectorsAndNoOtherTenant(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	c := mustReceive(t, svc, w, "queue-supervisor", "Preciso de suporte", at(1)).Conversation

	u, m := seedMember(t, w.tenant, "supervisor")
	supervisor := Principal{TenantID: w.tenant, UserID: u, MembershipID: m, Role: "supervisor"}
	page, err := svc.Queue(ctx, supervisor, store.QueueInput{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != c.ID || page.Counts.All != 1 {
		t.Fatalf("supervisor sem vínculo de setor: %+v %v", page, err)
	}

	au, am := seedMember(t, w.tenant, "agent")
	agent := Principal{TenantID: w.tenant, UserID: au, MembershipID: am, Role: "agent"}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id IN ($2,$3)`, w.tenant, am, m)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	page, err = svc.Queue(ctx, agent, store.QueueInput{})
	if err != nil || len(page.Items) != 0 || page.Counts.All != 0 {
		t.Fatalf("atendente fora do setor viu a fila: %+v %v", page, err)
	}

	foreign := seedWorld(t)
	fu, fm := seedMember(t, foreign.tenant, "supervisor")
	other := Principal{TenantID: foreign.tenant, UserID: fu, MembershipID: fm, Role: "supervisor"}
	page, err = svc.Queue(ctx, other, store.QueueInput{})
	if err != nil || len(page.Items) != 0 || page.Counts.All != 0 {
		t.Fatalf("supervisor de outro provedor viu a fila: %+v %v", page, err)
	}
}
