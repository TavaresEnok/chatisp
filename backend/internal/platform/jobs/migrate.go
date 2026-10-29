package jobs

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Migrate applies the official River main line in SchemaName and restricts it.
// owner must be a chatisp_owner pool with at least two connections. The role
// is not a superuser and does not bypass row security. chatisp_jobs must
// already exist as a non-superuser role without BYPASSRLS. This function does
// not create login roles and does not open a worker.
func Migrate(ctx context.Context, owner *pgxpool.Pool) error {
	if owner == nil {
		return errors.New("pool do dono indisponível")
	}
	if owner.Config().MaxConns < 2 {
		return errors.New("pool de migração precisa de ao menos duas conexões")
	}
	conn, err := owner.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("falha ao reservar conexão de migração: %w", err)
	}
	defer conn.Release()

	if err := requireOwner(ctx, conn); err != nil {
		return err
	}
	if err := requireRuntimeRoles(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID()); err != nil {
		return fmt.Errorf("falha ao travar migração River: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockID())
	}()

	if _, err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS river`); err != nil {
		return fmt.Errorf("falha ao criar schema river; chatisp_owner precisa de CREATE no banco: %w", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(owner), &rivermigrate.Config{Schema: SchemaName})
	if err != nil {
		return fmt.Errorf("falha ao preparar migração River: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{
		TargetVersion: MigrationVersion,
	}); err != nil {
		return fmt.Errorf("falha ao migrar River: %w", err)
	}
	for _, stmt := range restrictStatements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("falha ao restringir schema river: %w", err)
		}
	}
	return nil
}

func requireOwner(ctx context.Context, conn *pgxpool.Conn) error {
	var name string
	var super, bypass bool
	err := conn.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls
		FROM pg_roles r
		WHERE r.rolname = current_user
	`).Scan(&name, &super, &bypass)
	if err != nil {
		return fmt.Errorf("falha ao identificar papel de migração: %w", err)
	}
	if name != roleOwner || super || bypass {
		return errMigrateIdentity
	}
	return nil
}

func requireRuntimeRoles(ctx context.Context, conn *pgxpool.Conn) error {
	var app, ingress, platform bool
	err := conn.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1),
			EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $2),
			EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $3)
	`, roleApp, roleIngress, rolePlatform).Scan(&app, &ingress, &platform)
	if err != nil {
		return fmt.Errorf("falha ao verificar papéis de enfileiramento: %w", err)
	}
	if !app || !ingress || !platform {
		return errEnqueueRole
	}
	var jobsSuper, jobsBypass bool
	err = conn.QueryRow(ctx, `
		SELECT rolsuper, rolbypassrls
		FROM pg_roles
		WHERE rolname = $1
	`, RoleJobs).Scan(&jobsSuper, &jobsBypass)
	if errors.Is(err, pgx.ErrNoRows) {
		return errJobsRole
	}
	if err != nil {
		return fmt.Errorf("falha ao verificar papel de jobs: %w", err)
	}
	if jobsSuper || jobsBypass {
		return errJobsRole
	}
	return nil
}

func advisoryLockID() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("chatisp:river_migrate"))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

var restrictStatements = []string{
	`REVOKE ALL ON SCHEMA river FROM PUBLIC`,
	`GRANT USAGE ON SCHEMA river TO chatisp_app, chatisp_ingress, chatisp_jobs`,
	`REVOKE ALL ON TABLE river.river_migration FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress, chatisp_jobs`,
	`REVOKE ALL ON TABLE river.river_job FROM PUBLIC, chatisp_platform`,
	`GRANT SELECT, INSERT, UPDATE ON TABLE river.river_job TO chatisp_app, chatisp_ingress`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE river.river_job TO chatisp_jobs`,
	`REVOKE ALL ON TABLE river.river_leader FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE river.river_leader TO chatisp_jobs`,
	`REVOKE ALL ON TABLE river.river_queue FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE river.river_queue TO chatisp_jobs`,
	`REVOKE ALL ON TABLE river.river_notification FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE river.river_notification TO chatisp_jobs`,
	`GRANT USAGE, SELECT ON SEQUENCE river.river_job_id_seq TO chatisp_app, chatisp_ingress`,
	`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA river TO chatisp_jobs`,
	`DO $$
DECLARE
  fn regprocedure;
BEGIN
  FOR fn IN
    SELECT p.oid::regprocedure
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'river'
  LOOP
    EXECUTE format('ALTER FUNCTION %s SET search_path TO pg_catalog', fn);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', fn);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO chatisp_app, chatisp_ingress, chatisp_jobs', fn);
  END LOOP;
END $$`,
	`CREATE OR REPLACE FUNCTION river.chatisp_job_update_guard()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $fn$
BEGIN
  IF current_user IS DISTINCT FROM 'chatisp_jobs' THEN
    RAISE EXCEPTION 'atualização de job recusada' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END;
$fn$`,
	`REVOKE ALL ON FUNCTION river.chatisp_job_update_guard() FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress`,
	`GRANT EXECUTE ON FUNCTION river.chatisp_job_update_guard() TO chatisp_jobs`,
	`DROP TRIGGER IF EXISTS chatisp_job_update_guard ON river.river_job`,
	`CREATE TRIGGER chatisp_job_update_guard
BEFORE UPDATE ON river.river_job
FOR EACH ROW
EXECUTE FUNCTION river.chatisp_job_update_guard()`,
}
