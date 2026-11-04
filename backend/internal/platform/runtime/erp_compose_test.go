package runtime

import (
	"context"
	"testing"

	"chatisp/internal/erp"
)

func TestComposeERPSkipsTheSupplier(t *testing.T) {
	if erp.SupplierEnabled() {
		t.Fatal("conector real habilitado")
	}
	if composeERP(nil, nil) != nil {
		t.Fatal("painel composto sem banco")
	}
	view, err := erp.Supplier().Call(context.Background(), erp.Consult{Capability: erp.SearchCustomer})
	if err == nil || view.Customer != nil {
		t.Fatalf("conector real respondeu: %+v %v", view, err)
	}
}
