package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
)

func TestIdentityTablesStayOnTheAuthPath(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal("falha ao conectar como administrador do teste")
	}
	defer admin.Close(ctx)

	var tables int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relkind = 'r'
		  AND c.relname IN ('sessions', 'auth_tokens', 'auth_attempt_counters')
		  AND pg_get_userbyid(c.relowner) = $1
		  AND NOT c.relrowsecurity
		  AND NOT has_table_privilege('chatisp_app', c.oid, 'SELECT')
		  AND NOT has_table_privilege('chatisp_platform', c.oid, 'SELECT')
		  AND NOT has_table_privilege('chatisp_ingress', c.oid, 'SELECT')
	`, db.RoleOwner).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 3 {
		t.Fatalf("tabelas restritas: %d", tables)
	}

	rows, err := admin.Query(ctx, `
		SELECT p.proname, p.prosecdef, r.rolname, p.proconfig,
		       has_function_privilege('chatisp_app', p.oid, 'EXECUTE'),
		       has_function_privilege('chatisp_platform', p.oid, 'EXECUTE'),
		       has_function_privilege('chatisp_ingress', p.oid, 'EXECUTE')
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		JOIN pg_roles r ON r.oid = p.proowner
		WHERE n.nspname = 'public' AND p.proname LIKE 'auth\_%' ESCAPE '\'
		ORDER BY p.proname
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, owner string
		var secdef, app, platform, ingress bool
		var config []string
		if err := rows.Scan(&name, &secdef, &owner, &config, &app, &platform, &ingress); err != nil {
			t.Fatal(err)
		}
		seen++
		if !secdef || owner != db.RoleOwner || !strings.Contains(strings.Join(config, ","), "search_path=pg_catalog") {
			t.Fatalf("função %s owner=%s secdef=%v config=%v", name, owner, secdef, config)
		}
		if platform || ingress {
			t.Fatalf("%s executável fora do papel da aplicação", name)
		}
		if name == "auth_require_scope" {
			if app {
				t.Fatal("auth_require_scope ficou executável pela aplicação")
			}
			continue
		}
		if !app {
			t.Fatalf("%s sem EXECUTE para chatisp_app", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen < 15 {
		t.Fatalf("funções de autenticação: %d", seen)
	}

	tx, err := testPools.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT auth_login_identity($1)`, "pessoa@example.com"); !isAuthDenied(err) {
		t.Fatalf("função sem escopo: %v", err)
	}
	if _, err := testPools.Platform.Exec(ctx, `SELECT token_hash FROM sessions`); !isPermission(err) {
		t.Fatalf("plataforma leu sessões: %v", err)
	}
}

func isAuthDenied(err error) bool {
	return isPermission(err)
}

func isPermission(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}
