package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOperationalTimeSubtractsWaitingOnce(t *testing.T) {
	loc := mustLoc(t)
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC) // 08:00 in São Paulo
	closed := opened.Add(4 * time.Hour)
	cycle := sampleCycle(opened, &closed)
	member := uuid.New()
	cycle.CloseMembership = member
	facts := Facts{
		Location: loc, Timezone: "America/Sao_Paulo",
		From: time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC),
		Now:  closed, Group: "day", WaitAlertSeconds: 900,
		Cycles: []Cycle{cycle},
		Events: []Event{
			{ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: "assumed", At: opened, Assignee: &member},
			{ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: "wait_started", At: opened.Add(30 * time.Minute)},
			{ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: "wait_ended", At: opened.Add(3*time.Hour + 30*time.Minute)},
		},
		Messages: []Message{
			customer(cycle, opened),
			human(cycle, opened.Add(30*time.Minute), member),
		},
		Names: Names{Sectors: map[uuid.UUID]string{cycle.SectorID: "Suporte"}, Inboxes: map[uuid.UUID]string{cycle.InboxID: "WhatsApp"}, Members: map[uuid.UUID]string{member: "Ana"}},
	}
	got := Compute(facts)
	if got.Cycles != 1 || got.Conversations != 1 || sec(got.CloseTotal) != 4*3600 || sec(got.CloseOperational) != 3600 {
		t.Fatalf("8h–12h com 3h de espera = 4h e 1h, obteve total %d operacional %d ciclos %d", sec(got.CloseTotal), sec(got.CloseOperational), got.Cycles)
	}
	if sec(got.FirstResponse) != 1800 || got.FirstResponse.NotApplicable != 0 || got.FirstResponse.Sample != 1 {
		t.Fatalf("primeira resposta %+v", got.FirstResponse)
	}
	if !strings.Contains(got.CSV, "encerramento_total,1,14400,14400,0") || !strings.Contains(got.CSV, "encerramento_operacional,1,3600,3600,0") {
		t.Fatal("csv divergiu da conta")
	}
	if !strings.Contains(got.CSV, "Não é o histórico completo do provedor nem do ERP") {
		t.Fatal("csv sem a ressalva da exportação")
	}
	if len(got.Heatmap) != 1 || got.Heatmap[0].Weekday != int(time.Tuesday) || got.Heatmap[0].Hour != 8 {
		t.Fatalf("calor %+v", got.Heatmap)
	}
	if len(got.Attendants) != 1 || got.Attendants[0].ResponsibilitySeconds != 4*3600 || got.Attendants[0].Closures != 1 || got.Attendants[0].Label != "Ana" {
		t.Fatalf("atendente %+v", got.Attendants)
	}
}

func TestOverlapOfWaitAndSnoozeIsDiscountedOnce(t *testing.T) {
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	closed := opened.Add(4 * time.Hour)
	cycle := sampleCycle(opened, &closed)
	got := Compute(Facts{
		Location: time.UTC, From: opened, To: closed.Add(time.Minute), Now: closed,
		Cycles:  []Cycle{cycle},
		Events:  []Event{{ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: "wait_started", At: opened}, {ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: "wait_ended", At: opened.Add(2 * time.Hour)}},
		Snoozes: []Snooze{{ConversationID: cycle.ConversationID, Start: opened.Add(time.Hour), End: opened.Add(3 * time.Hour)}},
	})
	if sec(got.CloseTotal) != 4*3600 || sec(got.CloseOperational) != 3600 {
		t.Fatalf("união 3h, operacional %d total %d", sec(got.CloseOperational), sec(got.CloseTotal))
	}
}

func TestBotClosureLeavesHumanResponseUnset(t *testing.T) {
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	closed := opened.Add(time.Hour)
	cycle := sampleCycle(opened, &closed)
	cycle.CloseResult = "resolved"
	cycle.CloseAuthorKind = "bot"
	cycle.CloseReason = "menu"
	got := Compute(Facts{
		Location: time.UTC, From: opened.Add(-time.Hour), To: closed.Add(time.Hour), Now: closed,
		Cycles:   []Cycle{cycle},
		Messages: []Message{{ID: uuid.New(), ConversationID: cycle.ConversationID, At: opened.Add(time.Minute), Direction: "outbound", Origin: "bot", Visibility: "public", State: "accepted"}},
	})
	if got.FirstResponse.Sample != 0 || got.FirstResponse.NotApplicable != 1 || got.FirstResponse.AverageSeconds != nil || got.FirstResponse.MedianSeconds != nil {
		t.Fatalf("resposta humana %+v", got.FirstResponse)
	}
	if len(got.Results) != 1 || got.Results[0].Label != "Encerrado pelo bot" || got.Results[0].Count != 1 {
		t.Fatalf("resultado %+v", got.Results)
	}
	if len(got.Reasons) != 1 || got.Reasons[0].Name != "menu" || got.Reasons[0].Overlap {
		t.Fatalf("motivo %+v", got.Reasons)
	}
}

