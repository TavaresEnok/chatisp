package httpapi

import (
	"errors"
	"testing"

	"chatisp/internal/attendance/service"
	"github.com/danielgtaylor/huma/v2"
)

func TestPolicyDeniedIsUnprocessable(t *testing.T) {
	err := problem(&service.PolicyDenied{Reason: "window_closed"})
	var status huma.StatusError
	if !errors.As(err, &status) || status.GetStatus() != 422 {
		t.Fatalf("política: %v", err)
	}
}
