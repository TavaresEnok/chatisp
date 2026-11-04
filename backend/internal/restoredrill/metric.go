package restoredrill

import (
	"errors"
	"path/filepath"
	"strconv"
	"time"
)

// writeRestoreMetric leaves one gauge beside the disposable dump.
// The series has no tenant, path or secret. It is not a pilot objective.
func writeRestoreMetric(dir string, duration time.Duration) error {
	if dir == "" || duration < 0 {
		return errors.New("duração do ensaio inválida")
	}
	path := filepath.Join(dir, "metrics.prom")
	if filepath.Base(path) != "metrics.prom" {
		return errors.New("arquivo de métrica inválido")
	}
	seconds := strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)
	body := "# HELP chatisp_restore_drill_duration_seconds Duração deste ensaio descartável. Não é meta de piloto.\n" +
		"# TYPE chatisp_restore_drill_duration_seconds gauge\n" +
		"chatisp_restore_drill_duration_seconds " + seconds + "\n"
	return writeExact(path, []byte(body), 0o444)
}
