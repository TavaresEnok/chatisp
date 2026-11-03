package triage

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"chatisp/internal/businesshours"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Input is one customer text already stored by the caller.
// Port may be nil. A nil port does not claim that somebody is available.
type Input struct {
	TenantID       uuid.UUID
	InboxID        uuid.UUID
	ConversationID uuid.UUID
	Text           string
	Instant        time.Time
	InputID        uuid.UUID
	Port           Port
}

type conversationRow struct {
	Inbox      uuid.UUID
	Status     string
	Controller string
	Protocol   string
	Epoch      int64
	Version    int64
	Cycle      int
}

type stateRow struct {
	Attempts        int
	WelcomeSent     bool
	Resolved        bool
	Phase           string
	MenuVersion     int64
	InboxID         uuid.UUID
	CycleNumber     int
	ControllerEpoch int64
}

// Decide chooses the next triage actions. The transaction must already carry
// app.tenant_id. The conversation row is locked before inbox and sector shares,
// and the outcome carries that controller epoch and version.
// Welcome is emitted once per conversation, using the inbox text and that
// conversation's protocol. The first arrival shows the menu and does not spend
// an attempt. Later unexpected input counts until the menu limit, then the
// default sector is the human exit. A human request does not wait for the limit
// and does not spend an attempt. Resolved state belongs to the conversation
// cycle and controller epoch locked with the row. A manual reopen keeps the
// conversation, increments both and returns the menu controller; the next new
// input starts that possession again. The same input id still returns the stored
// outcome, and earlier events stay. The function never inserts a message or
// assigns an attendant. A human request wins over a sector label or key.
func Decide(ctx context.Context, tx pgx.Tx, in Input) (Outcome, error) {
	var out Outcome
	if tx == nil || in.TenantID == uuid.Nil || in.InboxID == uuid.Nil || in.ConversationID == uuid.Nil || in.InputID == uuid.Nil || in.Instant.IsZero() {
		return out, ErrInvalid
	}
	if err := pinTenant(ctx, tx, in.TenantID); err != nil {
		return out, err
	}
	row, err := loadConversation(ctx, tx, in.TenantID, in.ConversationID)
	if err != nil {
		return out, err
	}
	if row.Inbox != in.InboxID {
		return out, sectors.ErrNotFound
	}
	if skipConversation(row) {
		return stamp(Outcome{Actions: []Action{}, Phase: PhaseSkipped}, row), nil
	}
	hours, err := businesshours.EvaluateTx(ctx, tx, in.Instant, in.TenantID, in.InboxID)
	if err != nil {
		return out, err
	}
	menu, err := loadMenu(ctx, tx, in.TenantID, in.InboxID)
	if err != nil {
		return out, err
	}
	state, err := lockState(ctx, tx, in.TenantID, in.InboxID, in.ConversationID)
	if err != nil {
		return out, err
	}
	if stored, ok, err := loadDecision(ctx, tx, in); err != nil || ok {
		return stored, err
	}
	bindPossession(&state, row)
	if state.Resolved {
		return stamp(quiet(state), row), nil
	}
	out, state, err = branch(ctx, tx, in, hours, menu, state, row.Protocol)
	if err != nil {
		return Outcome{}, err
	}
	out = stamp(out, row)
	if err = saveDecision(ctx, tx, in, state, out); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

func stamp(out Outcome, row conversationRow) Outcome {
	out.ControllerEpoch = row.Epoch
	out.Version = row.Version
	if out.Actions == nil {
		out.Actions = []Action{}
	}
	return out
}

func pinTenant(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	var scope string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&scope); err != nil {
		return err
	}
	if scope != tenant.String() {
		return sectors.ErrForbidden
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, tenant.String())
	return err
}

