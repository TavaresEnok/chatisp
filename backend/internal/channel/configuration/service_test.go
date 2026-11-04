package configuration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
)

const (
	// Synthetic values. They are not Cloud API credentials.
	testToken  = "token-teste-local-aaaa"
	testSecret = "segredo-teste-local-aaaa"
	testMarker = "MARCADOR-ENVELOPE-NAO-EXIBIR"
)

func TestPackageDoesNotAcceptExternalTenant(t *testing.T) {
	manual := reflect.TypeOf(Manual{})
	for i := 0; i < manual.NumField(); i++ {
		if strings.Contains(strings.ToLower(manual.Field(i).Name), "tenant") {
			t.Fatalf("formulário aceita tenant: %s", manual.Field(i).Name)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "set_config") {
			t.Fatalf("%s chama set_config", entry.Name())
		}
	}
	if err := json.Unmarshal([]byte(`{"AccessToken":"`+testToken+`"}`), &Material{}); err == nil {
		t.Fatal("payload externo reidratou material")
	}
	raw, err := json.Marshal(Material{AccessToken: testToken, AppSecret: testSecret})
	if err == nil || strings.Contains(string(raw), testToken) {
		t.Fatalf("json do material: %s %v", raw, err)
	}
	if (Material{AccessToken: testToken}).String() != "material redigido" {
		t.Fatal("string do material revelou segredo")
	}
}

