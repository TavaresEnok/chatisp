package reports

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type span struct {
	ConversationID uuid.UUID
	CycleNumber    int
	SectorID       uuid.UUID
	MembershipID   uuid.UUID
	Start          time.Time
	End            time.Time
}

type window struct {
	Start time.Time
	End   time.Time
}

// Compute builds the report from facts already limited to the caller's scope.
func Compute(in Facts) Report {
	loc := in.Location
	if loc == nil {
		loc = time.UTC
	}
	group := in.Group
	if group == "" {
		group = "day"
	}
	out := Report{
		Timezone:    in.Timezone,
		From:        in.From,
		To:          in.To,
		Group:       group,
		Definitions: append([]string(nil), Definitions...),
		Results:     []ResultRow{},
		Sectors:     []SectorRow{},
		Attendants:  []AttendantRow{},
		Inboxes:     []InboxRow{},
		Reasons:     []ReasonRow{},
		Tags:        []ReasonRow{},
		Heatmap:     []HeatCell{},
		Buckets:     []Bucket{},
	}
	out.Live = livePicture(in)

	cycles := append([]Cycle(nil), in.Cycles...)
	sort.Slice(cycles, func(i, j int) bool {
		if cycles[i].Opened.Equal(cycles[j].Opened) {
			return cycles[i].ConversationID.String() < cycles[j].ConversationID.String()
		}
		return cycles[i].Opened.Before(cycles[j].Opened)
	})
	events := append([]Event(nil), in.Events...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].At.Equal(events[j].At) {
			return events[i].ID.String() < events[j].ID.String()
		}
		return events[i].At.Before(events[j].At)
	})
	messages := append([]Message(nil), in.Messages...)
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].At.Equal(messages[j].At) {
			return messages[i].ID.String() < messages[j].ID.String()
		}
		return messages[i].At.Before(messages[j].At)
	})

	spans := responsibility(cycles, events, in.Now)
	opened := make([]Cycle, 0)
	closed := make([]Cycle, 0)
	conversations := map[uuid.UUID]struct{}{}
	for _, cycle := range cycles {
		if inPeriod(cycle.Opened, in.From, in.To) {
			opened = append(opened, cycle)
			conversations[cycle.ConversationID] = struct{}{}
		}
		if cycle.Closed != nil && inPeriod(*cycle.Closed, in.From, in.To) {
			closed = append(closed, cycle)
		}
	}
	out.Conversations = len(conversations)
	out.Cycles = len(opened)

	var messageCount int
	for _, message := range messages {
		if countsAsVolume(message) && inPeriod(message.At, in.From, in.To) {
			messageCount++
		}
	}
	out.Messages = messageCount

	var first []time.Duration
	var firstNA int
	for _, cycle := range opened {
		if d, ok := firstHuman(cycle, messages, in.Now); ok {
			first = append(first, d)
		} else {
			firstNA++
		}
	}
	out.FirstResponse = summarize(first, firstNA)

	var totals []time.Duration
	var operational []time.Duration
	for _, cycle := range closed {
		limit := *cycle.Closed
		total := limit.Sub(cycle.Opened)
		if total < 0 {
			total = 0
		}
		paused := unionDuration(append(waitWindows(cycle, events, in.Now), snoozeWindows(cycle, in.Snoozes, in.Now)...), cycle.Opened, limit)
		op := total - paused
		if op < 0 {
			op = 0
		}
		totals = append(totals, total)
		operational = append(operational, op)
	}
	out.CloseTotal = summarize(totals, 0)
	out.CloseOperational = summarize(operational, 0)

	var queueDurations []time.Duration
	for _, queue := range in.Queues {
		if queue.End == nil || !inPeriod(*queue.End, in.From, in.To) {
			continue
		}
		if !sectorOK(in.SectorAllow, queue.SectorID) {
			continue
		}
		d := queue.End.Sub(queue.Start)
		if d < 0 {
			continue
		}
		queueDurations = append(queueDurations, d)
	}
	out.QueueWait = summarize(queueDurations, 0)

	out.Results = resultRows(closed)
	out.Sectors = sectorRows(in, opened, spans, in.Queues)
	out.Attendants = attendantRows(in, spans, messages, closed)
	out.Inboxes = inboxRows(in, opened, messages)
	out.Reasons, out.Tags = reasonRows(opened, in.Tags)
	out.Heatmap = heatCells(opened, loc)
	out.Buckets = buckets(in, opened, closed, messages, events, loc, group)
	out.CSV = renderCSV(out)
	return out
}

