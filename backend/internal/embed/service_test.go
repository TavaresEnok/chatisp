package embed

import (
	"context"
	"errors"
	"testing"

	"chatisp/internal/sectors"
	"github.com/google/uuid"
)

func admin() Principal {
	return Principal{TenantID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), Role: "admin"}
}

func TestSupervisorNaoGrava(t *testing.T) {
	svc := New(nil)
	origin, panel := "https://erp.example", "https://erp.example/painel"
	_, err := svc.Save(context.Background(), Principal{TenantID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), Role: "supervisor"}, Draft{AllowedOrigin: &origin, PanelURL: &panel})
	if !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("supervisor: %v", err)
	}
	_, err = svc.Settings(context.Background(), Principal{TenantID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), Role: "agent"})
	if !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("agent: %v", err)
	}
}

func TestSaveRecusaURLAntesDoBanco(t *testing.T) {
	svc := New(nil)
	origin, internal := "https://erp.example", "http://127.0.0.1/secreto"
	_, err := svc.Save(context.Background(), admin(), Draft{AllowedOrigin: &origin, PanelURL: &internal})
	if !errors.Is(err, ErrURL) {
		t.Fatalf("interna: %v", err)
	}
	other := "https://outro.example/painel"
	_, err = svc.Save(context.Background(), admin(), Draft{AllowedOrigin: &origin, PanelURL: &other})
	if !errors.Is(err, ErrOrigin) {
		t.Fatalf("origem: %v", err)
	}
}