func TestManualConfigurationIsolationVersionAndSecret(t *testing.T) {
	ctx := context.Background()
	ring := testRing(t)
	svc := newService(t, ring)
	var logs bytes.Buffer
	svc.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))

	adminA := seedActor(t, "admin", true, "full")
	agent := seedActor(t, "agent", true, "full")
	bare := seedActor(t, "admin", false, "full")
	enroll := seedActor(t, "admin", true, "enrollment")
	adminB := seedActor(t, "admin", true, "full")

	first := manual("Caixa A1", "app-teste-a1", "waba-teste-a1", "phone-teste-a1", testToken, testSecret)
	if _, err := svc.Configure(ctx, agent.hash, first); !errors.Is(err, ErrForbidden) {
		t.Fatalf("atendente: %v", err)
	}
	if _, err := svc.Configure(ctx, bare.hash, first); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("sem MFA: %v", err)
	}
	if _, err := svc.Configure(ctx, enroll.hash, first); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("cadastro MFA: %v", err)
	}
	if _, err := svc.Configure(ctx, []byte("curto"), first); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("sessão curta: %v", err)
	}

	box1, err := svc.Configure(ctx, adminA.hash, first)
	if err != nil {
		t.Fatal(err)
	}
	if box1.Version != 1 || box1.Status != "connected" || box1.AppID != first.AppID {
		t.Fatalf("caixa 1: %+v", box1)
	}
	assertNoSecret(t, box1, logs.String())

	second := manual("Caixa A2", first.AppID, "waba-teste-a2", "phone-teste-a2", "token-teste-local-bbbb", testSecret)
	box2, err := svc.Configure(ctx, adminA.hash, second)
	if err != nil {
		t.Fatal(err)
	}
	if box2.ConnectorID == box1.ConnectorID || box2.InboxID == box1.InboxID || box2.Version != 1 {
		t.Fatalf("segunda caixa: %+v / %+v", box1, box2)
	}

	foreign := manual("Caixa B", first.AppID, "waba-teste-b1", "phone-teste-b1", "token-teste-local-bbbb", "segredo-teste-local-bbbb")
	if _, err := svc.Configure(ctx, adminB.hash, foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("colisão de app: %v", err)
	}
	foreign.AppID = "app-teste-b1"
	foreign.WABAID = second.WABAID
	if _, err := svc.Configure(ctx, adminB.hash, foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("colisão de WABA: %v", err)
	}
	foreign.WABAID = "waba-teste-b1"
	foreign.PhoneNumberID = first.PhoneNumberID
	if _, err := svc.Configure(ctx, adminB.hash, foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("colisão de número: %v", err)
	}
	own := manual("Caixa B1", "app-teste-b1", "waba-teste-b1", "phone-teste-b1", "token-teste-local-bbbb", "segredo-teste-local-bbbb")
	if _, err := svc.Configure(ctx, adminB.hash, own); err != nil {
		t.Fatal(err)
	}

	crossed := manual("Caixa cruzada", "app-teste-a9", first.WABAID, "phone-teste-a9", testToken, testSecret)
	if _, err := svc.Configure(ctx, adminA.hash, crossed); !errors.Is(err, ErrInvalid) {
		t.Fatalf("WABA de outro app: %v", err)
	}
	relink := first
	relink.WABAID = second.WABAID
	if _, err := svc.Configure(ctx, adminA.hash, relink); !errors.Is(err, ErrInvalid) {
		t.Fatalf("número religado: %v", err)
	}

	statusA, err := svc.Status(ctx, adminA.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(statusA.Boxes) != 2 || statusA.Boxes[0].PhoneNumberID == statusA.Boxes[1].PhoneNumberID {
		t.Fatalf("resumo A: %+v", statusA.Boxes)
	}
	statusB, err := svc.Status(ctx, adminB.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(statusB.Boxes) != 1 || statusB.Boxes[0].PhoneNumberID != own.PhoneNumberID {
		t.Fatalf("resumo B: %+v", statusB.Boxes)
	}
	for _, box := range append(statusA.Boxes, statusB.Boxes...) {
		assertNoSecret(t, box, "")
	}

	server := svc.Server()
	route, err := server.ResolveRoute(ctx, first.AppID, first.PhoneNumberID)
	if err != nil {
		t.Fatal(err)
	}
	if route.TenantID != adminA.tenant || route.ConnectorID != box1.ConnectorID || route.InboxID != box1.InboxID {
		t.Fatalf("rota: %+v", route)
	}
	siblingRoute, err := server.ResolveRoute(ctx, first.AppID, second.PhoneNumberID)
	if err != nil || siblingRoute.ConnectorID != box2.ConnectorID || siblingRoute.TenantID != adminA.tenant {
		t.Fatalf("segunda caixa na mesma app: %+v %v", siblingRoute, err)
	}
	if _, err := server.ResolveRoute(ctx, own.AppID, first.PhoneNumberID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app alheio: %v", err)
	}

	material, err := server.ResolveMaterial(ctx, adminA.tenant, box1.ConnectorID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if material.AccessToken != testToken || material.AppSecret != testSecret || material.Version != 1 {
		t.Fatal("material divergente")
	}
	if strings.Contains(fmt.Sprintf("%s %#v %v", material, material, material.LogValue()), testToken) {
		t.Fatal("formatação do material revelou segredo")
	}

	rotated := first
	rotated.AccessToken = "token-teste-local-cccc"
	box1b, err := svc.Configure(ctx, adminA.hash, rotated)
	if err != nil {
		t.Fatal(err)
	}
	if box1b.Version != 2 || box1b.ConnectorID != box1.ConnectorID {
		t.Fatalf("rotação: %+v", box1b)
	}
	if _, err := server.ResolveMaterial(ctx, adminA.tenant, box1.ConnectorID, 1); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("versão antiga: %v", err)
	}
	fresh, err := server.ResolveMaterial(ctx, adminA.tenant, box1.ConnectorID, 2)
	if err != nil || fresh.AccessToken != rotated.AccessToken {
		t.Fatalf("versão nova: %v", err)
	}
	sibling, err := server.ResolveMaterial(ctx, adminA.tenant, box2.ConnectorID, 1)
	if err != nil || sibling.AccessToken != second.AccessToken {
		t.Fatalf("caixa irmã: %v", err)
	}

	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box1.ConnectorID, 2); err != nil {
		t.Fatal(err)
	}
	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box1.ConnectorID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ResolveMaterial(ctx, adminA.tenant, box1.ConnectorID, 2); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("job anterior à degradação: %v", err)
	}
	if _, err := server.ResolveMaterial(ctx, adminA.tenant, box1.ConnectorID, 3); !errors.Is(err, ErrDegraded) {
		t.Fatalf("versão degradada: %v", err)
	}
	degraded, err := svc.Status(ctx, adminA.hash)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	var alertID uuid.UUID
	for _, box := range degraded.Boxes {
		if box.ConnectorID == box1.ConnectorID {
			seen = box.Status == "degraded" && box.Version == 3
		}
	}
	if !seen || len(degraded.Alerts) != 1 || degraded.Alerts[0].ConnectorID != box1.ConnectorID || degraded.Alerts[0].Result != "open" {
		t.Fatalf("alerta: %+v", degraded)
	}
	alertID = degraded.Alerts[0].ID
	if strings.Contains(fmt.Sprintf("%+v", degraded), rotated.AccessToken) || strings.Contains(logs.String(), rotated.AccessToken) {
		t.Fatal("alerta ou log contém token")
	}
	if err := svc.AcknowledgeAlert(ctx, adminA.hash, alertID); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Status(ctx, adminA.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Alerts) != 0 {
		t.Fatalf("alerta aberto após ciência: %+v", after.Alerts)
	}
	var acknowledged int
	if err := testStore.WithTenantTx(ctx, adminA.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM audit_logs
			WHERE action = 'channel.credential_invalid' AND result = 'acknowledged' AND id = $1
		`, alertID).Scan(&acknowledged)
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledged != 1 {
		t.Fatalf("alerta persistido: %d", acknowledged)
	}

	if err := transplantSecrets(ctx, adminA.tenant, adminB.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ResolveMaterial(ctx, adminB.tenant, statusB.Boxes[0].ConnectorID, 1); !errors.Is(err, ErrSecret) {
		t.Fatalf("segredo transplantado: %v", err)
	}
	still, err := server.ResolveMaterial(ctx, adminA.tenant, box2.ConnectorID, 1)
	if err != nil || still.AccessToken != second.AccessToken {
		t.Fatalf("origem do segredo: %v", err)
	}

	if err := assertConnectorRules(ctx, adminA.tenant, box1.ConnectorID); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureReportsPersistedAppSecretKeyVersion(t *testing.T) {
	ctx := context.Background()
	admin := seedActor(t, "admin", true, "full")
	form := manual("Caixa chave", "app-teste-key1", "waba-teste-key1", "phone-teste-key1", testToken, testSecret)

	firstRing := testRingCurrent(t, "v1", "v1")
	svc := newService(t, firstRing)
	created, err := svc.Configure(ctx, admin.hash, form)
	if err != nil {
		t.Fatal(err)
	}
	if created.AppSecretKeyVersion != "v1" || created.TokenKeyVersion != "v1" {
		t.Fatalf("primeira gravação: %+v", created)
	}

	later := newService(t, testRingCurrent(t, "v2", "v1", "v2"))
	saved, err := later.Configure(ctx, admin.hash, form)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ConnectorID != created.ConnectorID || saved.AppSecretKeyVersion != "v1" || saved.TokenKeyVersion != "v2" {
		t.Fatalf("mesmo segredo deve manter a chave persistida: %+v", saved)
	}
	if version := storedAppSecretVersion(t, admin.tenant); version != "v1" {
		t.Fatalf("ciphertext do app secret: %s", version)
	}

	siblingForm := manual("Caixa chave 2", form.AppID, "waba-teste-key2", "phone-teste-key2", "token-teste-local-dddd", testSecret)
	sibling, err := later.Configure(ctx, admin.hash, siblingForm)
	if err != nil {
		t.Fatal(err)
	}
	if sibling.AppSecretKeyVersion != "v1" || sibling.TokenKeyVersion != "v2" {
		t.Fatalf("segunda caixa copiou a chave corrente: %+v", sibling)
	}

	status, err := later.Status(ctx, admin.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Boxes) != 2 {
		t.Fatalf("resumo: %+v", status.Boxes)
	}
	for _, box := range status.Boxes {
		if box.AppSecretKeyVersion != "v1" {
			t.Fatalf("status diverge do banco: %+v", box)
		}
		if box.TokenKeyVersion != "v2" {
			t.Fatalf("token não foi selado de novo: %+v", box)
		}
	}
	if version := storedAppSecretVersion(t, admin.tenant); version != "v1" {
		t.Fatalf("segunda caixa reescreveu o app secret: %s", version)
	}

	form.AppSecret = "segredo-teste-local-novo1"
	form.AccessToken = "token-teste-local-alterado"
	if _, err := later.Configure(ctx, admin.hash, form); !errors.Is(err, ErrConflict) {
		t.Fatalf("troca unilateral do segredo: %v", err)
	}
	if version := storedAppSecretVersion(t, admin.tenant); version != "v1" {
		t.Fatalf("segredo alterado após conflito: %s", version)
	}
	trusted, err := later.Server().ResolveApp(ctx, form.AppID)
	if err != nil || trusted.AppSecret != testSecret {
		t.Fatalf("segredo original após conflito: %v", err)
	}
	material, err := later.Server().ResolveMaterial(ctx, admin.tenant, created.ConnectorID, saved.Version)
	if err != nil || material.AccessToken != testToken {
		t.Fatalf("token ou versão alterados após conflito: %v", err)
	}

}

func TestIngressAppSecretAndStaleCredentialReport(t *testing.T) {
	ctx := context.Background()
	ring := testRing(t)
	svc := newService(t, ring)
	server := svc.Server()
	adminA := seedActor(t, "admin", true, "full")
	adminB := seedActor(t, "admin", true, "full")

	first := manual("Caixa ingresso", "app-teste-ing1", "waba-teste-ing1", "phone-teste-ing1", testToken, testSecret)
	box, err := svc.Configure(ctx, adminA.hash, first)
	if err != nil {
		t.Fatal(err)
	}
	own := manual("Caixa alheia", "app-teste-ing2", "waba-teste-ing2", "phone-teste-ing2", "token-teste-local-bbbb", "segredo-teste-local-bbbb")
	if _, err := svc.Configure(ctx, adminB.hash, own); err != nil {
		t.Fatal(err)
	}

	trusted, err := server.ResolveApp(ctx, first.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if trusted.AppID != first.AppID || trusted.AppSecret != testSecret {
		t.Fatal("aplicativo confiável divergente")
	}
	if strings.Contains(fmt.Sprintf("%s %#v %v", trusted, trusted, trusted.LogValue()), testSecret) {
		t.Fatal("formatação do aplicativo revelou segredo")
	}
	raw, err := json.Marshal(trusted)
	if err == nil || strings.Contains(string(raw), testSecret) {
		t.Fatalf("json do aplicativo: %s %v", raw, err)
	}
	if _, err := server.ResolveApp(ctx, "app-inexistente"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app inexistente: %v", err)
	}
	if _, err := server.ResolveApp(ctx, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("app vazio: %v", err)
	}

	if _, err := server.ResolveMaterial(ctx, adminA.tenant, box.ConnectorID, 1); err != nil {
		t.Fatal(err)
	}
	rotated := first
	rotated.AccessToken = "token-teste-local-cccc"
	saved, err := svc.Configure(ctx, adminA.hash, rotated)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || saved.Status != "connected" {
		t.Fatalf("versão publicada: %+v", saved)
	}
	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box.ConnectorID, 1); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("falha da versão 1: %v", err)
	}
	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box.ConnectorID, 1); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("falha repetida da versão 1: %v", err)
	}
	held, err := svc.Status(ctx, adminA.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Boxes) != 1 || held.Boxes[0].Version != 2 || held.Boxes[0].Status != "connected" || len(held.Alerts) != 0 {
		t.Fatalf("configuração nova foi degradada: %+v", held)
	}
	var staleAlerts int
	if err := testStore.WithTenantTx(ctx, adminA.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM audit_logs WHERE action = 'channel.credential_invalid'
		`).Scan(&staleAlerts)
	}); err != nil {
		t.Fatal(err)
	}
	if staleAlerts != 0 {
		t.Fatalf("alerta de versão obsoleta: %d", staleAlerts)
	}
	fresh, err := server.ResolveMaterial(ctx, adminA.tenant, box.ConnectorID, 2)
	if err != nil || fresh.AccessToken != rotated.AccessToken || fresh.AppSecret != testSecret {
		t.Fatalf("material da versão 2: %v", err)
	}

	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box.ConnectorID, 2); err != nil {
		t.Fatal(err)
	}
	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box.ConnectorID, 2); err != nil {
		t.Fatal(err)
	}
	if err := server.ReportInvalidCredential(ctx, adminA.tenant, box.ConnectorID, 3); err != nil {
		t.Fatal(err)
	}
	degraded, err := svc.Status(ctx, adminA.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(degraded.Boxes) != 1 || degraded.Boxes[0].Status != "degraded" || degraded.Boxes[0].Version != 3 || len(degraded.Alerts) != 1 {
		t.Fatalf("degradação idempotente: %+v", degraded)
	}
	if strings.Contains(fmt.Sprintf("%+v", degraded), rotated.AccessToken) {
		t.Fatal("alerta contém token")
	}
	during, err := server.ResolveApp(ctx, first.AppID)
	if err != nil || during.AppSecret != testSecret {
		t.Fatalf("canal degradado impede autenticação: %v", err)
	}

	if err := setAppStatus(ctx, adminA.tenant, first.AppID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ResolveApp(ctx, first.AppID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app inativo: %v", err)
	}
	if err := setAppStatus(ctx, adminA.tenant, first.AppID, "active"); err != nil {
		t.Fatal(err)
	}
	restored, err := server.ResolveApp(ctx, first.AppID)
	if err != nil || restored.AppSecret != testSecret {
		t.Fatalf("app reativado: %v", err)
	}

	if err := transplantSecrets(ctx, adminA.tenant, adminB.tenant); err != nil {
		t.Fatal(err)
	}
	_, err = server.ResolveApp(ctx, own.AppID)
	if !errors.Is(err, ErrSecret) {
		t.Fatalf("segredo transplantado: %v", err)
	}
	if strings.Contains(err.Error(), testSecret) || strings.Contains(err.Error(), "segredo-teste-local-bbbb") {
		t.Fatal("erro revela segredo")
	}
	still, err := server.ResolveApp(ctx, first.AppID)
	if err != nil || still.AppSecret != testSecret {
		t.Fatalf("origem do segredo: %v", err)
	}
}

func setAppStatus(ctx context.Context, tenantID uuid.UUID, appID, status string) error {
	return db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE meta_app_registry SET status = $2 WHERE app_id = $1`, appID, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("status do app em %d linhas", tag.RowsAffected())
		}
		return nil
	})
}

func TestPrivilegesQuarantineAndDiscarded(t *testing.T) {
	ctx := context.Background()
	ring := testRing(t)
	svc := newService(t, ring)
	adminA := seedActor(t, "admin", true, "full")
	adminB := seedActor(t, "admin", true, "full")
	boxA, err := svc.Configure(ctx, adminA.hash, manual("Caixa A", "app-teste-q1", "waba-teste-q1", "phone-teste-q1", testToken, testSecret))
	if err != nil {
		t.Fatal(err)
	}
	boxB, err := svc.Configure(ctx, adminB.hash, manual("Caixa B", "app-teste-q1", "waba-teste-q2", "phone-teste-q2", "token-teste-local-dddd", testSecret))
	if err != nil {
		t.Fatal(err)
	}
	if boxB.ConnectorID == boxA.ConnectorID {
		t.Fatal("caixas de tenants diferentes colidiram")
	}

	var deliveryB uuid.UUID
	bodyA := []byte(`{"body":"` + testMarker + `-A"}`)
	bodyB := []byte(`{"body":"` + testMarker + `-B"}`)
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (store.Repo{}).StoreDelivery(ctx, tx, "app-teste-q1", bodyA, "unknown_phone", "5511900000099")
		if err != nil {
			return err
		}
		second, err := (store.Repo{}).StoreDelivery(ctx, tx, "app-teste-q1", bodyB, "unknown_phone", "5511900000088")
		if err != nil {
			return err
		}
		deliveryB = second.ID
		routed, err := (store.Repo{}).StoreDelivery(ctx, tx, "app-teste-q1", []byte(`{"item":"ok-`+testMarker+`"}`), "", "")
		if err != nil {
			return err
		}
		_, err = (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
			TenantID: adminA.tenant, DeliveryID: routed.ID, InboxID: boxA.InboxID, ConnectorID: boxA.ConnectorID,
			PhoneNumberID: "phone-teste-q1", ExternalKey: "item-quarentena", Kind: "message",
			Payload: []byte(`{"payload":"` + testMarker + `"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, adminA.tenant, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE webhook_items SET state = 'quarantined'
			WHERE tenant_id = $1 AND external_key = 'item-quarentena'
		`, adminA.tenant)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("item não atualizado")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	conn := adminConn(t)
	defer conn.Close(ctx)
	var jobA, jobB int64
	if err := conn.QueryRow(ctx, `
		INSERT INTO river.river_job (args, attempt, kind, max_attempts, state, finalized_at, queue)
		VALUES ($1::jsonb, 8, 'chatisp.outbound.dispatch.v1', 8, 'discarded'::river.river_job_state, now(), 'outbound')
		RETURNING id
	`, fmt.Sprintf(`{"v":1,"tenant_id":"%s","attempt_id":"%s","note":"%s"}`, adminA.tenant, uuid.New(), testMarker)).Scan(&jobA); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
		INSERT INTO river.river_job (args, attempt, kind, max_attempts, state, finalized_at, queue)
		VALUES ($1::jsonb, 8, 'chatisp.outbound.dispatch.v1', 8, 'discarded'::river.river_job_state, now(), 'outbound')
		RETURNING id
	`, fmt.Sprintf(`{"v":1,"tenant_id":"%s","attempt_id":"%s"}`, adminB.tenant, uuid.New())).Scan(&jobB); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO river.river_job (args, attempt, kind, max_attempts, state, finalized_at, queue)
		VALUES ('{"v":1,"delivery_id":"`+uuid.NewString()+`","note":"`+testMarker+`"}'::jsonb, 8, 'chatisp.ingress.expand.v1', 8, 'discarded'::river.river_job_state, now(), 'ingress')
	`); err != nil {
		t.Fatal(err)
	}

	view, err := svc.Inspect(ctx, adminA.hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Quarantine) != 0 {
		t.Fatalf("quarentena: %+v", view.Quarantine)
	}
	viewB, err := svc.Inspect(ctx, adminB.hash)
	if err != nil || len(viewB.Quarantine) != 0 || len(viewB.Items) != 0 {
		t.Fatalf("inspeção do segundo tenant: %+v %v", viewB, err)
	}
	if len(view.Items) != 1 || view.Items[0].ExternalKey != "item-quarentena" || view.Items[0].State != "quarantined" {
		t.Fatalf("itens: %+v", view.Items)
	}
	if len(view.Discarded) != 1 || view.Discarded[0].ID != jobA || view.Discarded[0].State != "discarded" {
		t.Fatalf("dlq: %+v", view.Discarded)
	}
	dumped := fmt.Sprintf("%+v", view)
	if strings.Contains(dumped, testMarker) || strings.Contains(dumped, testToken) {
		t.Fatalf("inspeção revelou envelope ou segredo: %s", dumped)
	}
	for _, job := range view.Discarded {
		if job.ID == jobB {
			t.Fatal("job de outro tenant visível")
		}
	}
	for _, item := range view.Quarantine {
		if item.DeliveryID == deliveryB {
			t.Fatal("quarentena de outro tenant visível")
		}
	}

	// Migration 00035 grants chatisp_platform the counting columns only
	// (messages.tenant_id/ingested_at, conversations.tenant_id/status).
	// PostgreSQL allows count(*) with any column privilege, so the denial
	// checked here is the content those grants explicitly leave revoked.
	assertDenied(t, execDenied(testPools.Platform, ctx, `SELECT body FROM messages`))
	assertDenied(t, execDenied(testPools.Platform, ctx, `SELECT contact_id FROM conversations`))
	assertDenied(t, execDenied(testPools.Platform, ctx, `SELECT ciphertext FROM credential_secrets`))
	assertDenied(t, execDenied(testPools.Platform, ctx, `SELECT body FROM ingress_deliveries`))
	assertDenied(t, execDenied(testPools.Ingress, ctx, `SELECT count(*) FROM messages`))
	assertDenied(t, execDenied(testPools.Ingress, ctx, `SELECT ciphertext FROM credential_secrets`))
	assertDenied(t, execDenied(testPools.App, ctx, `SELECT body FROM ingress_deliveries`))
	assertDenied(t, execDenied(testPools.App, ctx, `SELECT ciphertext FROM meta_app_registry_secrets`))
	assertDenied(t, execDenied(testPools.App, ctx, `SELECT app_id FROM meta_app_registry`))

	var platformMessages, ingressSecrets, appEnvelope bool
	if err := conn.QueryRow(ctx, `
		SELECT
			has_table_privilege('chatisp_platform', 'public.messages', 'SELECT'),
			has_table_privilege('chatisp_ingress', 'public.credential_secrets', 'SELECT'),
			has_table_privilege('chatisp_app', 'public.ingress_deliveries', 'SELECT')
	`).Scan(&platformMessages, &ingressSecrets, &appEnvelope); err != nil {
		t.Fatal(err)
	}
	if platformMessages || ingressSecrets || appEnvelope {
		t.Fatalf("privilégios indevidos: platform=%v ingress=%v app=%v", platformMessages, ingressSecrets, appEnvelope)
	}
}

type seeded struct {
	hash   []byte
	tenant uuid.UUID
	user   uuid.UUID
}

func seedActor(t *testing.T, role string, confirm bool, assurance string) seeded {
	t.Helper()
	ctx := context.Background()
	actor := seeded{hash: randomBytes(t, 32), tenant: uuid.New(), user: uuid.New()}
	membership := uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID: actor.tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`),
		}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{
			ID: actor.user, Email: fmt.Sprintf("%s-%s@example.com", role, actor.user), Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, actor.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID: membership, UserID: actor.user, Role: role, Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		if confirm {
			sealed, err := testRing(t).Encrypt([]byte("totp-teste-local-123456"), crypto.Context{
				TenantID: actor.tenant.String(), Entity: "user", EntityID: actor.user.String(), Field: "totp_secret",
			})
			if err != nil {
				return err
			}
			if err := testStore.BeginMFA(ctx, actor.user, actor.tenant, sealed.Ciphertext, sealed.Version); err != nil {
				return err
			}
			ok, err := testStore.ConfirmMFA(ctx, actor.user, now)
			if err != nil || !ok {
				return fmt.Errorf("confirmar MFA: %v", err)
			}
		}
		sessionID := uuid.New()
		if err := testStore.InsertSession(ctx, db.NewSession{
			ID: sessionID, TokenHash: actor.hash, UserID: actor.user, TenantID: actor.tenant, MembershipID: membership,
			CSRFHash: randomBytes(t, 32), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour),
		}); err != nil {
			return err
		}
		if assurance != "full" {
			return testStore.SetSessionAssurance(ctx, sessionID, assurance)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return actor
}

