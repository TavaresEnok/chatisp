package erp

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGuardTimeoutDoesNotWaitForTheDelay(t *testing.T) {
	tenant, id, sector := uuid.New(), uuid.New(), uuid.New()
	fake := NewFake(AllCapabilities())
	fake.SetDelay(time.Second)
	svc, err := New(connection(tenant, id, sector, AllCapabilities()), guarded{
		inner: fake, gate: NewGate(1), timeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	view, err := svc.Lateral(context.Background(), agent(tenant, sector), Consult{
		Capability: SearchCustomer, Reference: "ref-casa", Now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if view.Outcome != OutcomeUnavailable || view.Reason != ReasonTimeout {
		t.Fatalf("view=%+v", view)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("timeout não cortou a espera: %s", elapsed)
	}
	if fake.Calls != 1 {
		t.Fatalf("calls=%d", fake.Calls)
	}
}

func TestSupplierStaysUnwired(t *testing.T) {
	if SupplierEnabled() {
		t.Fatal("conector real habilitado")
	}
	view, err := Supplier().Call(context.Background(), Consult{Capability: SearchCustomer})
	if err == nil || view.Customer != nil || view.PixCode != "" {
		t.Fatalf("conector real respondeu: %+v %v", view, err)
	}
}
