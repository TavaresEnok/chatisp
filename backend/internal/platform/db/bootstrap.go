// Package db owns pools, migrations and tenant-scoped access.
package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	RoleOwner    = "chatisp_owner"
	RoleApp      = "chatisp_app"
	RolePlatform = "chatisp_platform"
	RoleIngress  = "chatisp_ingress"
)

// RoleSecrets are login passwords for the database roles.
// They stay in memory. Do not log this value.
type RoleSecrets struct {
	Owner    string
	App      string
	Platform string
	Ingress  string
}

// RoleURLs are DSNs for each function. Do not log this value.
type RoleURLs struct {
	Owner    string
	App      string
	Platform string
	Ingress  string
}

// NewRoleSecrets returns independent passwords for the four roles.
func NewRoleSecrets() (RoleSecrets, error) {
	owner, err := randomPassword()
	if err != nil {
		return RoleSecrets{}, err
	}
	app, err := randomPassword()
	if err != nil {
		return RoleSecrets{}, err
	}
	platform, err := randomPassword()
	if err != nil {
		return RoleSecrets{}, err
	}
	ingress, err := randomPassword()
	if err != nil {
		return RoleSecrets{}, err
	}
	return RoleSecrets{Owner: owner, App: app, Platform: platform, Ingress: ingress}, nil
}

// Bootstrap creates or updates the runtime roles. It is safe to call more than once.
// adminURL must be able to create roles. Migrations then run as chatisp_owner.
func Bootstrap(ctx context.Context, adminURL string, secrets RoleSecrets) (RoleURLs, error) {
	if err := secrets.complete(); err != nil {
		return RoleURLs{}, err
	}
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return RoleURLs{}, errors.New("falha ao conectar ao banco para preparar papéis")
	}
	defer conn.Close(ctx)

	roles := []struct {
		name     string
		password string
	}{
		{RoleOwner, secrets.Owner},
		{RoleApp, secrets.App},
		{RolePlatform, secrets.Platform},
		{RoleIngress, secrets.Ingress},
	}
	for _, role := range roles {
		if err := ensureRole(ctx, conn, role.name, role.password); err != nil {
			return RoleURLs{}, err
		}
	}
	if err := grantClusterAccess(ctx, conn); err != nil {
		return RoleURLs{}, err
	}
	urls, err := roleURLs(adminURL, secrets)
	if err != nil {
		return RoleURLs{}, errors.New("falha ao montar as conexões dos papéis")
	}
	return urls, nil
}

func (s RoleSecrets) complete() error {
	if s.Owner == "" || s.App == "" || s.Platform == "" || s.Ingress == "" {
		return errors.New("senhas dos papéis incompletas")
	}
	if s.Owner == s.App || s.Owner == s.Platform || s.Owner == s.Ingress || s.App == s.Platform || s.App == s.Ingress || s.Platform == s.Ingress {
		return errors.New("senhas dos papéis devem ser distintas")
	}
	return nil
}

func ensureRole(ctx context.Context, conn *pgx.Conn, name, password string) error {
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists); err != nil {
		return roleError(err)
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := conn.QueryRow(ctx, `
		SELECT format(
			'%s ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L',
			$1::text,
			$2::text,
			$3::text
		)`, verb, name, password).Scan(&stmt); err != nil {
		return roleError(err)
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return roleError(err)
	}
	return nil
}

func grantClusterAccess(ctx context.Context, conn *pgx.Conn) error {
	statements := []string{
		`SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC', current_database())`,
		`SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_owner, chatisp_app, chatisp_platform, chatisp_ingress', current_database())`,
		`SELECT format('REVOKE ALL ON SCHEMA public FROM PUBLIC')`,
		`SELECT format('GRANT USAGE ON SCHEMA public TO chatisp_owner, chatisp_app, chatisp_platform, chatisp_ingress')`,
		`SELECT format('GRANT CREATE ON SCHEMA public TO chatisp_owner')`,
	}
	for _, query := range statements {
		var stmt string
		if err := conn.QueryRow(ctx, query).Scan(&stmt); err != nil {
			return roleError(err)
		}
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return roleError(err)
		}
	}
	return nil
}

func roleURLs(adminURL string, secrets RoleSecrets) (RoleURLs, error) {
	owner, err := withUser(adminURL, RoleOwner, secrets.Owner)
	if err != nil {
		return RoleURLs{}, err
	}
	app, err := withUser(adminURL, RoleApp, secrets.App)
	if err != nil {
		return RoleURLs{}, err
	}
	platform, err := withUser(adminURL, RolePlatform, secrets.Platform)
	if err != nil {
		return RoleURLs{}, err
	}
	ingress, err := withUser(adminURL, RoleIngress, secrets.Ingress)
	if err != nil {
		return RoleURLs{}, err
	}
	return RoleURLs{Owner: owner, App: app, Platform: platform, Ingress: ingress}, nil
}

func withUser(raw, user, password string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", errors.New("url de banco inválida")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", errors.New("url de banco inválida")
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String(), nil
}

func roleError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("falha ao configurar papéis do banco (%s)", pgErr.Code)
	}
	return errors.New("falha ao configurar papéis do banco")
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("falha ao gerar senha de papel")
	}
	return hex.EncodeToString(buf), nil
}
