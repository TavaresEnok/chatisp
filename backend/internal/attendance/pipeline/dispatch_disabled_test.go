package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Without simulation and without the official authorization, a send fails
// before any network call with driver_indisponivel. No fake stands in for Meta
// and the attempt is final, not uncertain.
func TestDispatchFailsClearlyWhenMetaDispatchIsDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "disabled-dispatch-app", "disabled-dispatch-waba", "5511988880767", "disabled-token", "disabled-secret")
	attempt, _ := createTestAttempt(t, st, "sem autorização de envio", "disabled-dispatch")
	factory, err := NewDriverFactory(DriverFactoryConfig{GraphVersion: "v99.0", PrimaryDocURL: "https://developers.facebook.com/docs/whatsapp/cloud-api/messages"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)
	if err := handler.DispatchOutbound(ctx, st.tenantID, attempt); err != nil {
		t.Fatalf("despacho: %v", err)
	}
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	var state, code, message string
	if err := admin.QueryRow(ctx, `SELECT state, coalesce(error_code,''), coalesce(error_message,'') FROM outbound_attempts WHERE id=$1`, attempt).Scan(&state, &code, &message); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "driver_indisponivel" || message != ErrDispatchDisabled.Error() {
		t.Fatalf("tentativa: state=%s code=%q message=%q", state, code, message)
	}
}
