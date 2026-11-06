// Package loadlocal describes the disposable local drill and the measurements
// taken from the process that runs it. It does not open a database and it does
// not accept a remote target.
package loadlocal

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// Clients and RequestsPerClient are defaults for the local drill.
	Clients              = 8
	RequestsPerClient    = 25
	MaxClients           = 32
	MaxRequestsPerClient = 100
	// Acceptance marks this envelope as a local synthetic drill.
	// It is not the D2-07 pilot profile and it does not authorize a capacity number.
	Acceptance = "local-sintetico"
)

// Profile bounds the cost of a synthetic local run. These limits are safety
// limits for the tool, not capacity claims.
type Profile struct {
	Clients           int
	RequestsPerClient int
}

func ProfileFromEnv(getenv func(string) string) (Profile, error) {
	p := Profile{Clients: Clients, RequestsPerClient: RequestsPerClient}
	for _, item := range []struct {
		key    string
		target *int
		max    int
	}{
		{"CHATISP_LOAD_CLIENTS", &p.Clients, MaxClients},
		{"CHATISP_LOAD_REQUESTS_PER_CLIENT", &p.RequestsPerClient, MaxRequestsPerClient},
	} {
		value := getenv(item.key)
		if value == "" {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > item.max {
			return Profile{}, fmt.Errorf("%s deve ser inteiro entre 1 e %d", item.key, item.max)
		}
		*item.target = n
	}
	return p, nil
}

// Routes are the only paths the drill calls, on the listener it started.
var Routes = []string{"/healthz", "/readyz"}

// RefuseExternal reports a set database URL or an external load target.
func RefuseExternal(environ []string) error {
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "CHATISP_DATABASE_URL", "CHATISP_TEST_DATABASE_URL", "CHATISP_LOAD_URL":
			return errors.New("ensaio recusado: não usa banco nem URL externos")
		}
	}
	return nil
}

// Percentile returns the nearest-rank sample. p is between 0 and 1.
// An empty sample is zero. The result is a measurement, not a capacity target.
func Percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 || p <= 0 {
		return 0
	}
	if p > 1 {
		p = 1
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(float64(len(ordered))*p+0.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

// ProcessView is the API process around the drill. Database I/O is not included.
type ProcessView struct {
	RSSBytes   int64
	ReadBytes  int64
	WriteBytes int64
	CPUTicks   int64
}

// ReadProcess reads /proc status, io and stat text already loaded by the caller.
func ReadProcess(status, ioText, stat string) (ProcessView, error) {
	rss, err := rssBytes(status)
	if err != nil {
		return ProcessView{}, err
	}
	readBytes, writeBytes, err := ioBytes(ioText)
	if err != nil {
		return ProcessView{}, err
	}
	ticks, err := cpuTicks(stat)
	if err != nil {
		return ProcessView{}, err
	}
	return ProcessView{RSSBytes: rss, ReadBytes: readBytes, WriteBytes: writeBytes, CPUTicks: ticks}, nil
}

// ReadSelf reads the current process. It does not read another host.
func ReadSelf() (ProcessView, error) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return ProcessView{}, err
	}
	ioText, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return ProcessView{}, err
	}
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return ProcessView{}, err
	}
	return ReadProcess(string(status), string(ioText), string(stat))
}

func rssBytes(status string) (int64, error) {
	for _, line := range strings.Split(status, "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, errors.New("VmRSS ilegível")
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb < 0 {
			return 0, errors.New("VmRSS ilegível")
		}
		return kb * 1024, nil
	}
	return 0, errors.New("VmRSS ausente")
}

func ioBytes(text string) (int64, int64, error) {
	var readBytes int64
	var writeBytes int64
	var sawRead bool
	var sawWrite bool
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "read_bytes" && key != "write_bytes" {
			continue
		}
		number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || number < 0 {
			return 0, 0, errors.New("contador de E/S ilegível")
		}
		switch key {
		case "read_bytes":
			readBytes = number
			sawRead = true
		case "write_bytes":
			writeBytes = number
			sawWrite = true
		}
	}
	if !sawRead || !sawWrite {
		return 0, 0, errors.New("contador de E/S ausente")
	}
	return readBytes, writeBytes, nil
}

func cpuTicks(stat string) (int64, error) {
	end := strings.LastIndex(stat, ")")
	if end < 0 || end+2 >= len(stat) {
		return 0, errors.New("stat ilegível")
	}
	fields := strings.Fields(stat[end+2:])
	// After the comm field, utime is the 12th remaining field and stime the 13th
	// (proc fields 14 and 15, counting from 1).
	if len(fields) < 13 {
		return 0, errors.New("stat ilegível")
	}
	utime, err := strconv.ParseInt(fields[11], 10, 64)
	if err != nil || utime < 0 {
		return 0, errors.New("stat ilegível")
	}
	stime, err := strconv.ParseInt(fields[12], 10, 64)
	if err != nil || stime < 0 {
		return 0, errors.New("stat ilegível")
	}
	return utime + stime, nil
}

// Report is the local drill result. AcceptedAsCapacity stays false.
type Report struct {
	Acceptance         string  `json:"aceite"`
	AcceptedAsCapacity bool    `json:"aceito_como_capacidade"`
	Requests           int     `json:"pedidos"`
	Failures           int     `json:"falhas"`
	P95Millis          float64 `json:"p95_ms"`
	RSSBytes           int64   `json:"rss_bytes"`
	ReadBytes          int64   `json:"io_leitura_bytes"`
	WriteBytes         int64   `json:"io_escrita_bytes"`
	CPUTicks           int64   `json:"cpu_ticks"`
	Note               string  `json:"nota"`
}

// Note explains why the numbers are not pilot capacity.
func Note() string {
	return "ensaio local sintético de um processo em loopback; não é perfil D2-07, não é k6 e não é capacidade de piloto"
}

// Format returns one JSON object without a remote address.
func Format(report Report) string {
	return fmt.Sprintf(
		`{"aceite":%q,"aceito_como_capacidade":false,"pedidos":%d,"falhas":%d,"p95_ms":%.3f,"rss_bytes":%d,"io_leitura_bytes":%d,"io_escrita_bytes":%d,"cpu_ticks":%d,"nota":%q}`,
		Acceptance, report.Requests, report.Failures, report.P95Millis, report.RSSBytes, report.ReadBytes, report.WriteBytes, report.CPUTicks, Note(),
	)
}
