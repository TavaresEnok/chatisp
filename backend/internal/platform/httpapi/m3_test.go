package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"chatisp/internal/contactdirectory"
	"chatisp/internal/notifications"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/mail"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestM3RegisteredRoleMatrix(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	origin := "https://painel.chatisp.test"
	api := New(Dependencies{Auth: panelService(t, &mail.Fake{}), Sectors: sectors.Service{DB: panelStore}, Contacts: contactdirectory.Service{DB: panelStore, Visibility: ContactVisibility{}}, Notifications: notifications.New(panelStore), TrustedOrigins: []string{origin}})
	for _, role := range []string{"admin", "supervisor", "agent"} {
		member := seedMembership(t, tenant, role, "senha-correta-10")
		cookie, csrf, _ := panelLogin(t, nil, member.email, "senha-correta-10")
		for _, path := range []string{"/api/panel/sectors", "/api/panel/contacts", "/api/panel/preferences/notifications"} {
			w := panelCall(api.Handler, http.MethodGet, path, "198.51.100.40", nil, cookie, "", origin, "")
			if w.Code != http.StatusOK {
				t.Fatalf("%s %s: %d %s", role, path, w.Code, w.Body.String())
			}
		}
		w := panelCall(api.Handler, http.MethodPost, "/api/panel/sectors", "198.51.100.40", map[string]string{"name": "Teste " + role}, cookie, csrf, origin, "")
		want := http.StatusForbidden
		if role == "admin" {
			want = http.StatusCreated
		}
		if w.Code != want {
			t.Fatalf("create %s: %d %s", role, w.Code, w.Body.String())
		}
	}
}