func loadConversation(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID) (conversationRow, error) {
	var row conversationRow
	var protocol *string
	// The comment marks this wait in pg_stat_activity. The row lock is the same
	// exclusive conversation lock used by attendance, taken before any sector share.
	err := tx.QueryRow(ctx, `
		SELECT inbox_id, status, controller, controller_epoch, version, protocol, cycle_number
		FROM conversations
		WHERE tenant_id=$1 AND id=$2
		FOR UPDATE
		-- triage-conversation`, tenant, conversation).Scan(&row.Inbox, &row.Status, &row.Controller, &row.Epoch, &row.Version, &protocol, &row.Cycle)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, sectors.ErrNotFound
	}
	if err != nil {
		return row, err
	}
	if row.Inbox == uuid.Nil {
		return row, sectors.ErrNotFound
	}
	if protocol != nil {
		row.Protocol = *protocol
	}
	return row, nil
}

func skipConversation(row conversationRow) bool {
	if row.Controller != "menu" {
		return true
	}
	return row.Status != "open" && row.Status != "triage"
}

func lockState(ctx context.Context, tx pgx.Tx, tenant, inbox, conversation uuid.UUID) (stateRow, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO triage_states(tenant_id,conversation_id,inbox_id,attempts,welcome_sent,resolved,phase,menu_version) VALUES($1,$2,$3,0,false,false,'',0) ON CONFLICT DO NOTHING`, tenant, conversation, inbox); err != nil {
		return stateRow{}, err
	}
	var state stateRow
	err := tx.QueryRow(ctx, `SELECT attempts, welcome_sent, resolved, phase, menu_version, inbox_id, cycle_number, controller_epoch FROM triage_states WHERE tenant_id=$1 AND conversation_id=$2 FOR UPDATE`, tenant, conversation).Scan(&state.Attempts, &state.WelcomeSent, &state.Resolved, &state.Phase, &state.MenuVersion, &state.InboxID, &state.CycleNumber, &state.ControllerEpoch)
	if err != nil {
		return stateRow{}, err
	}
	if state.InboxID != inbox {
		return stateRow{}, ErrConflict
	}
	return state, nil
}

func loadDecision(ctx context.Context, tx pgx.Tx, in Input) (Outcome, bool, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT outcome FROM triage_decisions WHERE tenant_id=$1 AND conversation_id=$2 AND input_id=$3`, in.TenantID, in.ConversationID, in.InputID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	var out Outcome
	if err = json.Unmarshal(raw, &out); err != nil {
		return Outcome{}, false, ErrInvalid
	}
	if out.Actions == nil {
		out.Actions = []Action{}
	}
	return out, true, nil
}

func quiet(state stateRow) Outcome {
	return Outcome{Actions: []Action{}, Attempts: state.Attempts, WelcomeSent: state.WelcomeSent, Phase: state.Phase}
}

// bindPossession restarts triage when attendance reopened the same conversation.
// Welcome stays once per conversation. History and the stored input id do not move.
func bindPossession(state *stateRow, row conversationRow) {
	if state.CycleNumber == row.Cycle && state.ControllerEpoch == row.Epoch {
		return
	}
	state.CycleNumber = row.Cycle
	state.ControllerEpoch = row.Epoch
	state.Attempts = 0
	state.Resolved = false
	state.Phase = ""
	state.MenuVersion = 0
}

