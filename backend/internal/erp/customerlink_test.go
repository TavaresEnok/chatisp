package erp

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCustomerTemplateRefusesCredentialAndDocument(t *testing.T) {
	good := []string{
		"https://erp.provedor.example/clientes/{cliente}",
		"https://erp.provedor.example/cadastro?id={cliente}&aba=geral",
		"https://erp.provedor.example:8443/app/{cliente}/ver",
	}
	for _, raw := range good {
		if _, err := ValidateCustomerTemplate(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	bad := map[string]error{
		"http://erp.provedor.example/clientes/{cliente}":               ErrLinkTemplate,
		"https://erp.provedor.example/clientes":                        ErrLinkTemplate,
		"https://erp.provedor.example/{cliente}/{cliente}":             ErrLinkTemplate,
		"https://usuario:senha@erp.provedor.example/{cliente}":         ErrLinkTemplate,
		"https://{cliente}.erp.provedor.example/":                      ErrLinkTemplate,
		"https://erp.provedor.example/{cliente}#aba":                   ErrLinkTemplate,
		"https://erp.provedor.example/{cliente}?x={outro}":             ErrLinkTemplate,
		"https://erp.provedor.example/c/{cliente} espaço":              ErrLinkTemplate,
		"https://erp.provedor.example/c/{cliente}?token=abc":           ErrLinkCredential,
		"https://erp.provedor.example/c/{cliente}?access_token=abc":    ErrLinkCredential,
		"https://erp.provedor.example/c/{cliente}?api_key=abc":         ErrLinkCredential,
		"https://erp.provedor.example/c/{cliente}?cpf=1":               ErrLinkCredential,
		"https://erp.provedor.example/senha/{cliente}":                 ErrLinkCredential,
		"https://erp.provedor.example/c/{cliente}?u=x&client_secret=y": ErrLinkCredential,
	}
	for raw, want := range bad {
		if _, err := ValidateCustomerTemplate(raw); !errors.Is(err, want) {
			t.Fatalf("%s: %v, esperado %v", raw, err, want)
		}
	}
}

func TestCustomerURLEscapesAndNeverCarriesADocument(t *testing.T) {
	path := "https://erp.provedor.example/clientes/{cliente}/ficha"
	query := "https://erp.provedor.example/cadastro?id={cliente}&aba=geral"
	cases := []struct{ template, id, want string }{
		{path, "fake-customer-casa", "https://erp.provedor.example/clientes/fake-customer-casa/ficha"},
		{path, "a/b?c", "https://erp.provedor.example/clientes/a%2Fb%3Fc/ficha"},
		{query, "a&b=c", "https://erp.provedor.example/cadastro?id=a%26b%3Dc&aba=geral"},
		{path, "123.456.789-09", ""},
		{path, "12345678909", ""},
		{path, "12.345.678/0001-95", ""},
		{path, "4521", "https://erp.provedor.example/clientes/4521/ficha"},
		{"", "fake-customer-casa", ""},
		{"https://erp.provedor.example/c/{cliente}?token=x", "fake", ""},
	}
	for _, c := range cases {
		if got := CustomerURL(c.template, c.id); got != c.want {
			t.Fatalf("%q %q: %q, esperado %q", c.template, c.id, got, c.want)
		}
	}
}

func TestCustomerLinkSettingsAdminVersionAndIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tenant, other := uuid.New(), uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'UTC','active'),($2,'UTC','active')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	panel := NewPanel(PanelOptions{DB: store})
	boss := Principal{TenantID: tenant, UserID: uuid.New(), Membership: uuid.New(), Role: "admin"}
	agent := boss
	agent.Role = "agent"
	if _, err := panel.CustomerLink(ctx, agent); !errors.Is(err, ErrDenied) {
		t.Fatalf("atendente leu: %v", err)
	}
	if _, err := panel.SetCustomerLink(ctx, agent, "https://erp.provedor.example/c/{cliente}", 0); !errors.Is(err, ErrDenied) {
		t.Fatalf("atendente gravou: %v", err)
	}
	got, err := panel.CustomerLink(ctx, boss)
	if err != nil || got.Template != "" || got.Version != 0 {
		t.Fatalf("vazio: %+v %v", got, err)
	}
	if _, err := panel.SetCustomerLink(ctx, boss, "https://erp.provedor.example/c/{cliente}?token=x", 0); !errors.Is(err, ErrLinkCredential) {
		t.Fatalf("credencial aceita: %v", err)
	}
	saved, err := panel.SetCustomerLink(ctx, boss, " https://erp.provedor.example/c/{cliente} ", 0)
	if err != nil || saved.Version != 1 || saved.Template != "https://erp.provedor.example/c/{cliente}" {
		t.Fatalf("gravar: %+v %v", saved, err)
	}
	if _, err := panel.SetCustomerLink(ctx, boss, "https://erp.provedor.example/d/{cliente}", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("versão velha aceita: %v", err)
	}
	updated, err := panel.SetCustomerLink(ctx, boss, "https://erp.provedor.example/d/{cliente}", 1)
	if err != nil || updated.Version != 2 {
		t.Fatalf("atualizar: %+v %v", updated, err)
	}
	foreign := boss
	foreign.TenantID = other
	if got, err := panel.CustomerLink(ctx, foreign); err != nil || got.Template != "" {
		t.Fatalf("outro tenant viu: %+v %v", got, err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO erp_customer_link_settings(tenant_id,url_template) VALUES($1,'http://x/{cliente}')`, other); err == nil {
		t.Fatal("banco aceitou http")
	}
	if cleared, err := panel.SetCustomerLink(ctx, boss, "", 2); err != nil || cleared.Version != 0 {
		t.Fatalf("limpar: %+v %v", cleared, err)
	}
	if got, err := panel.CustomerLink(ctx, boss); err != nil || got.Template != "" || got.Version != 0 {
		t.Fatalf("depois de limpar: %+v %v", got, err)
	}
}