func TestCompanyMessageDoesNotStartTheClock(t *testing.T) {
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	cycle := sampleCycle(opened, nil)
	member := uuid.New()
	got := Compute(Facts{
		Location: time.UTC, From: opened, To: opened.Add(4 * time.Hour), Now: opened.Add(2 * time.Hour),
		Cycles: []Cycle{cycle},
		Messages: []Message{
			human(cycle, opened, member),
			customer(cycle, opened.Add(time.Hour)),
			human(cycle, opened.Add(time.Hour+10*time.Minute), member),
		},
	})
	if sec(got.FirstResponse) != 600 || got.FirstResponse.Sample != 1 {
		t.Fatalf("relógio começou na mensagem da empresa: %+v", got.FirstResponse)
	}
}

func TestTransferSplitsTimeAndKeepsBothSectors(t *testing.T) {
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	closed := opened.Add(4 * time.Hour)
	cycle := sampleCycle(opened, &closed)
	other := uuid.New()
	ana := uuid.New()
	bia := uuid.New()
	cycle.CloseMembership = ana
	transfer := "transferred_sector:" + cycle.SectorID.String() + ":" + other.String() + ":" + ana.String() + ":" + bia.String()
	got := Compute(Facts{
		Location: time.UTC, From: opened, To: closed.Add(time.Minute), Now: closed,
		Names:  Names{Sectors: map[uuid.UUID]string{cycle.SectorID: "A", other: "B"}, Members: map[uuid.UUID]string{ana: "Ana", bia: "Bia"}},
		Cycles: []Cycle{cycle},
		Events: []Event{
			{ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: "assumed", At: opened, Assignee: &ana},
			{ID: uuid.New(), ConversationID: cycle.ConversationID, CycleNumber: 1, Kind: transfer, At: opened.Add(2 * time.Hour), Assignee: &bia},
		},
		Messages: []Message{human(cycle, opened.Add(time.Minute), ana), human(cycle, opened.Add(3*time.Hour), bia)},
	})
	if len(got.Sectors) != 2 {
		t.Fatalf("setores %+v", got.Sectors)
	}
	byName := map[string]SectorRow{}
	for _, row := range got.Sectors {
		byName[row.Name] = row
	}
	if byName["A"].ResponsibilitySeconds != 2*3600 || byName["B"].ResponsibilitySeconds != 2*3600 || byName["A"].Cycles != 1 || byName["B"].Cycles != 1 {
		t.Fatalf("divisão %+v", got.Sectors)
	}
	byAtt := map[string]AttendantRow{}
	for _, row := range got.Attendants {
		byAtt[row.Label] = row
	}
	if byAtt["Ana"].ResponsibilitySeconds != 2*3600 || byAtt["Ana"].Messages != 1 || byAtt["Ana"].Closures != 1 {
		t.Fatalf("ana %+v", byAtt["Ana"])
	}
	if byAtt["Bia"].ResponsibilitySeconds != 2*3600 || byAtt["Bia"].Messages != 1 || byAtt["Bia"].Closures != 0 {
		t.Fatalf("bia %+v", byAtt["Bia"])
	}
}

func TestTagsDoNotEnterThePrimaryTotal(t *testing.T) {
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	closed := opened.Add(time.Hour)
	cycle := sampleCycle(opened, &closed)
	cycle.CloseReason = "lentidão"
	got := Compute(Facts{
		Location: time.UTC, From: opened, To: closed.Add(time.Minute), Now: closed,
		Cycles: []Cycle{cycle},
		Tags:   []Tag{{ConversationID: cycle.ConversationID, Name: "urgente"}, {ConversationID: cycle.ConversationID, Name: "lentidão"}},
	})
	if len(got.Reasons) != 1 || got.Reasons[0].Count != 1 || got.Reasons[0].Overlap {
		t.Fatalf("motivo %+v", got.Reasons)
	}
	if len(got.Tags) != 2 || !got.Tags[0].Overlap {
		t.Fatalf("etiquetas %+v", got.Tags)
	}
	var tagSum int
	for _, tag := range got.Tags {
		tagSum += tag.Count
	}
	if tagSum == got.Reasons[0].Count && len(got.Tags) == 1 {
		t.Fatal("etiqueta foi tratada como motivo único")
	}
}

