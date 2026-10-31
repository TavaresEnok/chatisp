package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
)

func labelsOf(page OperatorDirectoryPage) map[uuid.UUID]string {
	m := map[uuid.UUID]string{}
	for _, e := range page.Items {
		m[e.MembershipID] = e.Label
	}
	return m
}

func TestTransferSectorDestinationsWithoutDestinationMembership(t *testing.T) {
	ctx := context.Background()
	w, foreign := seedWorld(t), seedWorld(t)
	svc := live(nil)
	conv := mustReceive(t, svc, w, "directory-destinations", "hello", at(0)).Conversation
	target, unlinked, inactive := uuid.New(), uuid.New(), uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name,active) VALUES($1,$2,'Destino',true),($1,$3,'Sem caixa',true),($1,$4,'Inativo',false)`, w.tenant, target, unlinked, inactive)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$4),($1,$3,$4)`, w.tenant, target, inactive, w.inbox)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.TransferSectors(ctx, principal(w), w.inbox, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.ID == target {
			found = true
		}
		if e.ID == unlinked || e.ID == inactive {
			t.Fatalf("invalid destination: %+v", e)
		}
	}
	if !found {
		t.Fatal("destination outside agent memberships missing")
	}
	if _, err := svc.TransferSectors(ctx, principal(foreign), w.inbox, conv.ID); err == nil {
		t.Fatal("cross tenant destination access")
	}
	if _, err := svc.TransferSectors(ctx, principal(w), foreign.inbox, conv.ID); err == nil {
		t.Fatal("forged inbox accepted")
	}
	if _, err := svc.TransferToSector(ctx, principal(w), AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: target, ExpectedVersion: conv.Version, ClientRequestID: "directory-transfer", At: at(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.History(ctx, principal(w), w.inbox, conv.ID); err == nil {
		t.Fatal("destination content accessible after transfer")
	}
	if _, err := svc.TransferSectors(ctx, principal(w), w.inbox, conv.ID); err == nil {
		t.Fatal("destinations accessible after loss of conversation")
	}
}

func TestDirectoryScopeAndPagination(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := New(testStore, nil)
	// 5 more agents in the inbox's sector, one supervisor, one agent outside the inbox's sectors.
	members := map[uuid.UUID]bool{w.membership: true}
	for i := 0; i < 5; i++ {
		_, m := seedMember(t, w.tenant, "agent")
		members[m] = true
	}
	supUser, sup := seedMember(t, w.tenant, "supervisor")
	members[sup] = true
	outsiderUser, outsider := seedMember(t, w.tenant, "agent")
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		sector := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'Isolado')`, w.tenant, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, w.tenant, outsider); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sector_memberships VALUES ($1,$2,$3)`, w.tenant, sector, outsider)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Paginate independently of any conversation page: limit 3 walks all 7.
	agent := principal(w)
	seen := map[uuid.UUID]string{}
	var after *uuid.UUID
	for pages := 0; pages < 10; pages++ {
		page, err := svc.Directory(ctx, agent, w.inbox, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 3 {
			t.Fatalf("page too large: %d", len(page.Items))
		}
		for id, label := range labelsOf(page) {
			seen[id] = label
		}
		if page.NextCursor == nil {
			break
		}
		after = page.NextCursor
	}
	if len(seen) != len(members) {
		t.Fatalf("directory has %d entries, want %d", len(seen), len(members))
	}
	for id := range members {
		if seen[id] == "" {
			t.Fatalf("missing label for %s", id)
		}
	}
	if _, ok := seen[outsider]; ok {
		t.Fatal("agent outside the inbox sectors is listed")
	}
	if seen[w.membership] != "u-"+w.user.String()+"@example.com" {
		t.Fatalf("label is not the real identity: %q", seen[w.membership])
	}
	// Supervisor sees the same inbox scope; the outsider agent is denied the inbox.
	if page, err := svc.Directory(ctx, Principal{TenantID: w.tenant, UserID: supUser, MembershipID: sup, Role: "supervisor"}, w.inbox, nil, 100); err != nil || len(page.Items) != len(members) {
		t.Fatalf("supervisor page: %v %d", err, len(page.Items))
	}
	if _, err := svc.Directory(ctx, Principal{TenantID: w.tenant, UserID: outsiderUser, MembershipID: outsider, Role: "agent"}, w.inbox, nil, 10); err == nil {
		t.Fatal("outsider read the directory")
	}
	// Disjoint sectors sharing an inbox must not expose one another's identities.
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO sector_inboxes (tenant_id,sector_id,inbox_id)
		 SELECT tenant_id,sector_id,$3 FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, w.tenant, outsider, w.inbox)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page, err := svc.Directory(ctx, agent, w.inbox, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := labelsOf(page)[outsider]; ok {
		t.Fatal("agent saw identity from disjoint sector sharing the inbox")
	}
	page, err = svc.Directory(ctx, Principal{TenantID: w.tenant, UserID: outsiderUser, MembershipID: outsider, Role: "agent"}, w.inbox, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if labelsOf(page)[outsider] == "" || labelsOf(page)[w.membership] != "" {
		t.Fatalf("disjoint agent scope: %+v", page.Items)
	}
	for _, role := range []string{"supervisor", "admin"} {
		user, member := seedMember(t, w.tenant, role)
		page, err := svc.Directory(ctx, Principal{TenantID: w.tenant, UserID: user, MembershipID: member, Role: role}, w.inbox, nil, 100)
		if err != nil || labelsOf(page)[outsider] == "" || labelsOf(page)[w.membership] == "" {
			t.Fatalf("%s lost authorized inbox-wide scope: %+v, %v", role, page.Items, err)
		}
	}
}

func TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	other := seedWorld(t)
	svc := New(testStore, nil)
	// Other tenant's principal cannot read this tenant's inbox, even claiming it.
	if _, err := svc.Directory(ctx, principal(other), w.inbox, nil, 10); err == nil {
		t.Fatal("cross-tenant directory read")
	}
	// Forged principal fields: the persisted role/membership decide, never the argument.
	forgedUser, forged := seedMember(t, w.tenant, "agent")
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, w.tenant, forged); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Directory(ctx, Principal{TenantID: w.tenant, UserID: forgedUser, MembershipID: forged, Role: "admin"}, w.inbox, nil, 10); err == nil {
		t.Fatal("role argument escalated access")
	}
	// The function itself rejects a user/membership mismatch and a foreign tenant argument.
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		for name, args := range map[string][]uuid.UUID{
			"wrong user":     {w.tenant, uuid.New(), w.membership, w.inbox},
			"foreign tenant": {other.tenant, other.user, other.membership, other.inbox},
		} {
			items, err := store.OperatorDirectory(ctx, tx, args[0], args[1], args[2], args[3], nil, 10)
			if err != nil {
				return err
			}
			if len(items) != 0 {
				t.Errorf("%s returned %d rows", name, len(items))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// App role has no direct SELECT on the global users table.
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `SAVEPOINT s`)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `SELECT email FROM users LIMIT 1`)
		if e == nil {
			t.Error("chatisp_app can read users")
		}
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Revocation: a disabled membership is denied and disappears from the directory.
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, e := testStore.UpdateMembershipStatus(ctx, forged, "disabled")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Directory(ctx, Principal{TenantID: w.tenant, UserID: forgedUser, MembershipID: forged, Role: "agent"}, w.inbox, nil, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked directory read: %v", err)
	}
	page, err := svc.Directory(ctx, principal(w), w.inbox, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := labelsOf(page)[forged]; ok {
		t.Fatal("revoked membership still listed")
	}
}
