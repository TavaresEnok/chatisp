package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// CountSample is one closed-set bucket. Name values outside the set collapse
// to other before they become labels.
type CountSample struct {
	Name  string
	Value float64
}

// PairSample is one connector type and status. Both sides are closed sets.
type PairSample struct {
	Left  string
	Right string
	Value float64
}

// OpsSnapshot is a database-wide reading. It has no tenant, message or secret.
type OpsSnapshot struct {
	Uncertain            float64
	UncertainOldestAge   float64
	SendReservationsHeld float64
	StorageExpectedBytes float64
	WebhookPendingAge    float64
	JobReservations      []CountSample
	WebhookItems         []CountSample
	Connectors           []PairSample
}

// EnableOps registers the operational collector without emitting a zero.
// Series stay absent until the first completed read.
func (m *Metrics) EnableOps() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ops != nil {
		return
	}
	collector := newOpsCollector()
	m.registry.MustRegister(collector)
	m.ops = collector
}

// SetOps replaces the last successful snapshot and marks the read ok.
func (m *Metrics) SetOps(snapshot OpsSnapshot) {
	if m == nil {
		return
	}
	m.EnableOps()
	m.ops.succeed(snapshot)
}

// SetOpsCollectError marks the last read failed and keeps the previous snapshot.
func (m *Metrics) SetOpsCollectError() {
	if m == nil {
		return
	}
	m.EnableOps()
	m.ops.fail()
}

var opsJobQueues = []string{"ingress", "inbound", "outbound", "other"}

var opsWebhookStates = []string{"pending", "processed", "quarantined", "other"}

var opsConnectorTypes = []string{"meta_cloud", "evolution", "other"}

var opsConnectorStatuses = []string{"pending", "connected", "degraded", "disconnected", "other"}

func sanitizeOpsQueue(queue string) string {
	switch queue {
	case "ingress", "inbound", "outbound":
		return queue
	default:
		return "other"
	}
}

func sanitizeWebhookState(state string) string {
	switch state {
	case "pending", "processed", "quarantined":
		return state
	default:
		return "other"
	}
}

func sanitizeConnectorType(kind string) string {
	switch kind {
	case "meta_cloud", "evolution":
		return kind
	default:
		return "other"
	}
}

func sanitizeConnectorStatus(status string) string {
	switch status {
	case "pending", "connected", "degraded", "disconnected":
		return status
	default:
		return "other"
	}
}

type opsCollector struct {
	mu           sync.Mutex
	ready        bool
	ok           bool
	have         bool
	errors       float64
	uncertain    float64
	uncertainAge float64
	sendHeld     float64
	storage      float64
	webhookAge   float64
	jobs         map[string]float64
	webhooks     map[string]float64
	connectors   map[[2]string]float64

	okDesc           *prometheus.Desc
	errDesc          *prometheus.Desc
	uncertainDesc    *prometheus.Desc
	uncertainAgeDesc *prometheus.Desc
	sendDesc         *prometheus.Desc
	storageDesc      *prometheus.Desc
	webhookAgeDesc   *prometheus.Desc
	jobsDesc         *prometheus.Desc
	webhookDesc      *prometheus.Desc
	connectorDesc    *prometheus.Desc
}

func newOpsCollector() *opsCollector {
	return &opsCollector{
		okDesc: prometheus.NewDesc(
			"chatisp_ops_collect_ok",
			"1 quando a última leitura operacional concluiu. 0 quando falhou. Ausente antes da primeira leitura concluída.",
			nil, nil,
		),
		errDesc: prometheus.NewDesc(
			"chatisp_ops_collect_errors_total",
			"Leituras operacionais que falharam. Não é quantidade de envios nem de bytes.",
			nil, nil,
		),
		uncertainDesc: prometheus.NewDesc(
			"chatisp_outbound_uncertain",
			"Tentativas de envio ainda no estado uncertain, em todos os provedores deste banco.",
			nil, nil,
		),
		uncertainAgeDesc: prometheus.NewDesc(
			"chatisp_outbound_uncertain_oldest_age_seconds",
			"Segundos desde created_at da tentativa uncertain mais antiga. 0 quando não há tentativa.",
			nil, nil,
		),
		sendDesc: prometheus.NewDesc(
			"chatisp_send_reservations_held",
			"Reservas de envio no estado held, em todos os provedores deste banco.",
			nil, nil,
		),
		storageDesc: prometheus.NewDesc(
			"chatisp_storage_expected_bytes",
			"Soma de expected_bytes dos anexos validados. Não é o uso do volume nem um limite de plano.",
			nil, nil,
		),
		webhookAgeDesc: prometheus.NewDesc(
			"chatisp_webhook_pending_oldest_age_seconds",
			"Segundos desde created_at do item de webhook pending mais antigo. 0 quando não há item pending.",
			nil, nil,
		),
		jobsDesc: prometheus.NewDesc(
			"chatisp_job_reservations",
			"Reservas de trabalho ainda não vencidas, por fila fechada.",
			[]string{"queue"}, nil,
		),
		webhookDesc: prometheus.NewDesc(
			"chatisp_webhook_items",
			"Itens de webhook por estado fechado, sem payload.",
			[]string{"state"}, nil,
		),
		connectorDesc: prometheus.NewDesc(
			"chatisp_connectors",
			"Conectores por tipo e estado fechados, sem origem nem credencial.",
			[]string{"type", "status"}, nil,
		),
	}
}

