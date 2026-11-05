package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/testdb"
)

// Production refuses simulation, so a worker that demanded simulation could
// never start. Without simulation, without the official authorization and
// without the webhook verify token, every mode must still start and serve.
func TestProductionStartsWithMetaDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	t.Cleanup(env.Close)
	urls, jobsURL, err := prepareDevDatabase(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"api", "worker", "all"} {
		t.Run(mode, func(t *testing.T) {
			httpPort, metricsPort := freePort(t), freePort(t)
			runCtx, stop := context.WithCancel(ctx)
			defer stop()
			var logs bytes.Buffer
			done := make(chan error, 1)
			go func() {
				done <- run(runCtx, []string{mode}, []string{
					"CHATISP_ENV=production",
					"CHATISP_HTTP_ADDR=127.0.0.1:" + httpPort,
					"CHATISP_METRICS_ADDR=127.0.0.1:" + metricsPort,
					"CHATISP_APP_DATABASE_URL=" + urls.App,
					"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
					"CHATISP_INGRESS_DATABASE_URL=" + urls.Ingress,
					"CHATISP_JOBS_DATABASE_URL=" + jobsURL,
					"CHATISP_DATA_KEYS=1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=",
					"CHATISP_DATA_KEY_VERSION=1",
					"CHATISP_PANEL_ORIGINS=https://painel.exemplo.test",
					"CHATISP_LOG_LEVEL=info",
					"CHATISP_SHUTDOWN_TIMEOUT=2s",
				}, &logs, io.Discard)
			}()
			client := &http.Client{Timeout: 2 * time.Second}
			probe := "http://127.0.0.1:" + metricsPort + "/metrics"
			if mode != "worker" {
				probe = "http://127.0.0.1:" + httpPort + "/readyz"
			}
			deadline := time.Now().Add(30 * time.Second)
			for {
				select {
				case err := <-done:
					t.Fatalf("modo %s encerrou: %v\n%s", mode, err, logs.String())
				default:
				}
				resp, err := client.Get(probe)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("modo %s não ficou pronto\n%s", mode, logs.String())
				}
				time.Sleep(100 * time.Millisecond)
			}
			client.CloseIdleConnections()
			stop()
			if err := <-done; err != nil {
				t.Fatalf("encerramento de %s: %v", mode, err)
			}
			out := logs.String()
			if !strings.Contains(out, `"envio":"disabled"`) || !strings.Contains(out, "envio pela Meta não autorizado") {
				t.Fatalf("estado da integração Meta ausente do log:\n%s", out)
			}
		})
	}
}

func TestMetaDispatchConfiguration(t *testing.T) {
	base := []string{"CHATISP_ENV=production", "CHATISP_DATABASE_URL=postgres://u:p@127.0.0.1/chatisp", "CHATISP_DATA_KEYS=1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=", "CHATISP_DATA_KEY_VERSION=1"}
	doc := "CHATISP_META_PRIMARY_DOC_URL=https://developers.facebook.com/docs/whatsapp/cloud-api"
	cases := []struct {
		name    string
		extra   []string
		mode    string
		invalid string
	}{
		{name: "padrão desabilitado", mode: config.MetaDispatchDisabled},
		{name: "versão preparada sem autorização", extra: []string{"CHATISP_META_GRAPH_VERSION=v99.0", doc}, mode: config.MetaDispatchDisabled},
		{name: "oficial completo", extra: []string{"CHATISP_META_OFFICIAL_DISPATCH=true", "CHATISP_META_GRAPH_VERSION=v99.0", doc}, mode: config.MetaDispatchOfficial},
		{name: "oficial sem versão", extra: []string{"CHATISP_META_OFFICIAL_DISPATCH=true", doc}, invalid: "CHATISP_META_GRAPH_VERSION"},
		{name: "oficial sem documentação", extra: []string{"CHATISP_META_OFFICIAL_DISPATCH=true", "CHATISP_META_GRAPH_VERSION=v99.0"}, invalid: "CHATISP_META_PRIMARY_DOC_URL"},
		{name: "versão malformada", extra: []string{"CHATISP_META_GRAPH_VERSION=23"}, invalid: "CHATISP_META_GRAPH_VERSION"},
		{name: "documentação fora do domínio", extra: []string{"CHATISP_META_PRIMARY_DOC_URL=https://exemplo.test/docs"}, invalid: "CHATISP_META_PRIMARY_DOC_URL"},
		{name: "simulação em produção", extra: []string{"CHATISP_META_SIMULATION=true"}, invalid: "CHATISP_META_SIMULATION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(append(append([]string{}, base...), tc.extra...))
			if tc.invalid != "" {
				if err == nil || !strings.Contains(err.Error(), tc.invalid) {
					t.Fatalf("esperava recusa de %s: %v", tc.invalid, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MetaDispatch() != tc.mode {
				t.Fatalf("modo: %s", cfg.MetaDispatch())
			}
		})
	}
	dev := []string{"CHATISP_ENV=development", "CHATISP_DATABASE_URL=postgres://u:p@127.0.0.1/chatisp", "CHATISP_META_SIMULATION=true", "CHATISP_META_OFFICIAL_DISPATCH=true", "CHATISP_META_GRAPH_VERSION=v99.0", doc}
	if _, err := config.Load(dev); err == nil || !strings.Contains(err.Error(), "CHATISP_META_OFFICIAL_DISPATCH") {
		t.Fatalf("simulação e oficial juntos: %v", err)
	}
}
