package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
)

const actionPlatformBootstrap = "platform.bootstrap"

// errAlreadyBootstrapped means the installation already has users; the command changes nothing.
var errAlreadyBootstrapped = errors.New("a instalação já tem usuários; nada foi alterado. Novos provedores e administradores são criados pelo painel da plataforma")

// bootstrap creates the first provider and its first administrator on an empty installation.
// The password comes from stdin (first line), never from flags or the environment. With
// -superadmin the account also operates the platform. A second run with the same e-mail is a
// no-op; any other run on a populated installation is refused.
func bootstrap(parent context.Context, args, environ []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	email := flags.String("email", "", "e-mail do primeiro administrador")
	timezone := flags.String("timezone", "America/Sao_Paulo", "fuso IANA do primeiro provedor")
	superadmin := flags.Bool("superadmin", false, "também opera a plataforma (provedores e painel técnico)")
	storageBytes := flags.Int64("storage-bytes", 0, "max_storage_bytes do plano do primeiro provedor (anexos); sem ele os anexos ficam recusados até o superadmin definir")
	if err := flags.Parse(args); err != nil {
		return errors.New("argumentos inválidos para bootstrap")
	}
	if flags.NArg() != 0 || *storageBytes < 0 || *storageBytes >= 1<<62 {
		return errors.New("argumentos inválidos para bootstrap")
	}
	var limits []byte
	if *storageBytes > 0 {
		limits = []byte(fmt.Sprintf(`{"max_storage_bytes":%d}`, *storageBytes))
	}
	normalized := strings.ToLower(strings.TrimSpace(*email))
	if normalized == "" || !strings.Contains(normalized, "@") || len(normalized) > 320 {
		return errors.New("informe -email válido para bootstrap")
	}

	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.AppDatabaseURL) == "" || strings.TrimSpace(cfg.PlatformDatabaseURL) == "" {
		return errors.New("bootstrap exige CHATISP_DATABASE_URL, CHATISP_APP_DATABASE_URL e CHATISP_PLATFORM_DATABASE_URL")
	}

	password, err := readPassword(stdin)
	if err != nil {
		return err
	}
	if !auth.PasswordAcceptable(password) {
		return auth.ErrInvalidPassword
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return auth.ErrInvalidPassword
	}

	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()

	owner, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("falha ao conectar como dono do banco")
	}
	defer owner.Close(ctx)

	// Serializes concurrent runs; released when the session ends.
	if _, err := owner.Exec(ctx, `SELECT pg_advisory_lock(hashtext('chatisp.bootstrap'))`); err != nil {
		return errors.New("falha ao reservar o bootstrap")
	}

	var users int
	var existing *uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT count(*), (SELECT id FROM users WHERE lower(email) = $1) FROM users`, normalized).Scan(&users, &existing); err != nil {
		return errors.New("falha ao consultar usuários")
	}
	if existing == nil && users > 0 {
		return errAlreadyBootstrapped
	}

	pools, err := db.OpenPools(ctx, db.RoleURLs{App: cfg.AppDatabaseURL, Platform: cfg.PlatformDatabaseURL})
	if err != nil {
		return err
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)

	// Step 1, one platform transaction: provider, user and an invited admin membership.
	// An invited membership cannot sign in, so a run interrupted after this step leaves
	// nothing usable and the next run with the same e-mail resumes it.
	var userID, tenantID uuid.UUID
	var status string
	if existing == nil {
		userID, tenantID = uuid.New(), uuid.New()
		err = store.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := store.CreateTenant(ctx, db.NewTenant{ID: tenantID, Timezone: *timezone, Status: "active", Limits: limits}); err != nil {
				return err
			}
			if _, err := store.CreateUser(ctx, db.NewUser{ID: userID, Email: normalized, Status: "active"}); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO memberships (id, tenant_id, user_id, role, status) VALUES ($1, $2, $3, 'admin', 'invited')`, uuid.New(), tenantID, userID)
			return err
		})
		if errors.Is(err, db.ErrInvalidTimezone) {
			return auth.ErrInvalidTimezone
		}
		if err != nil {
			return fmt.Errorf("falha ao criar provedor e usuário: %w", err)
		}
		status = "invited"
	} else {
		userID = *existing
		err = store.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT tenant_id, status FROM memberships WHERE user_id = $1 AND role = 'admin' ORDER BY created_at LIMIT 1`, userID).Scan(&tenantID, &status)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errAlreadyBootstrapped
		}
		if err != nil {
			return fmt.Errorf("falha ao consultar o vínculo: %w", err)
		}
		if status == "active" {
			fmt.Fprintf(stdout, "bootstrap: %s já existe; nada foi alterado\n", normalized)
			return nil
		}
		// Resuming is only for an interrupted first run: a single user on the installation.
		// A pending invitation on a populated installation is accepted through its token.
		if status != "invited" || users != 1 {
			return errAlreadyBootstrapped
		}
	}

	// Step 2: password and, when asked, the platform flag. Both are idempotent.
	if err := store.WithAuth(ctx, func(ctx context.Context) error {
		return store.SetPasswordHash(ctx, userID, hash)
	}); err != nil {
		return fmt.Errorf("falha ao gravar a senha: %w", err)
	}
	if *superadmin {
		tag, err := owner.Exec(ctx, `UPDATE users SET is_superadmin = true WHERE id = $1`, userID)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("falha ao marcar o superadmin")
		}
	}

	// Step 3, one platform transaction: activate the membership and audit.
	err = store.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE memberships SET status = 'active' WHERE tenant_id = $1 AND user_id = $2 AND status = 'invited'`, tenantID, userID); err != nil {
			return err
		}
		target := tenantID.String()
		_, err := store.CreatePlatformAuditLog(ctx, db.NewPlatformAuditLog{
			Actor:      userID.String(),
			Action:     actionPlatformBootstrap,
			TargetType: "tenant",
			TargetID:   &target,
			Result:     "success",
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("falha ao ativar o administrador: %w", err)
	}

	role := "admin"
	if *superadmin {
		role = "admin e superadmin"
	}
	fmt.Fprintf(stdout, "bootstrap: provedor %s criado; %s (%s). O MFA é exigido no primeiro acesso.\n", tenantID, normalized, role)
	return nil
}

func readPassword(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", errors.New("informe a senha pela entrada padrão")
	}
	line, err := bufio.NewReader(io.LimitReader(stdin, 512)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("falha ao ler a senha da entrada padrão")
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("informe a senha pela entrada padrão")
	}
	return line, nil
}
