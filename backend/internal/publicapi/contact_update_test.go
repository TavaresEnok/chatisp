package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestUpdateContactKeepsDocumentOut(t *testing.T) {
	f := seedConversation(t, 30)
	ctx := context.Background()
	secret, _, err := IssueScoped(ctx, testStore, f.tenant, []string{"contacts.write"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := IssueScoped(ctx, testStore, f.tenant, []string{"contacts.read"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if err := testStore.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE contacts SET notes='observacao-que-nao-sai', external_ref='ref-original' WHERE tenant_id=$1 AND id=$2`, f.tenant, f.contact); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name, external_ref) VALUES ($1,$2,'Outro','ref-ocupada')`, other, f.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	denied := patchContact(t, f.contact, reader, `{"name":"Nao"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("leitura %d %s", denied.Code, denied.Body.String())
	}
	document := patchContact(t, f.contact, secret, `{"name":"Titular","document":"123"}`)
	if document.Code != http.StatusBadRequest || strings.Contains(document.Body.String(), "observacao-que-nao-sai") {
		t.Fatalf("documento %d %s", document.Code, document.Body.String())
	}
	clash := patchContact(t, f.contact, secret, `{"external_ref":"ref-ocupada"}`)
	if clash.Code != http.StatusConflict {
		t.Fatalf("conflito %d %s", clash.Code, clash.Body.String())
	}
	missing := patchContact(t, uuid.New(), secret, `{"name":"Ninguem"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("ausente %d %s", missing.Code, missing.Body.String())
	}
	ok := patchContact(t, f.contact, secret, `{"name":"Titular Atualizado","phone":"+5511988887777","email":"Titular@Example.com","external_ref":"ref-nova"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("atualização %d %s", ok.Code, ok.Body.String())
	}
	body := ok.Body.String()
	if strings.Contains(body, "observacao-que-nao-sai") || strings.Contains(body, "DOCUMENTO") {
		t.Fatalf("vazou: %s", body)
	}
	var view contactView
	if err := json.Unmarshal(ok.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Name != "Titular Atualizado" || view.Phone == nil || *view.Phone != "+5511988887777" || view.Email == nil || *view.Email != "titular@example.com" || view.ExternalRef == nil || *view.ExternalRef != "ref-nova" {
		t.Fatalf("contato %#v", view)
	}
	var notes string
	if err := testStore.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT notes FROM contacts WHERE tenant_id=$1 AND id=$2`, f.tenant, f.contact).Scan(&notes)
	}); err != nil || notes != "observacao-que-nao-sai" {
		t.Fatalf("notas %q %v", notes, err)
	}

	if err := testStore.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE contacts SET privacy_erased_at=now(), name='titular excluído', phone=NULL, email=NULL, notes='', external_ref=NULL WHERE tenant_id=$1 AND id=$2`, f.tenant, f.contact)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	erased := patchContact(t, f.contact, secret, `{"name":"Voltou"}`)
	if erased.Code != http.StatusConflict || !strings.Contains(erased.Body.String(), "titular excluído") {
		t.Fatalf("excluído %d %s", erased.Code, erased.Body.String())
	}
	var name string
	if err := testStore.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM contacts WHERE tenant_id=$1 AND id=$2`, f.tenant, f.contact).Scan(&name)
	}); err != nil || name != "titular excluído" {
		t.Fatalf("nome depois da recusa: %s %v", name, err)
	}
}

func patchContact(t *testing.T, id uuid.UUID, secret, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/contacts/"+id.String(), bytes.NewBufferString(payload))
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	testAPI.ServeHTTP(rec, req)
	return rec
}
