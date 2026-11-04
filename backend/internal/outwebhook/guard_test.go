package outwebhook

import (
	"testing"

	"chatisp/internal/platform/urlguard"
)

func TestAllowDestinationRefusesPrivateOutsideTest(t *testing.T) {
	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "production"} {
		if err := AllowDestination(policy, mode, "http://10.1.2.3/hook"); err == nil {
			t.Fatalf("modo %q aceitou IP privado", mode)
		}
		if err := AllowDestination(policy, mode, "http://127.0.0.1:9/hook"); err == nil {
			t.Fatalf("modo %q aceitou loopback", mode)
		}
	}
	if err := AllowDestination(policy, "test", "http://127.0.0.1:9/hook"); err != nil {
		t.Fatal(err)
	}
	if err := AllowDestination(policy, "development", "http://127.0.0.1/hook"); err != nil {
		t.Fatal(err)
	}
	if err := AllowDestination(policy, "test", "http://10.1.2.3/hook"); err == nil {
		t.Fatal("modo de teste aceitou IP privado")
	}
	if err := AllowDestination(policy, "test", "http://user:secret@127.0.0.1/hook"); err == nil {
		t.Fatal("modo de teste aceitou credencial na URL")
	}
}
