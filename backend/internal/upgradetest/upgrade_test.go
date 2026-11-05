// Package upgradetest proves that the current migrations upgrade a database that
// already holds synthetic data at the previous baseline (migration 00045,
// commit 54febe9) without losing it. It owns a private embedded PostgreSQL.
package upgradetest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

const baselineLast = 45

func TestUpgradeFromBaselineKeepsSyntheticData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("PostgreSQL de teste: %v", err)
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
	full, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}

	// Baseline: only the migrations up to 00045, as shipped before this branch.
	baseline := t.TempDir()
	entries, err := os.ReadDir(full)
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, entry := range entries {
		var number int
		if _, scanErr := parseNumber(entry.Name(), &number); scanErr != nil {
			continue
		}
		if number > baselineLast {
			pending++
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(full, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := os.WriteFile(filepath.Join(baseline, entry.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if pending < 3 {
		t.Fatalf("esperava ao menos 3 migrações depois da base, achei %d", pending)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, baseline); err != nil {
		t.Fatalf("migrações da base: %v", err)
	}

	// Synthetic data written at the baseline schema.
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tenant := uuid.New()
	contact := uuid.New()
	token := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO tenants (id, timezone, status, limits) VALUES ($1, 'America/Sao_Paulo', 'active', $2::jsonb)`, tenant, json.RawMessage(`{"max_agents":2}`)); err != nil {
		t.Fatalf("tenant sintético: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO contacts (tenant_id, id, name, phone, notes) VALUES ($1, $2, 'Cliente Sintético', '+5511999990001', 'nota antiga')`, tenant, contact); err != nil {
		t.Fatalf("contato sintético: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO api_tokens (id, tenant_id, secret_hash, scopes, request_limit) VALUES ($1, $2, decode(repeat('ab', 32), 'hex'), ARRAY['read','contacts.read'], 60)`, token, tenant); err != nil {
		t.Fatalf("token sintético: %v", err)
	}
	// The old schema must not know the new scope or column yet.
	if _, err := admin.Exec(ctx, `INSERT INTO api_tokens (id, tenant_id, secret_hash, scopes, request_limit) VALUES ($1, $2, decode(repeat('cd', 32), 'hex'), ARRAY['conversations.write'], 60)`, uuid.New(), tenant); err == nil {
		t.Fatal("a base já aceitava conversations.write")
	}
	var hasColumn bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='contacts' AND column_name='privacy_erased_at')`).Scan(&hasColumn); err != nil || hasColumn {
		t.Fatalf("a base já tinha privacy_erased_at: %v %v", hasColumn, err)
	}

	// Upgrade with the full set, twice (the second run must be a no-op).
	for run := 1; run <= 2; run++ {
		if err := db.ApplyMigrations(ctx, urls.Owner, full); err != nil {
			t.Fatalf("atualização, execução %d: %v", run, err)
		}
	}

	var version int64
	if err := admin.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version <= baselineLast+2 {
		t.Fatalf("versão final %d: %v", version, err)
	}
	var name, phone, notes string
	var erased *time.Time
	if err := admin.QueryRow(ctx, `SELECT name, phone, notes, privacy_erased_at FROM contacts WHERE tenant_id=$1 AND id=$2`, tenant, contact).Scan(&name, &phone, &notes, &erased); err != nil {
		t.Fatalf("contato depois da atualização: %v", err)
	}
	if name != "Cliente Sintético" || phone != "+5511999990001" || notes != "nota antiga" || erased != nil {
		t.Fatalf("contato mudou: %q %q %q %v", name, phone, notes, erased)
	}
	var scopes []string
	if err := admin.QueryRow(ctx, `SELECT scopes FROM api_tokens WHERE id=$1`, token).Scan(&scopes); err != nil || strings.Join(scopes, ",") != "read,contacts.read" {
		t.Fatalf("token antigo mudou: %v %v", scopes, err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO api_tokens (id, tenant_id, secret_hash, scopes, request_limit) VALUES ($1, $2, decode(repeat('ef', 32), 'hex'), ARRAY['conversations.write'], 60)`, uuid.New(), tenant); err != nil {
		t.Fatalf("o escopo novo deveria valer depois da atualização: %v", err)
	}
	var count int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id=$1`, tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tenant sintético: %d %v", count, err)
	}
}

// parseNumber reads the leading migration number of "00046_name.sql".
func parseNumber(file string, out *int) (int, error) {
	prefix, _, ok := strings.Cut(file, "_")
	if !ok || !strings.HasSuffix(file, ".sql") {
		return 0, os.ErrInvalid
	}
	n := 0
	for _, r := range prefix {
		if r < '0' || r > '9' {
			return 0, os.ErrInvalid
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return n, nil
}
