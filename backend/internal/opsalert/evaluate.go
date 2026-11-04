package opsalert

import (
	"regexp"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// SignalJobAge é um job River ainda aberto, atribuível ao provedor, mais
	// velho que o limite gravado.
	SignalJobAge = "job_age"
	// SignalConnectorDisconnected é um conector com status disconnected.
	SignalConnectorDisconnected = "connector_disconnected"
	// SignalWebhookIncomplete é um item da fila webhook_items ainda pending
	// além do limite gravado.
	SignalWebhookIncomplete = "webhook_incomplete"

	// MinAgeSeconds e MaxAgeSeconds limitam o limiar configurável. Não são
	// capacidade de atendimento.
	MinAgeSeconds = 60
	MaxAgeSeconds = 604800

	// ListLimit é o teto de leitura de cada sinal por atualização do painel.
	ListLimit = 100

	maxLabelRunes = 180
	maxKeyRunes   = 80
)

// Limits são os limiares já gravados. Zero significa que o admin ainda não
// configurou aquele sinal e ele não gera alerta.
type Limits struct {
	JobAge     time.Duration
	WebhookAge time.Duration
}

// JobFact é o que a leitura do River pode entregar ao avaliador. Body existe
// para os testes provarem que o texto não entra no alerta; a consulta não o preenche.
type JobFact struct {
	ID    int64
	Kind  string
	Queue string
	State string
	Age   time.Duration
	Body  string
}

// ConnectorFact é o estado persistido do conector, sem credencial nem número.
type ConnectorFact struct {
	ID     uuid.UUID
	Type   string
	Status string
	Body   string
}

// WebhookFact é um item da fila de webhook, sem payload.
type WebhookFact struct {
	ID    uuid.UUID
	Kind  string
	State string
	Age   time.Duration
	Body  string
}

// Facts reúne as leituras de uma atualização.
type Facts struct {
	Jobs         []JobFact
	Connectors   []ConnectorFact
	Webhooks     []WebhookFact
	WebhookQueue bool
}

// Alert é o registro visível ao admin. Não há campo de mensagem, documento ou segredo.
type Alert struct {
	Signal     string `json:"signal"`
	SubjectKey string `json:"subject_key"`
	Label      string `json:"label"`
	AgeSeconds int64  `json:"age_seconds,omitempty"`
}

var tokenPattern = regexp.MustCompile(`^[a-z0-9._-]{1,40}$`)

var openJobStates = map[string]struct{}{
	"available": {},
	"pending":   {},
	"retryable": {},
	"running":   {},
	"scheduled": {},
}

// Derive monta os alertas abertos. Idade igual ao limite não alerta: o sinal
// exige idade acima do limite. Campos livres que não passam no token ficam de fora.
func Derive(limits Limits, facts Facts) []Alert {
	out := make([]Alert, 0)
	if limits.JobAge > 0 {
		for _, job := range facts.Jobs {
			if _, open := openJobStates[job.State]; !open || job.Age <= limits.JobAge || job.ID <= 0 {
				continue
			}
			alert := Alert{
				Signal:     SignalJobAge,
				SubjectKey: "job:" + strconv.FormatInt(job.ID, 10),
				Label:      "Job antigo na fila " + tokenOr(job.Queue, "desconhecida") + ", tipo " + tokenOr(job.Kind, "desconhecido") + ", estado " + tokenOr(job.State, "aberto"),
				AgeSeconds: int64(job.Age / time.Second),
			}
			if acceptable(alert) {
				out = append(out, alert)
			}
		}
	}
	for _, connector := range facts.Connectors {
		if connector.Status != "disconnected" || connector.ID == uuid.Nil {
			continue
		}
		kind := connector.Type
		if kind != "meta_cloud" && kind != "evolution" {
			kind = "desconhecido"
		}
		alert := Alert{
			Signal:     SignalConnectorDisconnected,
			SubjectKey: "connector:" + connector.ID.String(),
			Label:      "Conector " + kind + " desconectado",
		}
		if acceptable(alert) {
			out = append(out, alert)
		}
	}
	if facts.WebhookQueue && limits.WebhookAge > 0 {
		for _, item := range facts.Webhooks {
			if item.State != "pending" || item.Age <= limits.WebhookAge || item.ID == uuid.Nil {
				continue
			}
			alert := Alert{
				Signal:     SignalWebhookIncomplete,
				SubjectKey: "webhook:" + item.ID.String(),
				Label:      webhookPhrase(item.Kind),
				AgeSeconds: int64(item.Age / time.Second),
			}
			if acceptable(alert) {
				out = append(out, alert)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Signal != out[j].Signal {
			return out[i].Signal < out[j].Signal
		}
		return out[i].SubjectKey < out[j].SubjectKey
	})
	return out
}

// ValidAge informa se um limiar em segundos pode ser gravado.
func ValidAge(seconds int) bool {
	return seconds >= MinAgeSeconds && seconds <= MaxAgeSeconds
}

func webhookPhrase(kind string) string {
	switch kind {
	case "message":
		return "Webhook de mensagem pendente sem conclusão"
	case "status":
		return "Webhook de status pendente sem conclusão"
	default:
		return "Webhook pendente sem conclusão"
	}
}

func tokenOr(value, fallback string) string {
	if tokenPattern.MatchString(value) {
		return value
	}
	return fallback
}

func acceptable(alert Alert) bool {
	switch alert.Signal {
	case SignalJobAge, SignalConnectorDisconnected, SignalWebhookIncomplete:
	default:
		return false
	}
	if alert.SubjectKey == "" || utf8.RuneCountInString(alert.SubjectKey) > maxKeyRunes {
		return false
	}
	if alert.Label == "" || utf8.RuneCountInString(alert.Label) > maxLabelRunes {
		return false
	}
	return true
}
