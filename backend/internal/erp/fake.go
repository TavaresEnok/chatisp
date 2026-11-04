package erp

import (
	"context"
)

type record struct {
	customer string
	contract string
	name     string
	invoice  string
	pix      string
}

// catalog is fixed. The same reference always resolves to the same ids.
var catalog = []record{
	{
		customer: "fake-customer-casa",
		contract: "fake-contract-casa",
		name:     "Cadastro simulado — casa",
		invoice:  "fake-inv-casa-1",
		pix:      "simulacao:fake-pix-casa-1",
	},
	{
		customer: "fake-customer-comercio",
		contract: "fake-contract-comercio",
		name:     "Cadastro simulado — comércio",
		invoice:  "fake-inv-comercio-1",
		pix:      "simulacao:fake-pix-comercio-1",
	},
}

var referenceOf = map[string]record{
	"ref-casa":     catalog[0],
	"ref-comercio": catalog[1],
}

// Fake is the deterministic simulation. It does not open a network connection.
type Fake struct {
	enabled   map[string]bool
	down      bool
	ambiguous map[string]bool
	Calls     int
}

// NewFake enables only the named capabilities. The rest answer unavailable.
func NewFake(capabilities []string) *Fake {
	enabled := make(map[string]bool, len(capabilities))
	for _, name := range capabilities {
		if knownCapability(name) {
			enabled[name] = true
		}
	}
	return &Fake{enabled: enabled, ambiguous: map[string]bool{}}
}

// SetDown makes every call fail before returning data.
func (f *Fake) SetDown(down bool) {
	if f != nil {
		f.down = down
	}
}

// SetAmbiguous marks one local key as an external timeout with no proof.
func (f *Fake) SetAmbiguous(localKey string) {
	if f != nil {
		f.ambiguous[localKey] = true
	}
}

// Call resolves a capability from the fixed catalog.
func (f *Fake) Call(_ context.Context, in Consult) (View, error) {
	if f == nil {
		return View{}, ErrDown
	}
	f.Calls++
	if f.down {
		return View{}, ErrDown
	}
	if f.ambiguous[in.LocalKey] {
		return View{}, ErrAmbiguous
	}
	view := View{
		Driver:      DriverFake,
		Simulated:   true,
		Capability:  in.Capability,
		ConsultedAt: in.Now.UTC(),
		Effect:      effectOf(in.Capability),
	}
	if !f.enabled[in.Capability] {
		view.Outcome = OutcomeUnavailable
		view.Reason = ReasonUnavailable
		return view, nil
	}
	switch in.Capability {
	case SearchCustomer:
		rec, ok := referenceOf[in.Reference]
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.Customer = customerOf(rec)
		view.ExternalRef = rec.customer
	case GetCustomerSummary:
		rec, ok := customerRecord(in.CustomerID)
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.Customer = customerOf(rec)
		view.ExternalRef = rec.customer
	case ListOpenInvoices:
		rec, ok := customerRecord(in.CustomerID)
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.Invoices = []Invoice{{ExternalID: rec.invoice, Label: "fatura simulada em aberto"}}
		view.ExternalRef = rec.invoice
	case GetInvoiceDocument:
		rec, ok := invoiceRecord(in.InvoiceID)
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.Document = []byte("simulação\n" + rec.invoice + "\n")
		view.ExternalRef = rec.invoice
	case GetPixCode:
		rec, ok := invoiceRecord(in.InvoiceID)
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.PixCode = rec.pix
		view.ExternalRef = rec.invoice
	default:
		view.Outcome = OutcomeUnavailable
		view.Reason = ReasonUnavailable
	}
	return view, nil
}

func customerOf(rec record) *Customer {
	return &Customer{ExternalID: rec.customer, ContractID: rec.contract, DisplayName: rec.name}
}

func customerRecord(id string) (record, bool) {
	for _, rec := range catalog {
		if rec.customer == id {
			return rec, true
		}
	}
	return record{}, false
}

func invoiceRecord(id string) (record, bool) {
	for _, rec := range catalog {
		if rec.invoice == id {
			return rec, true
		}
	}
	return record{}, false
}
