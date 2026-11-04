// Command restoredrill runs the M8 restore drill against a disposable cluster.
// It refuses application databases and any destination outside a throwaway directory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"chatisp/internal/restoredrill"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "restoredrill: %s\n", err)
		os.Exit(1)
	}
}

type drillReport struct {
	Banco                string `json:"banco"`
	Anexos               bool   `json:"anexos"`
	Chaves               bool   `json:"chaves"`
	ConteudoLegivel      bool   `json:"conteudo_legivel"`
	ChaveRecuperada      bool   `json:"chave_recuperada"`
	SegredoDecifrado     bool   `json:"segredo_decifrado"`
	ExclusoesReaplicadas bool   `json:"exclusoes_reaplicadas"`
	DuracaoMS            int64  `json:"duracao_ms"`
	Medida               string `json:"medida"`
	DuracaoNota          string `json:"duracao_nota"`
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("restoredrill", flag.ContinueOnError)
	flags.SetOutput(stderr)
	adminURL := flags.String("url", "", "DSN do superusuário do cluster descartável, por socket Unix e sem senha")
	dumpDir := flags.String("dump", "", "diretório temporário vazio, fora do checkout real, para o dump")
	if err := flags.Parse(args); err != nil {
		return errors.New("argumentos inválidos")
	}
	if flags.NArg() != 0 || *adminURL == "" || *dumpDir == "" {
		return errors.New("uso: restoredrill -url <dsn-socket> -dump <diretório-temporário-vazio>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	report, err := restoredrill.Run(ctx, restoredrill.Options{AdminURL: *adminURL, DumpDir: *dumpDir})
	if err != nil {
		return err
	}
	payload := drillReport{
		Banco:                report.Database,
		Anexos:               report.AttachmentReferenceOK,
		Chaves:               report.KeyReferenceOK,
		ConteudoLegivel:      report.ContentReadable,
		ChaveRecuperada:      report.KeyRecovered,
		SegredoDecifrado:     report.SecretDecrypted,
		ExclusoesReaplicadas: report.DeletionReapplied,
		DuracaoMS:            report.Duration.Milliseconds(),
		Medida:               "rto",
		DuracaoNota:          report.DurationNote,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return errors.New("falha ao gravar o relatório")
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		return errors.New("falha ao gravar o relatório")
	}
	return nil
}
