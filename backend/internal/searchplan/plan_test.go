// Package searchplan measures the message full-text search expression used by the
// attendance queue (migration 00016) on synthetic rows. It is a measurement of the
// index expression and predicate on a probe table, not a capacity promise.
package searchplan

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

const rows = 50000

func TestMessageSearchUsesTheGINIndexOnSyntheticRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
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
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// The real index exists after the migrations.
	var def string
	if err := conn.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname='m3_message_search'`).Scan(&def); err != nil {
		t.Fatalf("índice m3_message_search: %v", err)
	}
	// Probe table with the same expression and partial predicate as the real index.
	for _, stmt := range []string{
		`CREATE TABLE probe_messages (id bigserial PRIMARY KEY, conversation_id int NOT NULL, visibility text NOT NULL, body text NOT NULL)`,
		fmt.Sprintf(`INSERT INTO probe_messages (conversation_id, visibility, body)
			SELECT g %% 5000, CASE WHEN g %% 7 = 0 THEN 'internal' ELSE 'public' END,
			       'mensagem de teste número ' || g || ' sobre ' || (ARRAY['pagamento','fatura','sinal','instalação','velocidade','cancelamento'])[1 + g %% 6] || ' do cliente'
			FROM generate_series(1,%d) g`, rows),
		`INSERT INTO probe_messages (conversation_id, visibility, body) VALUES (1, 'public', 'o roteador apresentou lentidão rara')`,
		`CREATE INDEX probe_message_search ON probe_messages USING gin (to_tsvector('portuguese', m3_search_normalize(body))) WHERE visibility='public'`,
		`ANALYZE probe_messages`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%.60s: %v", stmt, err)
		}
	}
	query := `SELECT id FROM probe_messages WHERE visibility='public' AND to_tsvector('portuguese', m3_search_normalize(body)) @@ websearch_to_tsquery('portuguese', m3_search_normalize($1))`
	explain, err := conn.Query(ctx, `EXPLAIN (ANALYZE, FORMAT TEXT) `+query, "lentidão")
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for explain.Next() {
		var line string
		if err := explain.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	explain.Close()
	joined := strings.Join(plan, "\n")
	t.Logf("%d mensagens sintéticas, plano medido:\n%s", rows, joined)
	if !strings.Contains(joined, "probe_message_search") {
		t.Fatalf("a busca não usou o índice GIN:\n%s", joined)
	}
	var found int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM (`+strings.ReplaceAll(query, "$1", "'lentidão'")+`) q`).Scan(&found); err != nil || found != 1 {
		t.Fatalf("resultado da busca com acento: %d %v", found, err)
	}
	// Accent-insensitive: the same word typed without the accent finds the row.
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM (`+strings.ReplaceAll(query, "$1", "'lentidao'")+`) q`).Scan(&found); err != nil || found != 1 {
		t.Fatalf("resultado da busca sem acento: %d %v", found, err)
	}
}
