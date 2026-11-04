package metrics

import (
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolBinding is an already open pgx pool. Stat is in memory and does not dial.
type PoolBinding struct {
	Role string
	Pool *pgxpool.Pool
}

// RiverSample is one queue/state group from river.river_job.
// Queue and state values outside the closed sets collapse to other.
type RiverSample struct {
	Queue            string
	State            string
	Jobs             float64
	OldestAgeSeconds float64
}

// WebSocketOpened counts one socket after the upgrade was accepted.
func (m *Metrics) WebSocketOpened() {
	if m == nil || m.websocket == nil {
		return
	}
	m.websocket.Inc()
}

// WebSocketClosed releases one accepted socket. Refusals never reach it.
func (m *Metrics) WebSocketClosed() {
	if m == nil || m.websocket == nil {
		return
	}
	m.websocket.Dec()
}

// RegisterPools publishes stats for pools this process already opened.
// An empty list does nothing. A second call is ignored.
func (m *Metrics) RegisterPools(bindings []PoolBinding) {
	if m == nil || len(bindings) == 0 {
		return
	}
	kept := make([]PoolBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Pool == nil {
			continue
		}
		kept = append(kept, binding)
	}
	if len(kept) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pools != nil {
		return
	}
	collector := newPoolCollector(kept)
	m.registry.MustRegister(collector)
	m.pools = collector
}

// EnableJobResults registers the worker result counter once.
// Series appear only after a real completion. A nil registry is ignored.
func (m *Metrics) EnableJobResults() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobResults != nil {
		return
	}
	m.jobResults = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "chatisp_river_job_results_total",
		Help: "Execuções concluídas dos workers River, por worker e resultado.",
	}, []string{"worker", "result"})
	m.registry.MustRegister(m.jobResults)
}

// ObserveRiverResult counts one finished worker call.
// Snooze is not an error. A panic is recorded as error by the worker middleware.
// Unknown worker or result collapses to other.
func (m *Metrics) ObserveRiverResult(worker, result string) {
	if m == nil {
		return
	}
	m.EnableJobResults()
	m.jobResults.WithLabelValues(sanitizeWorker(worker), sanitizeResult(result)).Inc()
}

// EnableRiverBacklog registers the backlog collector without emitting a zero.
// The series stay absent until the first completed read.
func (m *Metrics) EnableRiverBacklog() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.river != nil {
		return
	}
	collector := newRiverCollector()
	m.registry.MustRegister(collector)
	m.river = collector
}

// SetRiverBacklog replaces the last successful snapshot and marks the read ok.
func (m *Metrics) SetRiverBacklog(samples []RiverSample) {
	if m == nil {
		return
	}
	m.EnableRiverBacklog()
	m.river.succeed(samples)
}

// SetRiverCollectError marks the last read failed and keeps the previous snapshot.
func (m *Metrics) SetRiverCollectError() {
	if m == nil {
		return
	}
	m.EnableRiverBacklog()
	m.river.fail()
}

func sanitizeWorker(worker string) string {
	switch worker {
	case "ingress", "inbound", "outbound", "media", "webhooks", "automation":
		return worker
	default:
		return "other"
	}
}

func sanitizeResult(result string) string {
	switch result {
	case "success", "error", "cancelled", "snooze":
		return result
	default:
		return "other"
	}
}

func sanitizeRole(role string) string {
	switch role {
	case "app", "platform", "ingress", "jobs":
		return role
	default:
		return "other"
	}
}

func sanitizeQueue(queue string) string {
	switch queue {
	case "ingress", "inbound", "outbound", "inbound_media", "webhooks", "scheduled":
		return queue
	default:
		return "other"
	}
}

func sanitizeState(state string) string {
	switch state {
	case "available", "cancelled", "completed", "discarded", "pending", "retryable", "running", "scheduled":
		return state
	default:
		return "other"
	}
}

var knownQueues = []string{"ingress", "inbound", "outbound", "inbound_media", "webhooks", "scheduled", "other"}

var knownStates = []string{"available", "cancelled", "completed", "discarded", "pending", "retryable", "running", "scheduled", "other"}

type poolSnap struct {
	acquired     float64
	idle         float64
	constructing float64
	max          float64
	acquires     float64
	acquireSecs  float64
	canceled     float64
	newConns     float64
	empty        float64
}

type poolCollector struct {
	bindings     []PoolBinding
	connections  *prometheus.Desc
	maxConns     *prometheus.Desc
	acquires     *prometheus.Desc
	acquireSecs  *prometheus.Desc
	canceled     *prometheus.Desc
	newConns     *prometheus.Desc
	emptyAcquire *prometheus.Desc
}

