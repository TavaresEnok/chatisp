package reports

import (
	"time"

	"github.com/google/uuid"
)

const (
	// MaxDays is the longest civil window accepted in one report.
	MaxDays = 366
	// DefaultWaitAlertSeconds matches distribution when the tenant has no row.
	DefaultWaitAlertSeconds = 900
)

// Definitions are the sentences shared by the API, the panel and the CSV.
var Definitions = []string{
	"Relógios corridos, inclusive fora do expediente.",
	"A média e a mediana deixam de fora o que não se aplica. Sem resposta humana o tempo não vira zero.",
	"O tempo operacional desconta uma vez a união dos intervalos em que o atendimento aguardava o cliente ou estava adiado.",
	"Ciclos entram pela data de início. Mensagens entram pelo horário da mensagem. O encerramento entra pela data de fechamento.",
	"Setor e atendente usam o intervalo em que atuaram, não só o último nome da conversa.",
	"O motivo principal entra uma vez no total. Etiquetas adicionais se sobrepõem e não fecham essa conta.",
	"Esta exportação descreve o atendimento no chat. Não é o histórico completo do provedor nem do ERP.",
}

// Principal is the panel actor already authenticated by the caller.
type Principal struct {
	TenantID     uuid.UUID
	UserID       uuid.UUID
	MembershipID uuid.UUID
	Role         string
}

// Filter is the civil window in the provider timezone, plus optional scope.
type Filter struct {
	FromDate string
	ToDate   string
	Group    string
	InboxID  uuid.UUID
	SectorID uuid.UUID
	Now      time.Time
}

// Stats is a sample that excludes non-applicable values from the denominator.
type Stats struct {
	Sample         int    `json:"sample"`
	NotApplicable  int    `json:"not_applicable"`
	AverageSeconds *int64 `json:"average_seconds"`
	MedianSeconds  *int64 `json:"median_seconds"`
}

// ResultRow separates a bot closure from a human solution.
type ResultRow struct {
	Result     string `json:"result"`
	AuthorKind string `json:"author_kind"`
	Label      string `json:"label"`
	Count      int    `json:"count"`
}

// SectorRow counts cycles and intervals recorded for that sector.
type SectorRow struct {
	SectorID               uuid.UUID `json:"sector_id"`
	Name                   string    `json:"name"`
	Cycles                 int       `json:"cycles"`
	QueueWaits             int       `json:"queue_waits"`
	QueueWaitMedianSeconds *int64    `json:"queue_wait_median_seconds"`
	ResponsibilitySeconds  int64     `json:"responsibility_seconds"`
}

// AttendantRow credits only the intervals, messages and closures of that person.
type AttendantRow struct {
	MembershipID          uuid.UUID `json:"membership_id"`
	Label                 string    `json:"label"`
	ResponsibilitySeconds int64     `json:"responsibility_seconds"`
	Messages              int       `json:"messages"`
	Closures              int       `json:"closures"`
}

// InboxRow is the volume of one box inside the same definitions.
type InboxRow struct {
	InboxID  uuid.UUID `json:"inbox_id"`
	Name     string    `json:"name"`
	Cycles   int       `json:"cycles"`
	Messages int       `json:"messages"`
}

// ReasonRow is either the single primary reason or an overlapping tag.
type ReasonRow struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Overlap bool   `json:"overlap"`
}

// HeatCell counts cycles opened in that weekday and hour of the provider timezone.
// Weekday follows Go: Sunday is 0.
type HeatCell struct {
	Weekday int `json:"weekday"`
	Hour    int `json:"hour"`
	Cycles  int `json:"cycles"`
}

// Bucket is one day, week (Monday) or month in the provider timezone.
type Bucket struct {
	Key                  string `json:"key"`
	Cycles               int    `json:"cycles"`
	Messages             int    `json:"messages"`
	Closed               int    `json:"closed"`
	FirstResponseSample  int    `json:"first_response_sample"`
	FirstResponseAverage *int64 `json:"first_response_average_seconds"`
	OperationalSample    int    `json:"operational_sample"`
	OperationalAverage   *int64 `json:"operational_average_seconds"`
}

