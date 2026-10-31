package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestOperatorDirectoryFunctionIsRestricted(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	var definer bool
	var fixed []string
	var app, platform, ingress, public bool
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.prosecdef, p.proconfig,
		 has_function_privilege('chatisp_app',p.oid,'EXECUTE'), has_function_privilege('chatisp_platform',p.oid,'EXECUTE'),
		 has_function_privilege('chatisp_ingress',p.oid,'EXECUTE'), has_function_privilege('public',p.oid,'EXECUTE')
		 FROM pg_proc p WHERE p.proname='operator_directory'`).Scan(&definer, &fixed, &app, &platform, &ingress, &public)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !definer || len(fixed) == 0 || !app || platform || ingress || public {
		t.Fatalf("definer=%v config=%v app=%v platform=%v ingress=%v public=%v", definer, fixed, app, platform, ingress, public)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		items, err := OperatorDirectory(ctx, tx, w.tenant, w.user, w.membership, w.inbox, nil, 0)
		if err != nil {
			return err
		}
		if len(items) != 0 {
			t.Errorf("invalid limit returned %d rows", len(items))
		}
		items, err = OperatorDirectory(ctx, tx, w.tenant, w.user, w.membership, w.inbox, nil, 10)
		if err != nil {
			return err
		}
		if len(items) != 1 || items[0].MembershipID != w.membership || items[0].Label == "" {
			t.Errorf("unexpected entries: %+v", items)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
