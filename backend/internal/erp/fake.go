package erp

import (
	"context"
	"sync"
	"time"
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
	mu        sync.Mutex
	enabled   map[string]bool
	down      bool
	delay     time.Duration
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
	if f == nil {
		return
	}
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
}

// SetDelay waits before answering. A cancelled context returns while the wait is pending.
func (f *Fake) SetDelay(delay time.Duration) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.delay = delay
	f.mu.Unlock()
}

// SetAmbiguous marks one local key as an external timeout with no proof.
func (f *Fake) SetAmbiguous(localKey string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.ambiguous[localKey] = true
	f.mu.Unlock()
}

// Call resolves a capability from the fixed catalog.
func (f *Fake) Call(ctx context.Context, in Consult) (View, error) {
	if f == nil {
		return View{}, ErrDown
	}
	if ctx == nil {
		ctx = context.Background()
	}
	f.mu.Lock()
	f.Calls++
	down := f.down
	delay := f.delay
	ambiguous := f.ambiguous[in.LocalKey]
	enabled := f.enabled[in.Capability]
	f.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return View{}, ctx.Err()
		case <-timer.C:
		}
	} else if err := ctx.Err(); err != nil {
		return View{}, err
	}
	if down {
		return View{}, ErrDown
	}
	if ambiguous {
		return View{}, ErrAmbiguous
	}
	view := View{
		Driver:      DriverFake,
		Simulated:   true,
		Capability:  in.Capability,
		ConsultedAt: in.Now.UTC(),
		Effect:      effectOf(in.Capability),
	}
	if !enabled {
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
		rec, ok := customerForConsult(in)
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.Customer = customerOf(rec)
		view.ExternalRef = rec.customer
	case ListOpenInvoices:
		rec, ok := customerForConsult(in)
		if !ok {
			view.Outcome = OutcomeNotFound
			view.Reason = ReasonNotFound
			return view, nil
		}
		view.Outcome = OutcomeOK
		view.Invoices = []Invoice{{ExternalID: rec.invoice, Label: "fatura simulada em aberto"}}
		view.ExternalRef = rec.invoice
	case GetInvoiceDocument:
		rec, ok := ownedInvoice(in)
		if !ok {
			return invoiceMiss(view, in), nil
		}
		view.Outcome = OutcomeOK
		view.Document = []byte("simulação\n" + rec.invoice + "\n")
		view.ExternalRef = rec.invoice
	case GetPixCode:
		rec, ok := ownedInvoice(in)
		if !ok {
			return invoiceMiss(view, in), nil
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

// customerForConsult applies the catalog pair. An empty contract keeps the
// customer lookup. A contract that is not that customer's returns no row.
func customerForConsult(in Consult) (record, bool) {
	rec, ok := customerRecord(in.CustomerID)
	if !ok {
		return record{}, false
	}
	if in.ContractID != "" && rec.contract != in.ContractID {
		return record{}, false
	}
	return rec, true
}

func invoiceRecord(id string) (record, bool) {
	for _, rec := range catalog {
		if rec.invoice == id {
			return rec, true
		}
	}
	return record{}, false
}

// ownedInvoice resolves a simulated invoice only when it belongs to the customer
// and contract supplied by the caller. An empty customer keeps the catalog lookup
// used by direct adapter tests.
func ownedInvoice(in Consult) (record, bool) {
	rec, ok := invoiceRecord(in.InvoiceID)
	if !ok {
		return record{}, false
	}
	if in.CustomerID != "" && rec.customer != in.CustomerID {
		return record{}, false
	}
	if in.ContractID != "" && rec.contract != in.ContractID {
		return record{}, false
	}
	return rec, true
}

func invoiceMiss(view View, in Consult) View {
	view.Outcome = OutcomeNotFound
	view.Reason = ReasonNotFound
	if rec, ok := invoiceRecord(in.InvoiceID); ok {
		if (in.CustomerID != "" && rec.customer != in.CustomerID) || (in.ContractID != "" && rec.contract != in.ContractID) {
			view.Reason = ReasonInvoice
		}
	}
	return view
}
