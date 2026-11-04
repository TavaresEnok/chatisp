package restoredrill

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

const dumpFormat = "format postgres-binary-copy"

var identRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// writeDump stores a logical PostgreSQL dump of public tables.
// The embedded test bundle ships postgres, initdb and pg_ctl only, so the
// drill uses the server COPY binary format instead of an absent pg_dump.
func writeDump(ctx context.Context, conn *pgx.Conn, dir string) error {
	tables, err := listTables(ctx, conn)
	if err != nil {
		return err
	}
	lines := []string{dumpFormat}
	for _, table := range tables {
		cols, err := tableColumns(ctx, conn, table)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		sql := "COPY public." + table + " (" + strings.Join(cols, ", ") + ") TO STDOUT WITH (FORMAT binary)"
		if _, err := conn.PgConn().CopyTo(ctx, &buf, sql); err != nil {
			return fmt.Errorf("falha ao gravar o dump da tabela %s: %s", table, pgMessage(err))
		}
		if buf.Len() < 19 {
			return fmt.Errorf("falha ao gravar o dump da tabela %s", table)
		}
		path, err := confinedChild(dir, table+".copybin")
		if err != nil {
			return err
		}
		if err := writeReadOnly(path, buf.Bytes()); err != nil {
			return err
		}
		lines = append(lines, table+" "+strings.Join(cols, " "))
	}
	manifestPath, err := confinedChild(dir, "manifest.txt")
	if err != nil {
		return err
	}
	return writeReadOnly(manifestPath, []byte(strings.Join(lines, "\n")+"\n"))
}

func restoreDump(ctx context.Context, conn *pgx.Conn, dir string) error {
	manifest, err := readDumpPayload(dir, "manifest.txt")
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(manifest))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() || scanner.Text() != dumpFormat {
		return errors.New("falha ao ler o dump")
	}
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		return errors.New("falha ao preparar a restauração")
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SET session_replication_role = DEFAULT`)
	}()
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			return errors.New("falha ao ler o dump")
		}
		table, cols := fields[0], fields[1:]
		if err := validIdents(append([]string{table}, cols...)); err != nil {
			return err
		}
		payload, err := readDumpPayload(dir, table+".copybin")
		if err != nil {
			return err
		}
		sql := "COPY public." + table + " (" + strings.Join(cols, ", ") + ") FROM STDIN WITH (FORMAT binary)"
		if _, err := conn.PgConn().CopyFrom(ctx, bytes.NewReader(payload), sql); err != nil {
			return fmt.Errorf("falha ao restaurar a tabela %s: %s", table, pgMessage(err))
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("falha ao ler o dump")
	}
	return nil
}

func listTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT c.relname
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relkind = 'r'
		  AND c.relname <> 'goose_db_version'
		ORDER BY c.relname
	`)
	if err != nil {
		return nil, errors.New("falha ao listar as tabelas do dump")
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, errors.New("falha ao listar as tabelas do dump")
		}
		if err := validIdents([]string{name}); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("falha ao listar as tabelas do dump")
	}
	if len(tables) == 0 {
		return nil, errors.New("falha ao listar as tabelas do dump")
	}
	return tables, nil
}

func tableColumns(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	if err := validIdents([]string{table}); err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, `
		SELECT a.attname
		FROM pg_attribute AS a
		WHERE a.attrelid = ('public.' || $1)::regclass
		  AND a.attnum > 0
		  AND NOT a.attisdropped
		  AND a.attgenerated = ''
		ORDER BY a.attnum
	`, table)
	if err != nil {
		return nil, fmt.Errorf("falha ao ler as colunas de %s", table)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("falha ao ler as colunas de %s", table)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil || len(cols) == 0 {
		return nil, fmt.Errorf("falha ao ler as colunas de %s", table)
	}
	if err := validIdents(cols); err != nil {
		return nil, err
	}
	return cols, nil
}

func readDumpPayload(dir, name string) ([]byte, error) {
	path, err := confinedChild(dir, name)
	if err != nil {
		return nil, errors.New("falha ao ler o dump")
	}
	body, err := readRegular(path, maxDumpFileBytes)
	if err != nil {
		return nil, errors.New("falha ao ler o dump")
	}
	return body, nil
}

func validIdents(names []string) error {
	for _, name := range names {
		if !identRE.MatchString(name) {
			return errors.New("falha ao gravar o dump")
		}
	}
	return nil
}

func writeReadOnly(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return errors.New("falha ao gravar o dump")
	}
	defer file.Close()
	if _, err := file.Write(payload); err != nil {
		return errors.New("falha ao gravar o dump")
	}
	if err := file.Sync(); err != nil {
		return errors.New("falha ao gravar o dump")
	}
	return nil
}

func dumpContains(dir string, needle []byte) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, errors.New("falha ao ler o dump")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		payload, err := readDumpPayload(dir, entry.Name())
		if err != nil {
			return false, err
		}
		if bytes.Contains(payload, needle) {
			return true, nil
		}
	}
	return false, nil
}

func dumpWritable(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, errors.New("falha ao ler o dump")
	}
	saw := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false, errors.New("falha ao ler o dump")
		}
		saw = true
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, errors.New("falha ao ler o dump")
		}
		if info.Mode().Perm()&0o222 != 0 {
			return true, nil
		}
		if _, err := readDumpPayload(dir, entry.Name()); err != nil {
			return false, err
		}
	}
	if !saw {
		return false, errors.New("falha ao ler o dump")
	}
	return false, nil
}
