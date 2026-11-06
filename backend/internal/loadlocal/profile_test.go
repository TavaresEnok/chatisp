package loadlocal

import (
	"strings"
	"testing"
	"time"
)

func TestPercentileUsesNearestRank(t *testing.T) {
	samples := []time.Duration{10, 30, 20, 40, 50}
	if got := Percentile(samples, 0.95); got != 50 {
		t.Fatalf("p95 = %s", got)
	}
	if got := Percentile(nil, 0.95); got != 0 {
		t.Fatalf("vazio = %s", got)
	}
	if samples[0] != 10 {
		t.Fatal("a amostra original foi reordenada")
	}
}

func TestRefuseExternal(t *testing.T) {
	if err := RefuseExternal([]string{"CHATISP_HTTP_ADDR=127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{
		"CHATISP_DATABASE_URL=postgres://db",
		"CHATISP_TEST_DATABASE_URL=postgres://db",
		"CHATISP_LOAD_URL=http://10.0.0.8/",
	} {
		if err := RefuseExternal([]string{entry}); err == nil {
			t.Fatalf("aceitou %s", entry)
		}
	}
	if err := RefuseExternal([]string{"CHATISP_DATABASE_URL="}); err != nil {
		t.Fatal(err)
	}
}

func TestReadProcess(t *testing.T) {
	view, err := ReadProcess(
		"Name:\tchatisp\nVmRSS:\t  12 kB\n",
		"read_bytes: 100\nwrite_bytes: 40\n",
		"1 (chat isp) S 1 1 1 0 -1 0 0 0 0 0 7 8 0 0 20 0 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
	)
	if err != nil {
		t.Fatal(err)
	}
	if view.RSSBytes != 12*1024 || view.ReadBytes != 100 || view.WriteBytes != 40 || view.CPUTicks != 15 {
		t.Fatalf("%+v", view)
	}
}

func TestReadProcessIgnoresUnrelatedIOCounters(t *testing.T) {
	_, _, err := ioBytes("read_bytes: 100\nwrite_bytes: 40\ncancelled_write_bytes: -1\n")
	if err != nil {
		t.Fatalf("contador não utilizado impediu a leitura: %v", err)
	}
}

func TestProfileFromEnv(t *testing.T) {
	values := map[string]string{"CHATISP_LOAD_CLIENTS": "2", "CHATISP_LOAD_REQUESTS_PER_CLIENT": "3"}
	get := func(key string) string { return values[key] }
	p, err := ProfileFromEnv(get)
	if err != nil || p.Clients != 2 || p.RequestsPerClient != 3 {
		t.Fatalf("perfil = %+v, erro = %v", p, err)
	}
	for _, value := range []string{"0", "-1", "33", "x"} {
		values["CHATISP_LOAD_CLIENTS"] = value
		if _, err := ProfileFromEnv(get); err == nil {
			t.Fatalf("aceitou clientes=%q", value)
		}
	}
	values["CHATISP_LOAD_CLIENTS"] = "1"
	values["CHATISP_LOAD_REQUESTS_PER_CLIENT"] = "101"
	if _, err := ProfileFromEnv(get); err == nil {
		t.Fatal("aceitou pedidos acima do limite")
	}
}

func TestFormatKeepsCapacityRejected(t *testing.T) {
	text := Format(Report{Requests: 200, Failures: 0, P95Millis: 1.5, RSSBytes: 10, ReadBytes: 2, WriteBytes: 3, CPUTicks: 4})
	if strings.Contains(text, "http://") || strings.Contains(text, "postgres://") || !strings.Contains(text, `"aceito_como_capacidade":false`) || !strings.Contains(text, `"aceite":"local-sintetico"`) {
		t.Fatalf("relatório: %s", text)
	}
	if !strings.Contains(text, "não é capacidade de piloto") {
		t.Fatalf("nota ausente: %s", text)
	}
}