// Live is the current picture, independent of the selected period.
type Live struct {
	InQueue                 int   `json:"in_queue"`
	InAttendance            int   `json:"in_attendance"`
	WaitingCustomer         int   `json:"waiting_customer"`
	Snoozed                 int   `json:"snoozed"`
	Unassigned              int   `json:"unassigned"`
	OnlineAttendants        int   `json:"online_attendants"`
	LongestQueueWaitSeconds int64 `json:"longest_queue_wait_seconds"`
	WaitAlerts              int   `json:"wait_alerts"`
	WaitAlertSeconds        int   `json:"wait_alert_seconds"`
}

// Report is the payload of the panel and the source of the CSV.
type Report struct {
	Timezone         string         `json:"timezone"`
	From             time.Time      `json:"from"`
	To               time.Time      `json:"to"`
	Group            string         `json:"group"`
	Definitions      []string       `json:"definitions"`
	Conversations    int            `json:"conversations"`
	Cycles           int            `json:"cycles"`
	Messages         int            `json:"messages"`
	FirstResponse    Stats          `json:"first_response"`
	CloseTotal       Stats          `json:"close_total"`
	CloseOperational Stats          `json:"close_operational"`
	QueueWait        Stats          `json:"queue_wait"`
	Results          []ResultRow    `json:"results"`
	Sectors          []SectorRow    `json:"sectors"`
	Attendants       []AttendantRow `json:"attendants"`
	Inboxes          []InboxRow     `json:"inboxes"`
	Reasons          []ReasonRow    `json:"reasons"`
	Tags             []ReasonRow    `json:"tags"`
	Heatmap          []HeatCell     `json:"heatmap"`
	Buckets          []Bucket       `json:"buckets"`
	Live             Live           `json:"live"`
	CSV              string         `json:"csv"`
}

// Cycle is one persisted attendance cycle.
type Cycle struct {
	ConversationID  uuid.UUID
	CycleNumber     int
	InboxID         uuid.UUID
	SectorID        uuid.UUID
	Opened          time.Time
	Closed          *time.Time
	CloseReason     string
	CloseResult     string
	CloseAuthorKind string
	CloseMembership uuid.UUID
	TriageReason    string
}

// Event is one timeline row used to rebuild waits and responsibility.
type Event struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	CycleNumber    int
	Kind           string
	At             time.Time
	Assignee       *uuid.UUID
}

// Message is one persisted message. At is the trusted time, or ingestion.
type Message struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	InboxID        uuid.UUID
	At             time.Time
	Direction      string
	Origin         string
	Visibility     string
	State          string
	Author         uuid.UUID
	Historical     bool
}

// Snooze is one postponement. End is the resolution, or Now when still active.
type Snooze struct {
	ConversationID uuid.UUID
	Start          time.Time
	End            time.Time
}

// Queue is one sector queue interval. A nil End is still open.
type Queue struct {
	ConversationID uuid.UUID
	SectorID       uuid.UUID
	Start          time.Time
	End            *time.Time
	Priority       string
}

// Tag is an additional label. It is not the primary reason.
type Tag struct {
	ConversationID uuid.UUID
	Name           string
}

// LiveConversation is an attendance that is not closed right now.
type LiveConversation struct {
	ID       uuid.UUID
	Status   string
	Assignee uuid.UUID
	SectorID uuid.UUID
	Snoozed  bool
	Priority string
}

// Names are display strings already authorized for this principal.
type Names struct {
	Sectors map[uuid.UUID]string
	Inboxes map[uuid.UUID]string
	Members map[uuid.UUID]string
}

// Facts is everything Compute needs. SectorAllow nil means every sector.
type Facts struct {
	Location          *time.Location
	Timezone          string
	From              time.Time
	To                time.Time
	Now               time.Time
	Group             string
	WaitAlertSeconds  int
	SectorAllow       map[uuid.UUID]bool
	Cycles            []Cycle
	Events            []Event
	Messages          []Message
	Snoozes           []Snooze
	Queues            []Queue
	Tags              []Tag
	LiveConversations []LiveConversation
	OpenQueues        []Queue
	OnlineAttendants  int
	Names             Names
}
