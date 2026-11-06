package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"chatisp/internal/lifecycle"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/db/dbgen"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/triage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Automation composes domain rules. Outgoing text uses the existing channel
// outbox and its lease/epoch/window checks; it never calls a driver directly.
type Automation struct {
	DB            *db.Store
	Jobs          *jobs.Client
	TriageInboxes map[uuid.UUID]bool
	Now           func() time.Time
}

func (a Automation) clock() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// ApplyTriage is recoverable after inbound commit. The same source/message is
// recorded with the decision and all effects in one transaction.
func (a Automation) ApplyTriage(ctx context.Context, tenant, item uuid.UUID) error {
	return a.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := automationTenant(ctx, tx, tenant); err != nil {
			return err
		}
		var conv, inbox uuid.UUID
		var text string
		var source uuid.UUID
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT c.id,c.inbox_id,m.id,m.body,m.trusted_at FROM webhook_items w JOIN messages m ON m.tenant_id=w.tenant_id AND m.connector_id=w.connector_id AND m.external_message_id=w.payload->>'external_id' JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id JOIN contacts subject ON subject.tenant_id=c.tenant_id AND subject.id=c.contact_id AND subject.privacy_erased_at IS NULL WHERE w.tenant_id=$1 AND w.id=$2 AND w.kind='message' AND w.state='processed' AND NOT m.historical AND m.trusted_at>=c.last_customer_message_at`, tenant, item).Scan(&conv, &inbox, &source, &text, &at)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !a.TriageInboxes[inbox] {
			tag, err := tx.Exec(ctx, `UPDATE conversations SET status='open',version=version+1,updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND status='triage' AND controller='menu'`, tenant, conv, a.clock())
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				return automationPublish(ctx, tx, tenant, conv)
			}
			return nil
		}
		out, err := triage.Decide(ctx, tx, triage.Input{TenantID: tenant, InboxID: inbox, ConversationID: conv, InputID: source, Text: text, Instant: a.clock(), Port: triage.Availability{}})
		if err != nil {
			return err
		}
		// Serialize and claim all effects while holding Decide's conversation lock.
		var claimed bool
		if err = tx.QueryRow(ctx, `INSERT INTO automation_dispatches(tenant_id,id,conversation_id,source_id,action_index,kind) VALUES($1,$2,$3,$4,-1,'triage') ON CONFLICT DO NOTHING RETURNING true`, tenant, uuid.New(), conv, source).Scan(&claimed); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if out.Phase == triage.PhaseSkipped {
			return nil
		}
		// A newer menu decision invalidates queued output from older inputs.
		// Sending/uncertain rows remain honest about the dispatch boundary.
		if _, err = tx.Exec(ctx, `WITH cancelled AS (UPDATE outbound_attempts o SET state='cancelled' FROM automation_dispatches d WHERE o.tenant_id=$1 AND d.tenant_id=o.tenant_id AND d.attempt_id=o.id AND d.conversation_id=$2 AND d.kind='triage' AND d.source_id<>$3 AND o.state='queued' RETURNING o.message_id) UPDATE messages SET local_state='cancelled' WHERE tenant_id=$1 AND id IN (SELECT message_id FROM cancelled)`, tenant, conv, source); err != nil {
			return err
		}
		// Route first, keeping the menu epoch: the following messages remain
		// cancellable by a human takeover. Menu origin never starts human clocks.
		var sector *uuid.UUID
		var reason string
		terminal := false
		for _, action := range out.Actions {
			if action.Kind == triage.KindRouteSector || action.Kind == triage.KindOnCall {
				if action.SectorID != nil {
					sector = action.SectorID
				}
			}
			if action.Kind == triage.KindRecordReason {
				reason = action.Reason
			}
			if action.Kind == triage.KindForwardHuman {
				terminal = true
			}
		}
		status := "triage"
		if terminal {
			status = "open"
		}
		if _, err = tx.Exec(ctx, `UPDATE conversations SET sector_id=COALESCE($3,sector_id),status=$4,version=version+1,updated_at=$5 WHERE tenant_id=$1 AND id=$2 AND controller='menu' AND controller_epoch=$6 AND version=$7`, tenant, conv, sector, status, a.clock(), out.ControllerEpoch, out.Version); err != nil {
			return err
		}
		if reason != "" {
			if _, err = tx.Exec(ctx, `UPDATE automation_dispatches SET reason=$3 WHERE tenant_id=$1 AND conversation_id=$2 AND source_id=$4 AND action_index=-1`, tenant, conv, reason, source); err != nil {
				return err
			}
		}
		for index, action := range out.Actions {
			body := action.Text
			if action.Kind == triage.KindSendMenu {
				body = action.Fallback
			}
			if strings.TrimSpace(body) == "" {
				continue
			}
			if _, err = a.enqueue(ctx, tx, tenant, conv, source, index, "triage", 0, body); err != nil {
				return err
			}
		}
		return automationPublish(ctx, tx, tenant, conv)
	})
}

func automationTenant(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'),hashtext($1::text))`, tenant.String()); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT status='active' FROM tenants WHERE id=$1`, tenant).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrUnavailable
	}
	return nil
}

func (a Automation) enqueue(ctx context.Context, tx pgx.Tx, tenant, conv, source uuid.UUID, index int, kind string, generation int64, body string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO automation_dispatches(tenant_id,id,conversation_id,source_id,action_index,kind,generation) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING RETURNING id`, tenant, uuid.New(), conv, source, index, kind, generation).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	var connector uuid.UUID
	var controller string
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT i.connector_id,c.controller,c.controller_epoch FROM conversations c JOIN inboxes i ON i.tenant_id=c.tenant_id AND i.id=c.inbox_id WHERE c.tenant_id=$1 AND c.id=$2`, tenant, conv).Scan(&connector, &controller, &epoch); err != nil {
		return uuid.Nil, err
	}
	q := dbgen.New(tx)
	message := uuid.New()
	state := "queued"
	origin := "menu"
	if kind == "inactivity" {
		origin = "technical"
	}
	_, err = q.InsertMessage(ctx, dbgen.InsertMessageParams{TenantID: tenant, ID: message, ConversationID: conv, ConnectorID: connector, Direction: "outbound", Origin: origin, Visibility: "public", Body: body, LocalState: &state, Controller: &controller, ControllerEpoch: &epoch, TrustedAt: pgtype.Timestamptz{Time: a.clock(), Valid: true}})
	if err != nil {
		return uuid.Nil, err
	}
	attempt, err := q.InsertOutbound(ctx, dbgen.InsertOutboundParams{TenantID: tenant, ID: uuid.New(), MessageID: message, ConversationID: conv, AttemptNo: 1, Controller: controller, ControllerEpoch: epoch, Origin: origin})
	if err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE automation_dispatches SET attempt_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, attempt.ID); err != nil {
		return uuid.Nil, err
	}
	if _, err = a.Jobs.EnqueueOutbound(ctx, tx, tenant, attempt.ID); err != nil {
		return uuid.Nil, err
	}
	return attempt.ID, nil
}

func automationPublish(ctx context.Context, tx pgx.Tx, tenant, conv uuid.UUID) error {
	q := dbgen.New(tx)
	if err := q.EnsureRealtimeCursor(ctx, tenant); err != nil {
		return err
	}
	seq, err := q.AdvanceRealtimeCursor(ctx, tenant)
	if err != nil {
		return err
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, conv).Scan(&version); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"conversation_id": conv.String()})
	return q.InsertRealtimeEvent(ctx, dbgen.InsertRealtimeEventParams{TenantID: tenant, ID: uuid.New(), Seq: seq, EventType: "conversation.message", AggregateType: "conversation", AggregateID: conv, Version: version, Payload: payload})
}

// RunAutomations is invoked by River's scheduled queue. Every return and receipt
// uses its own transaction so capacity/cursor locks do not span conversations.
func (a Automation) RunAutomations(ctx context.Context) error {
	var tenants []db.Tenant
	if err := a.DB.WithPlatform(ctx, func(ctx context.Context) error { var err error; tenants, err = a.DB.ListTenants(ctx); return err }); err != nil {
		return err
	}
	var failures []error
	for _, tenant := range tenants {
		if tenant.Status != "active" {
			continue
		}
		if err := a.RunTenant(ctx, tenant.ID, a.clock()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (a Automation) RunTenant(ctx context.Context, tenant uuid.UUID, now time.Time) error {
	if err := a.releaseDisabledTriage(ctx, tenant, now); err != nil {
		return err
	}
	if _, err := lifecycle.New(a.DB).ReturnDue(ctx, tenant, now); err != nil {
		return err
	}
	if _, err := ReleaseDisabled(ctx, a.DB, tenant, now); err != nil {
		return err
	}
	if _, err := UnassignOffline(ctx, a.DB, tenant, now); err != nil {
		return err
	}
	if err := a.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := lifecycle.RunInactivity(ctx, tx, tenant, now)
		return err
	}); err != nil {
		return err
	}
	// Query IDs without locking, then take the conversation before each request.
	type warning struct {
		conv       uuid.UUID
		generation int64
	}
	var warnings []warning
	if err := a.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT conversation_id,generation FROM lifecycle_warning_requests WHERE tenant_id=$1 AND state='pending' ORDER BY conversation_id,generation LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w warning
			if err = rows.Scan(&w.conv, &w.generation); err != nil {
				return err
			}
			warnings = append(warnings, w)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, w := range warnings {
		if err := a.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if err := automationTenant(ctx, tx, tenant); err != nil {
				return err
			}
			var convID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, w.conv).Scan(&convID); err != nil {
				return err
			}
			source := uuid.NewSHA1(w.conv, []byte(fmt.Sprint(w.generation)))
			var attempt uuid.UUID
			var state string
			err := tx.QueryRow(ctx, `SELECT o.id,o.state FROM automation_dispatches d JOIN outbound_attempts o ON o.tenant_id=d.tenant_id AND o.id=d.attempt_id WHERE d.tenant_id=$1 AND d.conversation_id=$2 AND d.kind='inactivity' AND d.source_id=$3`, tenant, w.conv, source).Scan(&attempt, &state)
			if errors.Is(err, pgx.ErrNoRows) {
				var body string
				if err = tx.QueryRow(ctx, `SELECT body FROM lifecycle_warning_requests WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3 AND state='pending'`, tenant, w.conv, w.generation).Scan(&body); errors.Is(err, pgx.ErrNoRows) {
					return nil
				} else if err != nil {
					return err
				}
				if _, err = a.enqueue(ctx, tx, tenant, w.conv, source, 0, "inactivity", w.generation, body); err != nil {
					return err
				}
				return automationPublish(ctx, tx, tenant, w.conv)
			}
			if err != nil {
				return err
			}
			outcome := ""
			if state == "accepted" {
				outcome = lifecycle.WarningConfirmed
			}
			if state == "uncertain" || state == "failed" || state == "cancelled" {
				outcome = lifecycle.WarningUncertain
			}
			if outcome == "" {
				return nil
			}
			_, err = lifecycle.ConfirmWarning(ctx, tx, tenant, lifecycle.WarningReceipt{ConversationID: w.conv, Generation: w.generation, Outcome: outcome}, now)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Called with the conversation already locked at the final dispatch fence.
func automationAllowed(ctx context.Context, tx pgx.Tx, tenant, attempt uuid.UUID, enabled map[uuid.UUID]bool) (bool, error) {
	var kind string
	var generation int64
	var conv, inbox uuid.UUID
	err := tx.QueryRow(ctx, `SELECT d.kind,d.generation,d.conversation_id,c.inbox_id FROM automation_dispatches d JOIN conversations c ON c.tenant_id=d.tenant_id AND c.id=d.conversation_id WHERE d.tenant_id=$1 AND d.attempt_id=$2`, tenant, attempt).Scan(&kind, &generation, &conv, &inbox)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if kind == "triage" {
		return enabled[inbox], nil
	}
	var live bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversations c JOIN inactivity_watches w ON w.tenant_id=c.tenant_id AND w.conversation_id=c.id JOIN lifecycle_warning_requests r ON r.tenant_id=w.tenant_id AND r.conversation_id=w.conversation_id AND r.generation=w.generation JOIN memberships m ON m.tenant_id=c.tenant_id AND m.id=c.assignee_id AND m.status='active' WHERE c.tenant_id=$1 AND c.id=$2 AND w.generation=$3 AND c.status='waiting_customer' AND c.controller='human' AND w.assignee_id=c.assignee_id AND w.anchor_reply_at=c.last_eligible_human_reply_at AND (c.last_customer_message_at IS NULL OR c.last_customer_message_at<=w.anchor_reply_at) AND r.state='pending' AND w.warning_state='requested' AND NOT EXISTS(SELECT 1 FROM conversation_snoozes s WHERE s.tenant_id=c.tenant_id AND s.conversation_id=c.id AND s.state='active'))`, tenant, conv, generation).Scan(&live)
	return live, err
}

// Disabling triage also releases existing menus, even without new customer input.
func (a Automation) releaseDisabledTriage(ctx context.Context, tenant uuid.UUID, now time.Time) error {
	type target struct{ conv, inbox uuid.UUID }
	var targets []target
	if err := a.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,inbox_id FROM conversations WHERE tenant_id=$1 AND status='triage' AND controller='menu' ORDER BY id`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t target
			if err = rows.Scan(&t.conv, &t.inbox); err != nil {
				return err
			}
			if !a.TriageInboxes[t.inbox] {
				targets = append(targets, t)
			}
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, t := range targets {
		if err := a.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if err := automationTenant(ctx, tx, tenant); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE conversations SET status='open',version=version+1,updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND status='triage' AND controller='menu'`, tenant, t.conv, now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				return automationPublish(ctx, tx, tenant, t.conv)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
