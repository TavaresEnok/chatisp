package erp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFakeIsDeterministicAndLabeled(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	fake := NewFake(AllCapabilities())
	first, err := fake.Call(context.Background(), Consult{Capability: SearchCustomer, Reference: "ref-casa", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fake.Call(context.Background(), Consult{Capability: SearchCustomer, Reference: "ref-casa", Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Simulated || first.Driver != DriverFake || first.Customer == nil {
		t.Fatalf("view: %+v", first)
	}
	if first.Customer.ExternalID != second.Customer.ExternalID || first.Customer.ContractID != second.Customer.ContractID || first.Customer.DisplayName != second.Customer.DisplayName {
		t.Fatalf("instável: %+v %+v", first.Customer, second.Customer)
	}
	other, err := fake.Call(context.Background(), Consult{Capability: SearchCustomer, Reference: "ref-comercio", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if other.Customer.ExternalID == first.Customer.ExternalID {
		t.Fatal("referências diferentes colidiram")
	}
	doc, err := fake.Call(context.Background(), Consult{Capability: GetInvoiceDocument, InvoiceID: "fake-inv-casa-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	again, err := fake.Call(context.Background(), Consult{Capability: GetInvoiceDocument, InvoiceID: "fake-inv-casa-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.Document) == "" || string(doc.Document) != string(again.Document) {
		t.Fatalf("documento instável: %q %q", doc.Document, again.Document)
	}
	pix, err := fake.Call(context.Background(), Consult{Capability: GetPixCode, InvoiceID: "fake-inv-casa-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if pix.PixCode != "simulacao:fake-pix-casa-1" {
		t.Fatalf("pix: %q", pix.PixCode)
	}
	foreign, err := fake.Call(context.Background(), Consult{
		Capability: GetPixCode, InvoiceID: "fake-inv-comercio-1",
		CustomerID: "fake-customer-casa", ContractID: "fake-contract-casa", Now: now,
	})
	if err != nil || foreign.Outcome != OutcomeNotFound || foreign.Reason != ReasonInvoice || foreign.PixCode != "" {
		t.Fatalf("fatura de outro cliente: %+v %v", foreign, err)
	}
	missing := NewFake(DefaultCapabilities())
	off, err := missing.Call(context.Background(), Consult{Capability: ListOpenInvoices, CustomerID: first.Customer.ExternalID, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if off.Outcome != OutcomeUnavailable || off.Reason != ReasonUnavailable || len(off.Invoices) != 0 {
		t.Fatalf("capacidade ausente: %+v", off)
	}
}

func TestPhoneDoesNotSelectAndProtocolStays(t *testing.T) {
	if _, err := NormalizeReference("+5511999999999"); !errors.Is(err, ErrPhoneNotSelector) {
		t.Fatalf("telefone: %v", err)
	}
	links := []Link{
		{ID: uuid.New(), ExternalCustomerID: "fake-customer-casa"},
		{ID: uuid.New(), ExternalCustomerID: "fake-customer-comercio"},
	}
	if _, err := Choose(links); !errors.Is(err, ErrChoiceRequired) {
		t.Fatalf("escolha: %v", err)
	}
	links[1].Selected = true
	got, err := Choose(links)
	if err != nil || got.ExternalCustomerID != "fake-customer-comercio" {
		t.Fatalf("selecionado: %+v %v", got, err)
	}
	local, err := KeepLocalProtocol("P-LOCAL-1", "ERP-99")
	if err != nil || local != "P-LOCAL-1" {
		t.Fatalf("protocolo: %q %v", local, err)
	}
}

func TestOutageDoesNotBlockTheReply(t *testing.T) {
	tenant, id := uuid.New(), uuid.New()
	sector := uuid.New()
	fake := NewFake(AllCapabilities())
	fake.SetDown(true)
	svc, err := New(connection(tenant, id, sector, AllCapabilities()), fake)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := AcceptReply("  olá  ")
	if err != nil || reply != "olá" {
		t.Fatalf("resposta: %q %v", reply, err)
	}
	view, err := svc.Lateral(context.Background(), agent(tenant, sector), Consult{
		Capability: SearchCustomer, Reference: "ref-casa", Now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Outcome != OutcomeUnavailable || view.Customer != nil || fake.Calls != 1 || !ChatContinues(err) {
		t.Fatalf("view=%+v calls=%d", view, fake.Calls)
	}
	if reply != "olá" {
		t.Fatal("a resposta local mudou")
	}
}

func TestSensitiveStaysHiddenWithoutVerification(t *testing.T) {
	tenant, id, sector := uuid.New(), uuid.New(), uuid.New()
	fake := NewFake(AllCapabilities())
	svc, err := New(connection(tenant, id, sector, AllCapabilities()), fake)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	view, err := svc.Lateral(context.Background(), agent(tenant, sector), Consult{
		Capability: ListOpenInvoices, CustomerID: "fake-customer-casa", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Outcome != OutcomeHidden || view.Reason != ReasonHidden || fake.Calls != 0 || len(view.Invoices) != 0 {
		t.Fatalf("view=%+v calls=%d", view, fake.Calls)
	}
	shown, err := svc.Lateral(context.Background(), agent(tenant, sector), Consult{
		Capability: ListOpenInvoices, CustomerID: "fake-customer-casa", Verification: "verificacao-1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if shown.Outcome != OutcomeOK || len(shown.Invoices) != 1 || !shown.Simulated || fake.Calls != 1 {
		t.Fatalf("lista: %+v calls=%d", shown, fake.Calls)
	}
}

func TestAmbiguousResultIsNotRepeated(t *testing.T) {
	tenant, id, sector := uuid.New(), uuid.New(), uuid.New()
	fake := NewFake(AllCapabilities())
	fake.SetAmbiguous("boleto-1")
	svc, err := New(connection(tenant, id, sector, AllCapabilities()), fake)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	in := Consult{
		Capability: GetPixCode, InvoiceID: "fake-inv-casa-1", Verification: "verificacao-1",
		LocalKey: "boleto-1", Now: now,
	}
	first, err := svc.Consult(context.Background(), agent(tenant, sector), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != OutcomeUncertain || first.View.PixCode != "" || fake.Calls != 1 {
		t.Fatalf("primeiro: %+v calls=%d", first, fake.Calls)
	}
	second, err := svc.Consult(context.Background(), agent(tenant, sector), in)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Outcome != OutcomeUncertain || fake.Calls != 1 {
		t.Fatalf("repetiu: %+v calls=%d", second, fake.Calls)
	}
	changed := in
	changed.InvoiceID = "fake-inv-comercio-1"
	if _, err = svc.Consult(context.Background(), agent(tenant, sector), changed); !errors.Is(err, ErrConflict) || fake.Calls != 1 {
		t.Fatalf("conflito: %v calls=%d", err, fake.Calls)
	}
}

func TestFakeSummaryAndInvoicesHonorContract(t *testing.T) {
	fake := NewFake(AllCapabilities())
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	mismatch := Consult{Capability: GetCustomerSummary, CustomerID: "fake-customer-casa", ContractID: "fake-contract-comercio", Now: now}
	view, err := fake.Call(context.Background(), mismatch)
	if err != nil || view.Outcome != OutcomeNotFound || view.Customer != nil {
		t.Fatalf("resumo incompatível: %+v %v", view, err)
	}
	invoices := mismatch
	invoices.Capability = ListOpenInvoices
	view, err = fake.Call(context.Background(), invoices)
	if err != nil || view.Outcome != OutcomeNotFound || len(view.Invoices) != 0 || view.ExternalRef != "" {
		t.Fatalf("faturas incompatíveis: %+v %v", view, err)
	}
	open := Consult{Capability: GetCustomerSummary, CustomerID: "fake-customer-comercio", Now: now}
	view, err = fake.Call(context.Background(), open)
	if err != nil || view.Outcome != OutcomeOK || view.Customer == nil || view.Customer.ContractID != "fake-contract-comercio" {
		t.Fatalf("contrato opcional: %+v %v", view, err)
	}
	open.Capability = ListOpenInvoices
	view, err = fake.Call(context.Background(), open)
	if err != nil || view.Outcome != OutcomeOK || len(view.Invoices) != 1 || view.Invoices[0].ExternalID != "fake-inv-comercio-1" {
		t.Fatalf("faturas sem contrato: %+v %v", view, err)
	}
}

func TestSuccessfulKeyIsNotExecutedAgain(t *testing.T) {
	tenant, id, sector := uuid.New(), uuid.New(), uuid.New()
	fake := NewFake(AllCapabilities())
	svc, err := New(connection(tenant, id, sector, AllCapabilities()), fake)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	in := Consult{
		Capability: GetInvoiceDocument, InvoiceID: "fake-inv-casa-1", Verification: "verificacao-1",
		LocalKey: "doc-1", Now: now,
	}
	first, err := svc.Consult(context.Background(), agent(tenant, sector), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != OutcomeOK || string(first.View.Document) == "" {
		t.Fatalf("documento: %+v", first)
	}
	second, err := svc.Consult(context.Background(), agent(tenant, sector), in)
	if err != nil || second.ID != first.ID || fake.Calls != 1 {
		t.Fatalf("segunda: %+v calls=%d err=%v", second, fake.Calls, err)
	}
}

func TestCircuitOpensPerConnection(t *testing.T) {
	tenant, id, sector := uuid.New(), uuid.New(), uuid.New()
	fake := NewFake(AllCapabilities())
	fake.SetDown(true)
	svc, err := New(connection(tenant, id, sector, AllCapabilities()), fake)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		view, err := svc.Lateral(context.Background(), agent(tenant, sector), Consult{
			Capability: SearchCustomer, Reference: "ref-casa", Now: start.Add(time.Duration(i) * time.Second),
		})
		if err != nil || view.Outcome != OutcomeUnavailable {
			t.Fatalf("chamada %d: %+v %v", i, view, err)
		}
	}
	if fake.Calls != 3 {
		t.Fatalf("calls=%d", fake.Calls)
	}
	view, err := svc.Lateral(context.Background(), agent(tenant, sector), Consult{
		Capability: SearchCustomer, Reference: "ref-casa", Now: start.Add(2 * time.Second),
	})
	if err != nil || view.Outcome != OutcomeUnavailable || fake.Calls != 3 {
		t.Fatalf("circuito aberto chamou de novo: %+v calls=%d err=%v", view, fake.Calls, err)
	}
	view, err = svc.Lateral(context.Background(), agent(tenant, sector), Consult{
		Capability: SearchCustomer, Reference: "ref-casa", Now: start.Add(2*time.Second + breakerOpenFor),
	})
	if err != nil || fake.Calls != 4 {
		t.Fatalf("sonda: %+v calls=%d err=%v", view, fake.Calls, err)
	}
}

func TestAgentSectorAndUnmarkedAdapter(t *testing.T) {
	tenant, id, sector := uuid.New(), uuid.New(), uuid.New()
	fake := NewFake(DefaultCapabilities())
	svc, err := New(connection(tenant, id, sector, DefaultCapabilities()), fake)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	outsider := agent(tenant, uuid.New())
	if _, err = svc.Lateral(context.Background(), outsider, Consult{Capability: SearchCustomer, Reference: "ref-casa", Now: now}); !errors.Is(err, ErrDenied) || fake.Calls != 0 {
		t.Fatalf("setor: %v calls=%d", err, fake.Calls)
	}
	supervisor := Principal{TenantID: tenant, UserID: uuid.New(), Membership: uuid.New(), Role: "supervisor"}
	view, err := svc.Lateral(context.Background(), supervisor, Consult{Capability: SearchCustomer, Reference: "ref-casa", Now: now})
	if err != nil || view.Outcome != OutcomeOK || !view.Simulated {
		t.Fatalf("supervisor: %+v %v", view, err)
	}
	stub := &stubAdapter{view: View{Driver: "ixc", Simulated: false, Outcome: OutcomeOK, Customer: &Customer{ExternalID: "real"}}}
	other, err := New(connection(tenant, id, sector, DefaultCapabilities()), stub)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := other.Lateral(context.Background(), supervisor, Consult{Capability: SearchCustomer, Reference: "ref-casa", Now: now})
	if err != nil || hidden.Outcome != OutcomeUnavailable || hidden.Reason != ReasonNotSimulation || hidden.Customer != nil || stub.calls != 1 {
		t.Fatalf("não simulado: %+v calls=%d err=%v", hidden, stub.calls, err)
	}
}

func connection(tenant, id, sector uuid.UUID, caps []string) Connection {
	on := map[string]bool{}
	for _, name := range caps {
		on[name] = true
	}
	return Connection{
		TenantID: tenant, ID: id, Driver: DriverFake, Simulated: true,
		Enabled: true, Availability: AvailabilityUp, SectorID: sector, Capabilities: on,
	}
}

func agent(tenant, sector uuid.UUID) Principal {
	return Principal{TenantID: tenant, UserID: uuid.New(), Membership: uuid.New(), Role: "agent", SectorIDs: []uuid.UUID{sector}}
}

type stubAdapter struct {
	view  View
	calls int
}

func (s *stubAdapter) Call(context.Context, Consult) (View, error) {
	s.calls++
	return s.view, nil
}
