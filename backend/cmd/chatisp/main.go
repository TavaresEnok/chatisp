// Command chatisp runs the API, worker, migrations or OpenAPI export.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/migrate"
	"chatisp/internal/platform/runtime"
)

const usageText = `uso: chatisp <modo>

modos:
  api       servidor HTTP e métricas
  worker    worker e métricas
  migrate   aplica migrações SQL com goose
  all       API, worker e métricas
  openapi   grava o OpenAPI em JSON

openapi:
  chatisp openapi [-output caminho]

Execute a partir de backend/ para o caminho padrão api/openapi.json.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "chatisp: %s\n", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, environ []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return errors.New("informe o modo: api, worker, migrate, all ou openapi")
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageText)
		return nil
	case "openapi":
		return exportOpenAPI(args[1:], stdout, stderr)
	case "api", "worker", "migrate", "all":
		cfg, err := config.Load(environ)
		if err != nil {
			return err
		}
		log := logging.New(cfg.LogLevel, stdout)
		if args[0] == "migrate" {
			return migrate.Apply(ctx, cfg, log)
		}
		mode, err := config.ParseMode(args[0])
		if err != nil {
			return err
		}
		var identity *auth.Service
		if mode == config.ModeAPI || mode == config.ModeAll {
			opened, cleanup, err := openPanelAuth(ctx, cfg, log)
			if err != nil {
				return err
			}
			defer cleanup()
			identity = opened
		}
		return runtime.Run(ctx, runtime.Options{
			Mode:           mode,
			Config:         cfg,
			Logger:         log,
			Auth:           identity,
			TrustedOrigins: cfg.PanelOrigins,
		})
	default:
		return errors.New("modo desconhecido: use api, worker, migrate, all ou openapi")
	}
}

func openPanelAuth(ctx context.Context, cfg config.Config, log *slog.Logger) (*auth.Service, func(), error) {
	if cfg.AppDatabaseURL == "" && cfg.PlatformDatabaseURL == "" {
		return nil, func() {}, nil
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{
		App:      cfg.AppDatabaseURL,
		Platform: cfg.PlatformDatabaseURL,
	})
	if err != nil {
		return nil, nil, errors.New("falha ao preparar autenticação")
	}
	identity, err := auth.New(auth.Options{
		Store:  db.NewStore(pools.App, pools.Platform),
		Mail:   mail.NewSender(cfg.SMTPAddr, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom),
		Logger: log,
		Keys:   cfg.KeyRing,
	})
	if err != nil {
		pools.Close()
		return nil, nil, errors.New("falha ao preparar autenticação")
	}
	return identity, pools.Close, nil
}

func exportOpenAPI(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("openapi", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", defaultOpenAPIOutput(), "arquivo de saída")
	flags.Usage = func() {
		fmt.Fprint(stderr, "uso: chatisp openapi [-output caminho]\n")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("argumentos inválidos para openapi")
	}
	if flags.NArg() != 0 {
		return errors.New("argumentos inválidos para openapi")
	}
	body, err := httpapi.OpenAPIJSON()
	if err != nil {
		return errors.New("falha ao gerar OpenAPI")
	}
	if err := writeFileAtomic(*output, body); err != nil {
		return errors.New("falha ao gravar OpenAPI")
	}
	fmt.Fprintf(stdout, "OpenAPI gravado em %s\n", *output)
	return nil
}

func defaultOpenAPIOutput() string {
	if _, err := os.Stat("go.mod"); err == nil {
		return filepath.Join("api", "openapi.json")
	}
	if _, err := os.Stat(filepath.Join("backend", "go.mod")); err == nil {
		return filepath.Join("backend", "api", "openapi.json")
	}
	return filepath.Join("api", "openapi.json")
}

func writeFileAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(dir, ".openapi-*.json")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
