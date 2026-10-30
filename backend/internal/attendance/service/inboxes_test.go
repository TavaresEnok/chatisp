package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestListInboxesRevalidatesMembership(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	boxes, err := svc.ListInboxes(ctx, p)
	if err != nil || len(boxes) != 1 || boxes[0].ID != w.inbox {
		t.Fatalf("boxes: %+v %v", boxes, err)
	}
	wrong := p
	wrong.UserID = uuid.New()
	if _, err := svc.ListInboxes(ctx, wrong); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong user: %v", err)
	}
	super := p
	super.IsSuperadmin = true
	if _, err := svc.ListInboxes(ctx, super); !errors.Is(err, ErrForbidden) {
		t.Fatalf("superadmin: %v", err)
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.UpdateMembershipStatus(ctx, w.membership, "disabled")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListInboxes(ctx, p); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked: %v", err)
	}
}