func TestMedianIgnoresMissingHumanResponse(t *testing.T) {
	opened := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	a := sampleCycle(opened, ptr(opened.Add(time.Hour)))
	b := sampleCycle(opened.Add(time.Minute), ptr(opened.Add(2*time.Hour)))
	b.CloseAuthorKind = "bot"
	b.CloseResult = "resolved"
	member := uuid.New()
	got := Compute(Facts{
		Location: time.UTC, From: opened.Add(-time.Hour), To: opened.Add(5 * time.Hour), Now: opened.Add(5 * time.Hour),
		Cycles: []Cycle{a, b},
		Messages: []Message{
			customer(a, a.Opened),
			human(a, a.Opened.Add(10*time.Second), member),
			{ID: uuid.New(), ConversationID: b.ConversationID, At: b.Opened, Direction: "outbound", Origin: "menu", Visibility: "public", State: "accepted"},
		},
	})
	if got.FirstResponse.Sample != 1 || got.FirstResponse.NotApplicable != 1 || sec(got.FirstResponse) != 10 {
		t.Fatalf("denominador %+v", got.FirstResponse)
	}
}

func TestUrgentQueueDoesNotRaiseTheWaitAlert(t *testing.T) {
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	sector := uuid.New()
	normal := Queue{ConversationID: uuid.New(), SectorID: sector, Start: now.Add(-20 * time.Minute), Priority: "normal"}
	urgent := Queue{ConversationID: uuid.New(), SectorID: sector, Start: now.Add(-2 * time.Hour), Priority: "urgent"}
	got := Compute(Facts{
		Location: time.UTC, From: now.Add(-time.Hour), To: now, Now: now, WaitAlertSeconds: 900,
		OpenQueues: []Queue{normal, urgent},
		LiveConversations: []LiveConversation{
			{ID: normal.ConversationID, Status: "open", SectorID: sector},
			{ID: urgent.ConversationID, Status: "open", SectorID: sector, Priority: "urgent"},
		},
	})
	if got.Live.InQueue != 2 || got.Live.Unassigned != 2 || got.Live.WaitAlerts != 1 || got.Live.LongestQueueWaitSeconds != 2*3600 {
		t.Fatalf("ao vivo %+v", got.Live)
	}
}

func TestWindowRejectsMoreThanAYear(t *testing.T) {
	loc := mustLoc(t)
	if _, _, err := Window("2026-01-01", "2027-01-02", loc); err != ErrInvalid {
		t.Fatal("janela longa aceita")
	}
	start, end, err := Window("2026-11-03", "2026-11-03", loc)
	if err != nil || !end.After(start) || end.Sub(start) < 23*time.Hour {
		t.Fatalf("dia %v %v %v", start, end, err)
	}
	if _, _, err := Window("2026-11-04", "2026-11-03", loc); err != ErrInvalid {
		t.Fatal("intervalo invertido aceito")
	}
}

func sampleCycle(opened time.Time, closed *time.Time) Cycle {
	return Cycle{
		ConversationID: uuid.New(), CycleNumber: 1, InboxID: uuid.New(), SectorID: uuid.New(),
		Opened: opened, Closed: closed, CloseReason: "lentidão", CloseResult: "resolved", CloseAuthorKind: "human", CloseMembership: uuid.New(),
	}
}

func customer(cycle Cycle, at time.Time) Message {
	return Message{ID: uuid.New(), ConversationID: cycle.ConversationID, InboxID: cycle.InboxID, At: at, Direction: "inbound", Origin: "customer", Visibility: "public", State: "received"}
}

func human(cycle Cycle, at time.Time, member uuid.UUID) Message {
	return Message{ID: uuid.New(), ConversationID: cycle.ConversationID, InboxID: cycle.InboxID, At: at, Direction: "outbound", Origin: "human", Visibility: "public", State: "accepted", Author: member}
}

func sec(stats Stats) int64 {
	if stats.AverageSeconds == nil {
		return -1
	}
	return *stats.AverageSeconds
}

func ptr(t time.Time) *time.Time { return &t }

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