func branch(ctx context.Context, tx pgx.Tx, in Input, hours businesshours.Decision, menu Menu, state stateRow, protocol string) (Outcome, stateRow, error) {
	if hours.Configured && !hours.Open {
		return closeBranch(ctx, tx, in, hours, state, protocol)
	}
	if !menu.Configured {
		return openRoute(ctx, tx, in, hours, state, protocol, hours.DestinationSectorID, "", PhaseRouted)
	}
	limit := menu.MaxAttempts
	if limit < 1 {
		limit = 1
	}
	options := ensureHuman(menu.Options)
	state.MenuVersion = menu.Version
	if requestsHuman(in.Text, options) {
		return openRoute(ctx, tx, in, hours, state, protocol, hours.DefaultSectorID, "", PhaseRouted)
	}
	if option, ok := matchOption(in.Text, options); ok && option.Destination == DestSector && option.SectorID != nil {
		linked, err := sectorLinked(ctx, tx, in.TenantID, in.InboxID, *option.SectorID)
		if err != nil {
			return Outcome{}, state, err
		}
		if linked {
			return openRoute(ctx, tx, in, hours, state, protocol, option.SectorID, option.Reason, PhaseRouted)
		}
	}
	// The opening text presents the menu. It is not an unexpected reply.
	if state.Phase != PhaseMenu {
		actions := []Action{menuAction(options)}
		actions = withWelcome(&state, hours, protocol, actions)
		state.Phase = PhaseMenu
		state.Resolved = false
		return Outcome{Actions: actions, Attempts: state.Attempts, WelcomeSent: state.WelcomeSent, Phase: PhaseMenu}, state, nil
	}
	state.Attempts++
	if state.Attempts >= limit {
		return openRoute(ctx, tx, in, hours, state, protocol, hours.DefaultSectorID, "", PhaseExhausted)
	}
	actions := []Action{menuAction(options)}
	actions = withWelcome(&state, hours, protocol, actions)
	state.Phase = PhaseMenu
	state.Resolved = false
	return Outcome{Actions: actions, Attempts: state.Attempts, WelcomeSent: state.WelcomeSent, Phase: PhaseMenu}, state, nil
}

func closeBranch(ctx context.Context, tx pgx.Tx, in Input, hours businesshours.Decision, state stateRow, protocol string) (Outcome, stateRow, error) {
	dest := hours.DestinationSectorID
	onCall := hours.OnCallSectorID != nil && dest != nil && *hours.OnCallSectorID == *dest
	phase := PhaseOutside
	if onCall {
		phase = PhaseOnCall
	}
	actions := []Action{{Kind: KindOutsideHours, Text: hours.Config.OutsideHours}}
	actions = append(actions, routeActions(dest, "", onCall)...)
	actions, err := withAvailability(ctx, tx, in, hours, dest, actions)
	if err != nil {
		return Outcome{}, state, err
	}
	actions = withWelcome(&state, hours, protocol, actions)
	state.Resolved = true
	state.Phase = phase
	return Outcome{Actions: actions, Attempts: state.Attempts, WelcomeSent: state.WelcomeSent, Phase: phase}, state, nil
}

func openRoute(ctx context.Context, tx pgx.Tx, in Input, hours businesshours.Decision, state stateRow, protocol string, dest *uuid.UUID, reason, phase string) (Outcome, stateRow, error) {
	actions := routeActions(dest, reason, false)
	actions, err := withAvailability(ctx, tx, in, hours, dest, actions)
	if err != nil {
		return Outcome{}, state, err
	}
	actions = withWelcome(&state, hours, protocol, actions)
	state.Resolved = true
	state.Phase = phase
	return Outcome{Actions: actions, Attempts: state.Attempts, WelcomeSent: state.WelcomeSent, Phase: phase}, state, nil
}

func routeActions(dest *uuid.UUID, reason string, onCall bool) []Action {
	actions := make([]Action, 0, 4)
	if reason != "" {
		actions = append(actions, Action{Kind: KindRecordReason, Reason: reason, SectorID: dest})
	}
	if dest != nil {
		actions = append(actions, Action{Kind: KindRouteSector, SectorID: dest})
	}
	if onCall && dest != nil {
		actions = append(actions, Action{Kind: KindOnCall, SectorID: dest})
	}
	actions = append(actions, Action{Kind: KindForwardHuman, SectorID: dest})
	return actions
}

func withAvailability(ctx context.Context, tx pgx.Tx, in Input, hours businesshours.Decision, dest *uuid.UUID, actions []Action) ([]Action, error) {
	if dest == nil || in.Port == nil {
		return actions, nil
	}
	snap, err := in.Port.Inspect(ctx, tx, in.TenantID, *dest, in.InboxID, in.Instant)
	if err != nil {
		return nil, err
	}
	if snap.Reason == ReasonNobodyAvailable {
		actions = append(actions, Action{Kind: KindNobodyAvailable, Text: hours.Config.NobodyAvailable})
	}
	return actions, nil
}

