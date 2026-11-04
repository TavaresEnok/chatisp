package main

import (
	"context"

	"chatisp/internal/erp"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const m4bImageMediaID = "sim-media-fachada"

// Minimal JPEG that the inbound sniffer accepts. The stand-in fetcher serves
// these bytes and does not dial Meta.
var m4bJPEG = []byte{0xff, 0xd8, 0xff, 0x00}

// m4bInfo is the disposable catalog the browser journey reads. Nothing here is
// a production credential.
type m4bInfo struct {
	ImageMediaID, CustomerID, ContractID, InvoiceLabel, Simulation string
}

func seedM4B(ctx context.Context, store *db.Store, tenant, sector, member uuid.UUID) error {
	capabilities := map[string]bool{}
	for _, name := range erp.AllCapabilities() {
		capabilities[name] = true
	}
	return store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		if err := erp.InsertConnection(c, tx, erp.Connection{
			TenantID: tenant, ID: uuid.New(), Driver: erp.DriverFake, Simulated: true, Enabled: true,
			Availability: erp.AvailabilityUp, SectorID: sector, Capabilities: capabilities,
		}); err != nil {
			return err
		}
		for _, name := range []string{erp.ListOpenInvoices, erp.GetInvoiceDocument, erp.GetPixCode} {
			if err := erp.InsertGrant(c, tx, tenant, member, name); err != nil {
				return err
			}
		}
		return nil
	})
}