func newPoolCollector(bindings []PoolBinding) *poolCollector {
	return &poolCollector{
		bindings: bindings,
		connections: prometheus.NewDesc(
			"chatisp_db_pool_connections",
			"Conexões atuais do pool pgx por papel e estado.",
			[]string{"role", "state"},
			nil,
		),
		maxConns: prometheus.NewDesc(
			"chatisp_db_pool_max_connections",
			"Limite configurado de conexões do pool, por papel.",
			[]string{"role"},
			nil,
		),
		acquires: prometheus.NewDesc(
			"chatisp_db_pool_acquires_total",
			"Aquisições bem-sucedidas do pool desde a abertura, por papel.",
			[]string{"role"},
			nil,
		),
		acquireSecs: prometheus.NewDesc(
			"chatisp_db_pool_acquire_seconds_total",
			"Tempo acumulado esperando adquirir conexão, em segundos, por papel.",
			[]string{"role"},
			nil,
		),
		canceled: prometheus.NewDesc(
			"chatisp_db_pool_canceled_acquires_total",
			"Aquisições canceladas pelo contexto desde a abertura, por papel.",
			[]string{"role"},
			nil,
		),
		newConns: prometheus.NewDesc(
			"chatisp_db_pool_new_connections_total",
			"Conexões novas abertas pelo pool desde a criação, por papel.",
			[]string{"role"},
			nil,
		),
		emptyAcquire: prometheus.NewDesc(
			"chatisp_db_pool_empty_acquires_total",
			"Aquisições que esperaram porque o pool estava vazio, por papel.",
			[]string{"role"},
			nil,
		),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connections
	ch <- c.maxConns
	ch <- c.acquires
	ch <- c.acquireSecs
	ch <- c.canceled
	ch <- c.newConns
	ch <- c.emptyAcquire
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	byRole := make(map[string]poolSnap, len(c.bindings))
	for _, binding := range c.bindings {
		if binding.Pool == nil {
			continue
		}
		stat := binding.Pool.Stat()
		if stat == nil {
			continue
		}
		role := sanitizeRole(binding.Role)
		snap := byRole[role]
		snap.acquired += float64(stat.AcquiredConns())
		snap.idle += float64(stat.IdleConns())
		snap.constructing += float64(stat.ConstructingConns())
		snap.max += float64(stat.MaxConns())
		snap.acquires += float64(stat.AcquireCount())
		snap.acquireSecs += stat.AcquireDuration().Seconds()
		snap.canceled += float64(stat.CanceledAcquireCount())
		snap.newConns += float64(stat.NewConnsCount())
		snap.empty += float64(stat.EmptyAcquireCount())
		byRole[role] = snap
	}
	for role, snap := range byRole {
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, snap.acquired, role, "acquired")
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, snap.idle, role, "idle")
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, snap.constructing, role, "constructing")
		ch <- prometheus.MustNewConstMetric(c.maxConns, prometheus.GaugeValue, snap.max, role)
		ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, snap.acquires, role)
		ch <- prometheus.MustNewConstMetric(c.acquireSecs, prometheus.CounterValue, snap.acquireSecs, role)
		ch <- prometheus.MustNewConstMetric(c.canceled, prometheus.CounterValue, snap.canceled, role)
		ch <- prometheus.MustNewConstMetric(c.newConns, prometheus.CounterValue, snap.newConns, role)
		ch <- prometheus.MustNewConstMetric(c.emptyAcquire, prometheus.CounterValue, snap.empty, role)
	}
}

type queueAge struct {
	jobs float64
	age  float64
}

type riverCollector struct {
	mu       sync.Mutex
	ready    bool
	ok       bool
	have     bool
	errors   float64
	samples  map[[2]string]queueAge
	okDesc   *prometheus.Desc
	errDesc  *prometheus.Desc
	jobsDesc *prometheus.Desc
	ageDesc  *prometheus.Desc
}

func newRiverCollector() *riverCollector {
	return &riverCollector{
		okDesc: prometheus.NewDesc(
			"chatisp_river_collect_ok",
			"1 quando a última leitura do backlog River concluiu. 0 quando falhou. Ausente antes da primeira leitura concluída.",
			nil,
			nil,
		),
		errDesc: prometheus.NewDesc(
			"chatisp_river_collect_errors_total",
			"Leituras do backlog River que falharam. Não é o número de jobs.",
			nil,
			nil,
		),
		jobsDesc: prometheus.NewDesc(
			"chatisp_river_jobs",
			"Jobs em river.river_job por fila e estado na última leitura bem-sucedida.",
			[]string{"queue", "state"},
			nil,
		),
		ageDesc: prometheus.NewDesc(
			"chatisp_river_oldest_job_age_seconds",
			"Segundos desde created_at do job mais antigo da fila e do estado. 0 quando não há job.",
			[]string{"queue", "state"},
			nil,
		),
	}
}

func (c *riverCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.okDesc
	ch <- c.errDesc
	ch <- c.jobsDesc
	ch <- c.ageDesc
}

func (c *riverCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return
	}
	ok := 0.0
	if c.ok {
		ok = 1
	}
	ch <- prometheus.MustNewConstMetric(c.okDesc, prometheus.GaugeValue, ok)
	ch <- prometheus.MustNewConstMetric(c.errDesc, prometheus.CounterValue, c.errors)
	if !c.have {
		return
	}
	for _, queue := range knownQueues {
		for _, state := range knownStates {
			sample := c.samples[[2]string{queue, state}]
			ch <- prometheus.MustNewConstMetric(c.jobsDesc, prometheus.GaugeValue, sample.jobs, queue, state)
			ch <- prometheus.MustNewConstMetric(c.ageDesc, prometheus.GaugeValue, sample.age, queue, state)
		}
	}
}

func (c *riverCollector) succeed(samples []RiverSample) {
	aggregated := make(map[[2]string]queueAge, len(knownQueues)*len(knownStates))
	for _, queue := range knownQueues {
		for _, state := range knownStates {
			aggregated[[2]string{queue, state}] = queueAge{}
		}
	}
	for _, sample := range samples {
		if sample.Jobs <= 0 {
			continue
		}
		key := [2]string{sanitizeQueue(sample.Queue), sanitizeState(sample.State)}
		current := aggregated[key]
		current.jobs += sample.Jobs
		age := sample.OldestAgeSeconds
		if age < 0 {
			age = 0
		}
		if age > current.age {
			current.age = age
		}
		aggregated[key] = current
	}
	c.mu.Lock()
	c.ready = true
	c.ok = true
	c.have = true
	c.samples = aggregated
	c.mu.Unlock()
}

func (c *riverCollector) fail() {
	c.mu.Lock()
	c.ready = true
	c.ok = false
	c.errors++
	c.mu.Unlock()
}