func (c *opsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.okDesc
	ch <- c.errDesc
	ch <- c.uncertainDesc
	ch <- c.uncertainAgeDesc
	ch <- c.sendDesc
	ch <- c.storageDesc
	ch <- c.webhookAgeDesc
	ch <- c.jobsDesc
	ch <- c.webhookDesc
	ch <- c.connectorDesc
}

func (c *opsCollector) Collect(ch chan<- prometheus.Metric) {
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
	ch <- prometheus.MustNewConstMetric(c.uncertainDesc, prometheus.GaugeValue, c.uncertain)
	ch <- prometheus.MustNewConstMetric(c.uncertainAgeDesc, prometheus.GaugeValue, c.uncertainAge)
	ch <- prometheus.MustNewConstMetric(c.sendDesc, prometheus.GaugeValue, c.sendHeld)
	ch <- prometheus.MustNewConstMetric(c.storageDesc, prometheus.GaugeValue, c.storage)
	ch <- prometheus.MustNewConstMetric(c.webhookAgeDesc, prometheus.GaugeValue, c.webhookAge)
	for _, queue := range opsJobQueues {
		ch <- prometheus.MustNewConstMetric(c.jobsDesc, prometheus.GaugeValue, c.jobs[queue], queue)
	}
	for _, state := range opsWebhookStates {
		ch <- prometheus.MustNewConstMetric(c.webhookDesc, prometheus.GaugeValue, c.webhooks[state], state)
	}
	for _, kind := range opsConnectorTypes {
		for _, status := range opsConnectorStatuses {
			ch <- prometheus.MustNewConstMetric(c.connectorDesc, prometheus.GaugeValue, c.connectors[[2]string{kind, status}], kind, status)
		}
	}
}

func (c *opsCollector) succeed(snapshot OpsSnapshot) {
	jobs := map[string]float64{}
	webhooks := map[string]float64{}
	connectors := map[[2]string]float64{}
	for _, sample := range snapshot.JobReservations {
		if sample.Value <= 0 {
			continue
		}
		jobs[sanitizeOpsQueue(sample.Name)] += sample.Value
	}
	for _, sample := range snapshot.WebhookItems {
		if sample.Value <= 0 {
			continue
		}
		webhooks[sanitizeWebhookState(sample.Name)] += sample.Value
	}
	for _, sample := range snapshot.Connectors {
		if sample.Value <= 0 {
			continue
		}
		key := [2]string{sanitizeConnectorType(sample.Left), sanitizeConnectorStatus(sample.Right)}
		connectors[key] += sample.Value
	}
	uncertainAge := snapshot.UncertainOldestAge
	if snapshot.Uncertain <= 0 || uncertainAge < 0 {
		uncertainAge = 0
	}
	webhookAge := snapshot.WebhookPendingAge
	if webhooks["pending"] <= 0 || webhookAge < 0 {
		webhookAge = 0
	}
	storage := snapshot.StorageExpectedBytes
	if storage < 0 {
		storage = 0
	}
	held := snapshot.SendReservationsHeld
	if held < 0 {
		held = 0
	}
	uncertain := snapshot.Uncertain
	if uncertain < 0 {
		uncertain = 0
	}
	c.mu.Lock()
	c.ready = true
	c.ok = true
	c.have = true
	c.uncertain = uncertain
	c.uncertainAge = uncertainAge
	c.sendHeld = held
	c.storage = storage
	c.webhookAge = webhookAge
	c.jobs = jobs
	c.webhooks = webhooks
	c.connectors = connectors
	c.mu.Unlock()
}

func (c *opsCollector) fail() {
	c.mu.Lock()
	c.ready = true
	c.ok = false
	c.errors++
	c.mu.Unlock()
}