func livePicture(in Facts) Live {
	limit := in.WaitAlertSeconds
	if limit < 0 {
		limit = DefaultWaitAlertSeconds
	}
	live := Live{OnlineAttendants: in.OnlineAttendants, WaitAlertSeconds: limit}
	for _, row := range in.LiveConversations {
		if !sectorOK(in.SectorAllow, row.SectorID) {
			continue
		}
		switch {
		case row.Snoozed && row.Status != "closed":
			live.Snoozed++
		case row.Status == "waiting_customer":
			live.WaitingCustomer++
		case row.Status == "open" && row.Assignee != uuid.Nil:
			live.InAttendance++
		case row.Assignee == uuid.Nil && row.Status != "closed":
			live.Unassigned++
		}
	}
	for _, queue := range in.OpenQueues {
		if !sectorOK(in.SectorAllow, queue.SectorID) || queue.End != nil {
			continue
		}
		live.InQueue++
		wait := int64(in.Now.Sub(queue.Start) / time.Second)
		if wait < 0 {
			wait = 0
		}
		if wait > live.LongestQueueWaitSeconds {
			live.LongestQueueWaitSeconds = wait
		}
		priority := queue.Priority
		if priority == "" {
			priority = "normal"
		}
		threshold := queue.Start.Add(time.Duration(limit) * time.Second)
		if priority != "urgent" && !in.Now.Before(threshold) {
			live.WaitAlerts++
		}
	}
	return live
}

func inPeriod(at, from, to time.Time) bool {
	return !at.Before(from) && at.Before(to)
}

func sectorOK(allow map[uuid.UUID]bool, id uuid.UUID) bool {
	if allow == nil {
		return id != uuid.Nil
	}
	return allow[id]
}