func withWelcome(state *stateRow, hours businesshours.Decision, protocol string, actions []Action) []Action {
	if state.WelcomeSent {
		return actions
	}
	state.WelcomeSent = true
	text := strings.ReplaceAll(hours.Config.Welcome, "{protocolo}", protocol)
	if text == "" {
		return actions
	}
	return append([]Action{{Kind: KindSendText, Text: text}}, actions...)
}

func menuAction(options []Option) Action {
	return Action{Kind: KindSendMenu, Text: "Responda com o número da opção.", Fallback: numbered(options), Options: options}
}

func numbered(options []Option) string {
	lines := make([]string, 0, len(options))
	for _, option := range options {
		lines = append(lines, option.Key+" - "+option.Label)
	}
	return strings.Join(lines, "\n")
}

func ensureHuman(options []Option) []Option {
	for _, option := range options {
		if option.Destination == DestHuman {
			return options
		}
	}
	used := map[string]bool{}
	for _, option := range options {
		used[strings.ToLower(option.Key)] = true
	}
	key := "0"
	if used[key] {
		for n := 1; n < 10000; n++ {
			candidate := strconv.Itoa(n)
			if !used[candidate] {
				key = candidate
				break
			}
		}
	}
	copyOptions := append([]Option{}, options...)
	return append(copyOptions, Option{Key: key, Label: "Falar com atendente", Destination: DestHuman})
}

func normalizeChoice(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func reservedHuman(text string) bool {
	switch text {
	case "humano", "atendente", "pessoa", "falar com humano", "falar com atendente":
		return true
	default:
		return false
	}
}

// keyMatch accepts only an unambiguous reply: the key, the label, or the key
// plus one separator and that same label. A longer phrase is unexpected.
func keyMatch(text string, option Option) bool {
	if text == "" {
		return false
	}
	for _, form := range choiceForms(option) {
		if text == form {
			return true
		}
	}
	return false
}

func optionMatches(text string, option Option) bool {
	return keyMatch(text, option)
}

func matchOption(text string, options []Option) (Option, bool) {
	normalized := normalizeChoice(text)
	if normalized == "" {
		return Option{}, false
	}
	var sector Option
	foundSector := false
	for _, option := range options {
		if !optionMatches(normalized, option) {
			continue
		}
		if option.Destination == DestHuman {
			return option, true
		}
		if !foundSector {
			sector = option
			foundSector = true
		}
	}
	if reservedHuman(normalized) || (foundSector && humanKey(normalized, options)) {
		return Option{}, false
	}
	if foundSector {
		return sector, true
	}
	return Option{}, false
}

func humanKey(text string, options []Option) bool {
	for _, option := range options {
		if option.Destination == DestHuman && keyMatch(text, option) {
			return true
		}
	}
	return false
}

func requestsHuman(text string, options []Option) bool {
	normalized := normalizeChoice(text)
	if reservedHuman(normalized) {
		return true
	}
	if option, ok := matchOption(text, options); ok {
		return option.Destination == DestHuman
	}
	return false
}

func saveDecision(ctx context.Context, tx pgx.Tx, in Input, state stateRow, out Outcome) error {
	if out.Actions == nil {
		out.Actions = []Action{}
	}
	tag, err := tx.Exec(ctx, `UPDATE triage_states SET attempts=$4, welcome_sent=$5, resolved=$6, phase=$7, menu_version=$8, cycle_number=$9, controller_epoch=$10, updated_at=now() WHERE tenant_id=$1 AND conversation_id=$2 AND inbox_id=$3`, in.TenantID, in.ConversationID, in.InboxID, state.Attempts, state.WelcomeSent, state.Resolved, state.Phase, state.MenuVersion, state.CycleNumber, state.ControllerEpoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO triage_decisions(tenant_id,conversation_id,input_id,outcome) VALUES($1,$2,$3,$4)`, in.TenantID, in.ConversationID, in.InputID, raw); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO triage_events(tenant_id,id,conversation_id,input_id,kind,detail) VALUES($1,$2,$3,$4,$5,$6)`, in.TenantID, uuid.New(), in.ConversationID, in.InputID, out.Phase, raw)
	return err
}