func manual(name, appID, wabaID, phoneID, token, secret string) Manual {
	return Manual{
		InboxName: name, DisplayNumber: "+5511900000001", AppID: appID, WABAID: wabaID,
		PhoneNumberID: phoneID, AccessToken: token, AppSecret: secret,
	}
}

func newService(t *testing.T, ring *crypto.Ring) *Service {
	t.Helper()
	svc, err := New(testStore, testPools.Ingress, ring)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func testRing(t *testing.T) *crypto.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(key), "v1")
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func testRingCurrent(t *testing.T, current string, versions ...string) *crypto.Ring {
	t.Helper()
	lines := make([]string, 0, len(versions))
	for i, version := range versions {
		key := bytes.Repeat([]byte{byte(i + 1)}, 32)
		lines = append(lines, version+":"+base64.StdEncoding.EncodeToString(key))
	}
	ring, err := crypto.Parse(strings.Join(lines, "\n"), current)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func storedAppSecretVersion(t *testing.T, tenantID uuid.UUID) string {
	t.Helper()
	var version string
	err := testStore.WithTenantTx(context.Background(), tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT meta_app_secret_version(id) FROM meta_apps WHERE tenant_id = $1
		`, tenantID).Scan(&version)
	})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

func assertNoSecret(t *testing.T, box Box, extra string) {
	t.Helper()
	dumped := fmt.Sprintf("%+v %s", box, extra)
	if strings.Contains(dumped, testToken) || strings.Contains(dumped, testSecret) || strings.Contains(dumped, "ciphertext") {
		t.Fatalf("resposta contém segredo: %s", dumped)
	}
	if box.TokenKeyVersion == "" || box.AppSecretKeyVersion == "" {
		t.Fatalf("versão de chave ausente: %+v", box)
	}
}

func transplantSecrets(ctx context.Context, from, to uuid.UUID) error {
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `
		UPDATE credential_secrets AS dst
		SET ciphertext = src.ciphertext, aad = src.aad, key_version = src.key_version
		FROM credential_secrets AS src
		WHERE src.tenant_id = $1
		  AND dst.tenant_id = $2
		  AND src.field = 'access_token'
		  AND dst.field = 'access_token'
		  AND src.entity = 'channel_connector'
		  AND dst.entity = 'channel_connector'
	`, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("token transplantado em %d linhas", tag.RowsAffected())
	}
	tag, err = conn.Exec(ctx, `
		UPDATE meta_app_registry_secrets AS dst
		SET ciphertext = src.ciphertext, key_version = src.key_version
		FROM meta_app_registry_secrets AS src, meta_apps AS a, meta_apps AS b
		WHERE a.tenant_id = $1 AND b.tenant_id = $2
		  AND src.registry_id = a.registry_id AND dst.registry_id = b.registry_id
	`, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("app secret transplantado em %d linhas", tag.RowsAffected())
	}
	return nil
}

func assertConnectorRules(ctx context.Context, tenantID, connectorID uuid.UUID) error {
	err := testStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE channel_connectors SET connector_type = 'evolution' WHERE id = $1
		`, connectorID)
		return err
	})
	if pgMessage(err) != "tipo de conector imutável com conexão ativa" {
		return fmt.Errorf("tipo ativo: %v", err)
	}
	err = testStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'evolution', $3, 'pending', true)
		`, uuid.New(), tenantID, "evolution-"+uuid.NewString())
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		return fmt.Errorf("evolution habilitada: %v", err)
	}
	return nil
}

func execDenied(pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, ctx context.Context, query string) error {
	_, err := pool.Exec(ctx, query)
	return err
}

func assertDenied(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("acesso esperado 42501, obtido %v", err)
	}
}

func pgMessage(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Message
	}
	return ""
}