func summarize(values []time.Duration, na int) Stats {
	stats := Stats{Sample: len(values), NotApplicable: na}
	if len(values) == 0 {
		return stats
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, value := range sorted {
		sum += value
	}
	average := int64((sum / time.Duration(len(sorted))).Round(time.Second) / time.Second)
	median := medianSeconds(sorted)
	stats.AverageSeconds = &average
	stats.MedianSeconds = &median
	return stats
}

func medianSeconds(sorted []time.Duration) int64 {
	n := len(sorted)
	if n%2 == 1 {
		return int64(sorted[n/2].Round(time.Second) / time.Second)
	}
	mid := (sorted[n/2-1] + sorted[n/2]) / 2
	return int64(mid.Round(time.Second) / time.Second)
}

func unionDuration(parts []window, clipStart, clipEnd time.Time) time.Duration {
	clipped := make([]window, 0, len(parts))
	for _, part := range parts {
		start, end := part.Start, part.End
		if !end.After(start) || !start.Before(clipEnd) || !end.After(clipStart) {
			continue
		}
		if start.Before(clipStart) {
			start = clipStart
		}
		if end.After(clipEnd) {
			end = clipEnd
		}
		if end.After(start) {
			clipped = append(clipped, window{start, end})
		}
	}
	if len(clipped) == 0 {
		return 0
	}
	sort.Slice(clipped, func(i, j int) bool { return clipped[i].Start.Before(clipped[j].Start) })
	curStart, curEnd := clipped[0].Start, clipped[0].End
	var total time.Duration
	for _, part := range clipped[1:] {
		if !part.Start.After(curEnd) {
			if part.End.After(curEnd) {
				curEnd = part.End
			}
			continue
		}
		total += curEnd.Sub(curStart)
		curStart, curEnd = part.Start, part.End
	}
	return total + curEnd.Sub(curStart)
}

func waitWindows(cycle Cycle, events []Event, now time.Time) []window {
	limit := now
	if cycle.Closed != nil {
		limit = *cycle.Closed
	}
	var open *time.Time
	var out []window
	for _, event := range events {
		if event.ConversationID != cycle.ConversationID || event.CycleNumber != cycle.CycleNumber {
			continue
		}
		switch event.Kind {
		case "wait_started":
			if open == nil {
				at := event.At
				open = &at
			}
		case "wait_ended":
			if open != nil {
				out = append(out, window{*open, event.At})
				open = nil
			}
		}
	}
	if open != nil {
		out = append(out, window{*open, limit})
	}
	return out
}

func snoozeWindows(cycle Cycle, snoozes []Snooze, now time.Time) []window {
	limit := now
	if cycle.Closed != nil {
		limit = *cycle.Closed
	}
	var out []window
	for _, snooze := range snoozes {
		if snooze.ConversationID != cycle.ConversationID {
			continue
		}
		end := snooze.End
		if end.IsZero() {
			end = now
		}
		if !snooze.Start.Before(limit) && !snooze.Start.Equal(limit) {
			continue
		}
		if !end.After(cycle.Opened) {
			continue
		}
		out = append(out, window{snooze.Start, end})
	}
	return out
}

func countsAsVolume(message Message) bool {
	if message.Historical || message.Visibility != "public" {
		return false
	}
	switch message.Origin {
	case "customer", "human", "bot", "menu":
		return true
	default:
		return false
	}
}

func customerMessage(message Message) bool {
	return !message.Historical && message.Direction == "inbound" && message.Origin == "customer" && message.Visibility == "public"
}

func humanReply(message Message) bool {
	return !message.Historical && message.Direction == "outbound" && message.Origin == "human" && message.Visibility == "public" && message.State == "accepted"
}

func authorMessage(message Message) bool {
	if message.Historical || message.Author == uuid.Nil || message.Origin != "human" || message.Visibility != "public" {
		return false
	}
	return message.State != "failed" && message.State != "cancelled"
}

func cycleLimit(cycle Cycle, now time.Time) time.Time {
	if cycle.Closed != nil {
		return *cycle.Closed
	}
	return now
}

func insideCycle(at time.Time, cycle Cycle, now time.Time) bool {
	if at.Before(cycle.Opened) {
		return false
	}
	return !at.After(cycleLimit(cycle, now))
}

func firstHuman(cycle Cycle, messages []Message, now time.Time) (time.Duration, bool) {
	var customer *time.Time
	for _, message := range messages {
		if message.ConversationID != cycle.ConversationID || !customerMessage(message) || !insideCycle(message.At, cycle, now) {
			continue
		}
		at := message.At
		customer = &at
		break
	}
	if customer == nil {
		return 0, false
	}
	for _, message := range messages {
		if message.ConversationID != cycle.ConversationID || !humanReply(message) || !insideCycle(message.At, cycle, now) {
			continue
		}
		if message.At.Before(*customer) {
			continue
		}
		return message.At.Sub(*customer), true
	}
	return 0, false
}

func responsibility(cycles []Cycle, events []Event, now time.Time) []span {
	by := map[string][]Event{}
	for _, event := range events {
		key := event.ConversationID.String() + ":" + itoa(event.CycleNumber)
		by[key] = append(by[key], event)
	}
	var out []span
	for _, cycle := range cycles {
		key := cycle.ConversationID.String() + ":" + itoa(cycle.CycleNumber)
		out = append(out, responsibilityCycle(cycle, by[key], now)...)
	}
	return out
}

func responsibilityCycle(cycle Cycle, events []Event, now time.Time) []span {
	sector := initialSector(events, cycle.SectorID)
	limit := cycleLimit(cycle, now)
	var current *span
	var out []span
	flush := func(at time.Time) {
		if current == nil {
			return
		}
		end := at
		if end.After(limit) {
			end = limit
		}
		if end.After(current.Start) {
			current.End = end
			out = append(out, *current)
		}
		current = nil
	}
	for _, event := range events {
		change, ok := assignmentChange(event)
		if !ok {
			continue
		}
		flush(event.At)
		if change.hasSector && change.sector != uuid.Nil {
			sector = change.sector
		}
		if change.hasMember {
			current = &span{
				ConversationID: cycle.ConversationID,
				CycleNumber:    cycle.CycleNumber,
				SectorID:       sector,
				MembershipID:   change.member,
				Start:          event.At,
			}
			if current.Start.Before(cycle.Opened) {
				current.Start = cycle.Opened
			}
		}
	}
	flush(limit)
	return out
}

type assignment struct {
	hasSector bool
	sector    uuid.UUID
	hasMember bool
	member    uuid.UUID
}

func assignmentChange(event Event) (assignment, bool) {
	if event.Kind == "assumed" && event.Assignee != nil && *event.Assignee != uuid.Nil {
		return assignment{hasMember: true, member: *event.Assignee}, true
	}
	parts := strings.Split(event.Kind, ":")
	if len(parts) != 5 {
		return assignment{}, false
	}
	switch parts[0] {
	case "transferred_member", "transferred_sector", "unassigned", "distributed", "snooze_return", "offline_unassigned":
	default:
		return assignment{}, false
	}
	toSector, errSector := uuid.Parse(parts[2])
	toMember, errMember := uuid.Parse(parts[4])
	if errSector != nil || errMember != nil {
		return assignment{}, false
	}
	change := assignment{}
	if toSector != uuid.Nil {
		change.hasSector = true
		change.sector = toSector
	}
	if toMember != uuid.Nil {
		change.hasMember = true
		change.member = toMember
	}
	if event.Assignee != nil && *event.Assignee != uuid.Nil && parts[0] != "unassigned" && parts[0] != "offline_unassigned" {
		change.hasMember = true
		change.member = *event.Assignee
	}
	return change, true
}

func initialSector(events []Event, fallback uuid.UUID) uuid.UUID {
	for _, event := range events {
		parts := strings.Split(event.Kind, ":")
		if len(parts) != 5 {
			continue
		}
		id, err := uuid.Parse(parts[1])
		if err == nil && id != uuid.Nil {
			return id
		}
	}
	return fallback
}

func resultRows(closed []Cycle) []ResultRow {
	type key struct{ result, kind string }
	counts := map[key]int{}
	for _, cycle := range closed {
		if cycle.CloseResult == "" {
			continue
		}
		counts[key{cycle.CloseResult, cycle.CloseAuthorKind}]++
	}
	rows := make([]ResultRow, 0, len(counts))
	for item, count := range counts {
		rows = append(rows, ResultRow{Result: item.result, AuthorKind: item.kind, Label: resultLabel(item.result, item.kind), Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Label < rows[j].Label
		}
		return rows[i].Count > rows[j].Count
	})
	return rows
}

func resultLabel(result, kind string) string {
	switch {
	case result == "resolved" && kind == "bot":
		return "Encerrado pelo bot"
	case result == "resolved":
		return "Solução confirmada por pessoa"
	case result == "no_response" && kind == "bot":
		return "Sem resposta, encerrado pelo bot"
	case result == "no_response":
		return "Sem resposta"
	case result == "referred_external":
		return "Encaminhado para fora"
	case result == "spam":
		return "Spam"
	default:
		if kind == "bot" {
			return "Encerrado pelo bot"
		}
		return "Encerrado"
	}
}

func sectorRows(in Facts, opened []Cycle, spans []span, queues []Queue) []SectorRow {
	type acc struct {
		cycles int
		waits  []time.Duration
		hold   time.Duration
	}
	seen := map[uuid.UUID]*acc{}
	touch := func(id uuid.UUID) *acc {
		if !sectorOK(in.SectorAllow, id) {
			return nil
		}
		row := seen[id]
		if row == nil {
			row = &acc{}
			seen[id] = row
		}
		return row
	}
	for _, cycle := range opened {
		sectors := map[uuid.UUID]struct{}{}
		limit := cycleLimit(cycle, in.Now)
		for _, item := range spans {
			if item.ConversationID != cycle.ConversationID || item.CycleNumber != cycle.CycleNumber {
				continue
			}
			if item.Start.Before(limit) && item.End.After(cycle.Opened) {
				sectors[item.SectorID] = struct{}{}
			}
		}
		for _, queue := range queues {
			if queue.ConversationID != cycle.ConversationID {
				continue
			}
			end := in.Now
			if queue.End != nil {
				end = *queue.End
			}
			if queue.Start.Before(limit) && end.After(cycle.Opened) {
				sectors[queue.SectorID] = struct{}{}
			}
		}
		if len(sectors) == 0 && cycle.SectorID != uuid.Nil {
			sectors[cycle.SectorID] = struct{}{}
		}
		for id := range sectors {
			if row := touch(id); row != nil {
				row.cycles++
			}
		}
	}
	for _, item := range spans {
		row := touch(item.SectorID)
		if row == nil {
			continue
		}
		row.hold += overlap(item.Start, item.End, in.From, in.To)
	}
	for _, queue := range queues {
		if queue.End == nil || !inPeriod(*queue.End, in.From, in.To) {
			continue
		}
		row := touch(queue.SectorID)
		if row == nil {
			continue
		}
		d := queue.End.Sub(queue.Start)
		if d > 0 {
			row.waits = append(row.waits, d)
		}
	}
	rows := make([]SectorRow, 0, len(seen))
	for id, row := range seen {
		name := in.Names.Sectors[id]
		if name == "" {
			name = "Setor"
		}
		item := SectorRow{
			SectorID:              id,
			Name:                  name,
			Cycles:                row.cycles,
			QueueWaits:            len(row.waits),
			ResponsibilitySeconds: int64(row.hold / time.Second),
		}
		if len(row.waits) > 0 {
			stats := summarize(row.waits, 0)
			item.QueueWaitMedianSeconds = stats.MedianSeconds
		}
		rows = append(rows, item)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].SectorID.String() < rows[j].SectorID.String()
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func attendantRows(in Facts, spans []span, messages []Message, closed []Cycle) []AttendantRow {
	type acc struct {
		hold     time.Duration
		messages int
		closures int
	}
	seen := map[uuid.UUID]*acc{}
	ensure := func(id uuid.UUID) *acc {
		row := seen[id]
		if row == nil {
			row = &acc{}
			seen[id] = row
		}
		return row
	}
	for _, item := range spans {
		if !sectorOK(in.SectorAllow, item.SectorID) || item.MembershipID == uuid.Nil {
			continue
		}
		ensure(item.MembershipID).hold += overlap(item.Start, item.End, in.From, in.To)
	}
	for _, message := range messages {
		if !authorMessage(message) || !inPeriod(message.At, in.From, in.To) {
			continue
		}
		ensure(message.Author).messages++
	}
	for _, cycle := range closed {
		if cycle.CloseMembership == uuid.Nil {
			continue
		}
		ensure(cycle.CloseMembership).closures++
	}
	rows := make([]AttendantRow, 0, len(seen))
	for id, row := range seen {
		if row.hold == 0 && row.messages == 0 && row.closures == 0 {
			continue
		}
		label := in.Names.Members[id]
		if label == "" {
			label = "Atendente"
		}
		rows = append(rows, AttendantRow{
			MembershipID:          id,
			Label:                 label,
			ResponsibilitySeconds: int64(row.hold / time.Second),
			Messages:              row.messages,
			Closures:              row.closures,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Label == rows[j].Label {
			return rows[i].MembershipID.String() < rows[j].MembershipID.String()
		}
		return rows[i].Label < rows[j].Label
	})
	return rows
}

func inboxRows(in Facts, opened []Cycle, messages []Message) []InboxRow {
	type acc struct{ cycles, messages int }
	seen := map[uuid.UUID]*acc{}
	for _, cycle := range opened {
		row := seen[cycle.InboxID]
		if row == nil {
			row = &acc{}
			seen[cycle.InboxID] = row
		}
		row.cycles++
	}
	for _, message := range messages {
		if !countsAsVolume(message) || !inPeriod(message.At, in.From, in.To) || message.InboxID == uuid.Nil {
			continue
		}
		row := seen[message.InboxID]
		if row == nil {
			row = &acc{}
			seen[message.InboxID] = row
		}
		row.messages++
	}
	rows := make([]InboxRow, 0, len(seen))
	for id, row := range seen {
		name := in.Names.Inboxes[id]
		if name == "" {
			name = "Caixa"
		}
		rows = append(rows, InboxRow{InboxID: id, Name: name, Cycles: row.cycles, Messages: row.messages})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func reasonRows(opened []Cycle, tags []Tag) ([]ReasonRow, []ReasonRow) {
	primary := map[string]int{}
	for _, cycle := range opened {
		reason := strings.TrimSpace(cycle.CloseReason)
		if reason == "" {
			reason = strings.TrimSpace(cycle.TriageReason)
		}
		if reason != "" {
			primary[reason]++
		}
	}
	extra := map[string]int{}
	openedConv := map[uuid.UUID]int{}
	for _, cycle := range opened {
		openedConv[cycle.ConversationID]++
	}
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Name)
		n := openedConv[tag.ConversationID]
		if name == "" || n == 0 {
			continue
		}
		extra[name] += n
	}
	return reasonList(primary, false), reasonList(extra, true)
}

func reasonList(counts map[string]int, overlap bool) []ReasonRow {
	rows := make([]ReasonRow, 0, len(counts))
	for name, count := range counts {
		rows = append(rows, ReasonRow{Name: name, Count: count, Overlap: overlap})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Count > rows[j].Count
	})
	return rows
}

func heatCells(opened []Cycle, loc *time.Location) []HeatCell {
	counts := map[[2]int]int{}
	for _, cycle := range opened {
		local := cycle.Opened.In(loc)
		counts[[2]int{int(local.Weekday()), local.Hour()}]++
	}
	rows := make([]HeatCell, 0, len(counts))
	for key, count := range counts {
		rows = append(rows, HeatCell{Weekday: key[0], Hour: key[1], Cycles: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Weekday == rows[j].Weekday {
			return rows[i].Hour < rows[j].Hour
		}
		return rows[i].Weekday < rows[j].Weekday
	})
	return rows
}

func buckets(in Facts, opened, closed []Cycle, messages []Message, events []Event, loc *time.Location, group string) []Bucket {
	type acc struct {
		cycles, messages, closed int
		first                    []time.Duration
		firstNA                  int
		operational              []time.Duration
	}
	seen := map[string]*acc{}
	ensure := func(key string) *acc {
		row := seen[key]
		if row == nil {
			row = &acc{}
			seen[key] = row
		}
		return row
	}
	for _, cycle := range opened {
		row := ensure(bucketKey(cycle.Opened, loc, group))
		row.cycles++
		if d, ok := firstHuman(cycle, messages, in.Now); ok {
			row.first = append(row.first, d)
		} else {
			row.firstNA++
		}
	}
	for _, message := range messages {
		if countsAsVolume(message) && inPeriod(message.At, in.From, in.To) {
			ensure(bucketKey(message.At, loc, group)).messages++
		}
	}
	for _, cycle := range closed {
		row := ensure(bucketKey(*cycle.Closed, loc, group))
		row.closed++
		total := cycle.Closed.Sub(cycle.Opened)
		paused := unionDuration(append(waitWindows(cycle, events, in.Now), snoozeWindows(cycle, in.Snoozes, in.Now)...), cycle.Opened, *cycle.Closed)
		op := total - paused
		if op < 0 {
			op = 0
		}
		row.operational = append(row.operational, op)
	}
	rows := make([]Bucket, 0, len(seen))
	for key, row := range seen {
		first := summarize(row.first, row.firstNA)
		op := summarize(row.operational, 0)
		rows = append(rows, Bucket{
			Key: key, Cycles: row.cycles, Messages: row.messages, Closed: row.closed,
			FirstResponseSample: first.Sample, FirstResponseAverage: first.AverageSeconds,
			OperationalSample: op.Sample, OperationalAverage: op.AverageSeconds,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

func bucketKey(at time.Time, loc *time.Location, group string) string {
	local := at.In(loc)
	switch group {
	case "month":
		return local.Format("2006-01")
	case "week":
		return weekStart(local).Format("2006-01-02")
	default:
		return local.Format("2006-01-02")
	}
}

func weekStart(local time.Time) time.Time {
	weekday := int(local.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	return day.AddDate(0, 0, -(weekday - 1))
}

func overlap(start, end, from, to time.Time) time.Duration {
	if !end.After(start) || !start.Before(to) || !end.After(from) {
		return 0
	}
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
