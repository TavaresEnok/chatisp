package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"chatisp/internal/loadlocal"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

func TestSyntheticLocalLoad(t *testing.T) {
	if err := loadlocal.RefuseExternal(os.Environ()); err != nil {
		t.Fatal(err)
	}
	profile, err := loadlocal.ProfileFromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("perfil sintético: clientes=%d pedidos_por_cliente=%d", profile.Clients, profile.RequestsPerClient)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("PostgreSQL de teste não subiu: %s", err)
	}
	t.Cleanup(env.Close)
	if !env.Embedded {
		t.Fatal("o ensaio só usa o PostgreSQL embutido")
	}

	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSmokeJobsRole(ctx, env.AdminURL); err != nil {
		t.Fatal(err)
	}
	jobsURL, err := withJobsUser(urls.App, "chatisp_jobs", "teste-jobs")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	migrateCfg, err := config.Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=" + urls.Owner,
		"CHATISP_MIGRATIONS_DIR=" + migrations,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, migrateCfg, nil); err != nil {
		t.Fatal(err)
	}
	const (
		email    = "carga.local@example.com"
		password = "senha-correta-10"
	)
	if err := seedSmokeUser(ctx, urls, email, password); err != nil {
		t.Fatal(err)
	}

	httpPort := freePort(t)
	metricsPort := freePort(t)
	origin := "http://127.0.0.1:" + httpPort
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(runCtx, []string{"all"}, []string{
			"CHATISP_ENV=development",
			"CHATISP_HTTP_ADDR=127.0.0.1:" + httpPort,
			"CHATISP_METRICS_ADDR=127.0.0.1:" + metricsPort,
			"CHATISP_APP_DATABASE_URL=" + urls.App,
			"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
			"CHATISP_INGRESS_DATABASE_URL=" + urls.Ingress,
			"CHATISP_JOBS_DATABASE_URL=" + jobsURL,
			"CHATISP_DATA_KEYS=1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=",
			"CHATISP_DATA_KEY_VERSION=1",
			"CHATISP_META_SIMULATION=true",
			"CHATISP_META_VERIFY_TOKEN=carga-verify-token",
			"CHATISP_META_APP_ID=carga-app",
			"CHATISP_MIGRATIONS_DIR=" + migrations,
			"CHATISP_PANEL_ORIGINS=" + origin,
			"CHATISP_LOG_LEVEL=error",
			"CHATISP_SHUTDOWN_TIMEOUT=2s",
		}, io.Discard, io.Discard)
	}()

	base := "http://127.0.0.1:" + httpPort
	client := &http.Client{Timeout: 2 * time.Second}
	waitHealthy(t, ctx, errCh, client, base+"/healthz", "ok")
	waitHealthy(t, ctx, errCh, client, base+"/readyz", "ready")

	body := []byte(`{"email":"` + email + `","password":"` + password + `"}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/panel/session", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		t.Fatalf("login sintético: %d %s", response.StatusCode, payload)
	}

	before, err := loadlocal.ReadSelf()
	if err != nil {
		t.Fatal(err)
	}
	total := profile.Clients * profile.RequestsPerClient
	latencies := make([]time.Duration, total)
	failures := make([]error, total)
	var wg sync.WaitGroup
	for clientID := 0; clientID < profile.Clients; clientID++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			local := &http.Client{Timeout: 2 * time.Second}
			for n := 0; n < profile.RequestsPerClient; n++ {
				index := clientID*profile.RequestsPerClient + n
				route := loadlocal.Routes[index%len(loadlocal.Routes)]
				started := time.Now()
				call, callErr := http.NewRequestWithContext(ctx, http.MethodGet, base+route, nil)
				if callErr != nil {
					failures[index] = callErr
					continue
				}
				reply, callErr := local.Do(call)
				latencies[index] = time.Since(started)
				if callErr != nil {
					failures[index] = callErr
					continue
				}
				_, _ = io.Copy(io.Discard, io.LimitReader(reply.Body, 4096))
				reply.Body.Close()
				if reply.StatusCode != http.StatusOK {
					failures[index] = errStatus(reply.StatusCode)
				}
			}
		}(clientID)
	}
	wg.Wait()
	after, err := loadlocal.ReadSelf()
	if err != nil {
		t.Fatal(err)
	}
	failCount := 0
	okLatencies := make([]time.Duration, 0, total)
	for i, failure := range failures {
		if failure != nil {
			failCount++
			continue
		}
		okLatencies = append(okLatencies, latencies[i])
	}
	report := loadlocal.Report{
		Acceptance:         loadlocal.Acceptance,
		AcceptedAsCapacity: false,
		Requests:           total,
		Failures:           failCount,
		P95Millis:          float64(loadlocal.Percentile(okLatencies, 0.95)) / float64(time.Millisecond),
		RSSBytes:           after.RSSBytes,
		ReadBytes:          delta(after.ReadBytes, before.ReadBytes),
		WriteBytes:         delta(after.WriteBytes, before.WriteBytes),
		CPUTicks:           delta(after.CPUTicks, before.CPUTicks),
	}
	t.Log(loadlocal.Format(report))
	select {
	case err := <-errCh:
		t.Fatalf("modo all encerrou durante o ensaio: %v", err)
	default:
	}
	if failCount != 0 || len(okLatencies) != total {
		t.Fatalf("ensaio local falhou: %d de %d", failCount, total)
	}
	if report.P95Millis <= 0 {
		t.Fatal("p95 não foi medido")
	}
}

func delta(after, before int64) int64 {
	if after < before {
		return 0
	}
	return after - before
}

type statusError int

func (s statusError) Error() string { return http.StatusText(int(s)) }

func errStatus(code int) error { return statusError(code) }