func TestM3SectorMutationContracts(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin := seedMembership(t, tenant, "admin", "senha-correta-10")
	supervisor := seedMembership(t, tenant, "supervisor", "senha-correta-10")
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	sector, connector, defaultConnector, box, defaultBox := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'Teste')`, tenant, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3),($1,$4,'meta_cloud',$5)`, tenant, connector, connector.String(), defaultConnector, defaultConnector.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$4,'A'),($1,$3,$5,'B')`, tenant, box, defaultBox, connector, defaultConnector)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	origin := "https://painel.chatisp.test"
	api := New(Dependencies{Auth: panelService(t, &mail.Fake{}), Sectors: sectors.Service{DB: panelStore}, TrustedOrigins: []string{origin}})
	base := "/api/panel/sectors/" + sector.String()
	memberPath := base + "/memberships/" + agent.membership.String()
	inboxPath := base + "/inboxes/" + box.String()
	defaultPath := base + "/inboxes/" + defaultBox.String()
	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, base, map[string]any{"name": "Atualizado"}},
		{http.MethodPut, memberPath, nil}, {http.MethodDelete, memberPath, nil},
		{http.MethodPut, inboxPath, nil}, {http.MethodDelete, inboxPath, nil},
		{http.MethodPut, defaultPath + "/default", nil},
	}
	for _, email := range []string{supervisor.email, agent.email} {
		cookie, csrf, _ := panelLogin(t, nil, email, "senha-correta-10")
		for _, tc := range cases {
			w := panelCall(api.Handler, tc.method, tc.path, "198.51.100.40", tc.body, cookie, csrf, origin, "")
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s %s %s: %d %s", email, tc.method, tc.path, w.Code, w.Body.String())
			}
		}
	}
	cookie, csrf, _ := panelLogin(t, nil, admin.email, "senha-correta-10")
	for _, tc := range cases {
		if tc.path == defaultPath+"/default" {
			w := panelCall(api.Handler, http.MethodPut, defaultPath, "198.51.100.40", nil, cookie, csrf, origin, "")
			if w.Code != http.StatusOK {
				t.Fatalf("bind default inbox: %d %s", w.Code, w.Body.String())
			}
		}
		w := panelCall(api.Handler, tc.method, tc.path, "198.51.100.40", tc.body, cookie, csrf, origin, "")
		if w.Code != http.StatusOK {
			t.Fatalf("admin %s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestM3SectorMutationPathParameters(t *testing.T) {
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	base := "/api/panel/sectors/{sector_id}"
	for _, tc := range []struct {
		path, method string
		target       bool
	}{
		{base, "patch", false}, {base + "/memberships/{target_id}", "put", true}, {base + "/memberships/{target_id}", "delete", true},
		{base + "/inboxes/{target_id}", "put", true}, {base + "/inboxes/{target_id}", "delete", true}, {base + "/inboxes/{target_id}/default", "put", true},
	} {
		op, ok := spec.Paths[tc.path][tc.method]
		if !ok {
			t.Fatalf("missing %s %s", tc.method, tc.path)
		}
		want := []string{"sector_id"}
		if tc.target {
			want = append(want, "target_id")
		}
		for _, name := range want {
			found := false
			for _, p := range op.Parameters {
				if p.Name == name && p.In == "path" && p.Required {
					found = true
				}
			}
			if !found {
				t.Errorf("%s %s: missing required path parameter %s", tc.method, tc.path, name)
			}
		}
	}
}

func TestM3ContactHistoryFollowsSectorPolicy(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	cookie, _, _ := panelLogin(t, nil, agent.email, "senha-correta-10")
	sectorA, sectorB, connectorA, connectorB, boxA, boxB, contact, hidden, identityA, identityB, identityHidden, convA, convB, convHidden := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3),($1,$4,'meta_cloud',$5)`, []any{tenant, connectorA, connectorA.String(), connectorB, connectorB.String()}},
			{`INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$3,'A'),($1,$4,$5,'B')`, []any{tenant, boxA, connectorA, boxB, connectorB}},
			{`INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'A'),($1,$3,'B')`, []any{tenant, sectorA, sectorB}},
			{`INSERT INTO sector_inboxes VALUES ($1,$2,$3),($1,$4,$5)`, []any{tenant, sectorA, boxA, sectorB, boxB}},
			{`INSERT INTO sector_memberships VALUES ($1,$2,$3)`, []any{tenant, sectorA, agent.membership}},
			{`INSERT INTO contacts (tenant_id,id,name) VALUES ($1,$2,'Compartilhado'),($1,$3,'Oculto')`, []any{tenant, contact, hidden}},
			{`INSERT INTO contact_channel_identities (tenant_id,id,contact_id,connector_id,external_id) VALUES ($1,$2,$3,$4,'a'),($1,$5,$3,$6,'b'),($1,$7,$8,$6,'c')`, []any{tenant, identityA, contact, connectorA, identityB, connectorB, identityHidden, hidden}},
			{`INSERT INTO conversations (tenant_id,id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES ($1,$2,$3,$4,$5,$6,'A','202601',1),($1,$7,$8,$9,$5,$10,'B','202601',1),($1,$11,$8,$9,$12,$13,'C','202601',1)`, []any{tenant, convA, boxA, sectorA, contact, identityA, convB, boxB, sectorB, identityB, convHidden, hidden, identityHidden}},
		}
		for _, s := range statements {
			if _, err := tx.Exec(ctx, s.q, s.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	api := New(Dependencies{Auth: panelService(t, &mail.Fake{}), Contacts: contactdirectory.Service{DB: panelStore, Visibility: ContactVisibility{}}})
	w := panelCall(api.Handler, http.MethodGet, "/api/panel/contacts/"+contact.String()+"/history", "198.51.100.40", nil, cookie, "", "", "")
	var page contactdirectory.HistoryPage
	if w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != convA {
		t.Fatalf("history: %+v", page.Items)
	}
	w = panelCall(api.Handler, http.MethodGet, "/api/panel/contacts/"+hidden.String(), "198.51.100.40", nil, cookie, "", "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("hidden: %d %s", w.Code, w.Body.String())
	}
}

func TestM3ManualContactVisibility(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	owner := seedMembership(t, tenant, "agent", "senha-correta-10")
	supervisor := seedMembership(t, tenant, "supervisor", "senha-correta-10")
	admin := seedMembership(t, tenant, "admin", "senha-correta-10")
	contact := uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contacts (tenant_id,id,name,created_by_membership_id) VALUES ($1,$2,'Manual privado',$3)`, tenant, contact, owner.membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	origin := "https://painel.chatisp.test"
	api := New(Dependencies{Auth: panelService(t, &mail.Fake{}), Contacts: contactdirectory.Service{DB: panelStore, Visibility: ContactVisibility{}}, TrustedOrigins: []string{origin}})
	cookie, csrf, _ := panelLogin(t, nil, supervisor.email, "senha-correta-10")
	path := "/api/panel/contacts/" + contact.String()
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, path, nil},
		{http.MethodPut, path, map[string]any{"name": "Alterado"}},
		{http.MethodPut, path + "/block", map[string]any{"blocked": true, "reason": "teste"}},
	} {
		w := panelCall(api.Handler, tc.method, tc.path, "198.51.100.40", tc.body, cookie, csrf, origin, "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("supervisor %s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	w := panelCall(api.Handler, http.MethodGet, "/api/panel/contacts", "198.51.100.40", nil, cookie, "", origin, "")
	var page contactdirectory.Page
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 0 {
		t.Fatalf("supervisor list: %d %s", w.Code, w.Body.String())
	}
	for _, email := range []string{owner.email, admin.email} {
		cookie, _, _ := panelLogin(t, nil, email, "senha-correta-10")
		w := panelCall(api.Handler, http.MethodGet, path, "198.51.100.40", nil, cookie, "", origin, "")
		if w.Code != http.StatusOK {
			t.Fatalf("authorized contact: %d %s", w.Code, w.Body.String())
		}
	}
}
