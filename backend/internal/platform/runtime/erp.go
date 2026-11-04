package runtime

import (
	"chatisp/internal/erp"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/metrics"
)

// composeERP installs only the simulated connector.
// SupplierEnabled stays false, so the real adapter is not constructed.
func composeERP(store *db.Store, meter *metrics.Metrics) *erp.Panel {
	if store == nil || erp.SupplierEnabled() {
		return nil
	}
	return erp.NewPanel(erp.PanelOptions{
		DB:      store,
		Meter:   meter,
		Adapter: erp.NewFake(erp.AllCapabilities()),
	})
}
