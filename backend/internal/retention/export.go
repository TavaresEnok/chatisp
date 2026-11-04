package retention

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ExportDemands lists the contact's attendance cycles the caller may see.
// The label states that this file is not the provider or ERP history.
func (s Service) ExportDemands(ctx context.Context, p Principal, contact uuid.UUID) (Export, error) {
	if s.DB == nil {
		return Export{}, ErrUnavailable
	}
	if contact == uuid.Nil {
		return Export{}, ErrInvalid
	}
	var out Export
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := authorize(ctx, tx, p, false, true); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE tenant_id = $1 AND id = $2)`, p.TenantID, contact).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		sectors, err := visibleSectors(ctx, tx, p)
		if err != nil {
			return err
		}
		rows, err := loadDemands(ctx, tx, p, contact, sectors)
		if err != nil {
			return err
		}
		if len(rows) == 0 && p.Role == "agent" {
			var hidden bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM conversations
					WHERE tenant_id = $1 AND contact_id = $2
					  AND sector_id IS NOT NULL
					  AND NOT (sector_id = ANY($3::uuid[]))
				)
			`, p.TenantID, contact, sectors).Scan(&hidden); err != nil {
				return err
			}
			if hidden {
				return ErrForbidden
			}
		}
		if err := insertAudit(ctx, tx, p, "demand.export", "contact", contact, "success"); err != nil {
			return err
		}
		out = Export{Label: DemandLabel, ContactID: contact, Demands: rows}
		return nil
	})
	return out, mapDB(err)
}

// FormatCSV renders the same fields as the JSON export.
func FormatCSV(doc Export) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"rotulo", doc.Label})
	_ = w.Write([]string{"protocolo", "ciclo", "abertura", "encerramento", "motivo", "sintese", "encaminhamento", "autoria"})
	for _, row := range doc.Demands {
		closed := ""
		if row.ClosedAt != nil {
			closed = *row.ClosedAt
		}
		_ = w.Write([]string{
			row.Protocol, strconv.Itoa(row.Cycle), row.OpenedAt, closed,
			row.Reason, row.Synthesis, row.Forwarding, row.Author,
		})
	}
	w.Flush()
	return buf.String()
}

type cycleRow struct {
	conversation uuid.UUID
	protocol     string
	cycle        int
	opened       time.Time
	closed       *time.Time
	reason       string
	synthesis    string
	result       string
	reference    string
	external     string
	kind         string
	author       *uuid.UUID
}

func loadDemands(ctx context.Context, tx pgx.Tx, p Principal, contact uuid.UUID, sectorIDs []uuid.UUID) ([]Demand, error) {
	query := `
		SELECT c.id, c.protocol, cy.cycle_number, cy.opened_at, cy.closed_at,
		       COALESCE(cy.close_reason, ''), COALESCE(cy.close_summary, ''), COALESCE(cy.close_result, ''),
		       COALESCE(cy.external_reference, ''), COALESCE(c.external_protocol, ''),
		       COALESCE(cy.close_author_kind, ''), cy.close_membership_id
		FROM conversation_cycles cy
		JOIN conversations c ON c.tenant_id = cy.tenant_id AND c.id = cy.conversation_id
		WHERE cy.tenant_id = $1 AND c.contact_id = $2
	`
	args := []any{p.TenantID, contact}
	if p.Role == "agent" {
		query += ` AND c.sector_id = ANY($3::uuid[])`
		args = append(args, sectorIDs)
	}
	query += ` ORDER BY cy.opened_at, cy.cycle_number`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cycles []cycleRow
	seen := map[uuid.UUID]struct{}{}
	var ids []uuid.UUID
	for rows.Next() {
		var row cycleRow
		if err := rows.Scan(&row.conversation, &row.protocol, &row.cycle, &row.opened, &row.closed, &row.reason, &row.synthesis, &row.result, &row.reference, &row.external, &row.kind, &row.author); err != nil {
			return nil, err
		}
		cycles = append(cycles, row)
		if _, ok := seen[row.conversation]; !ok {
			seen[row.conversation] = struct{}{}
			ids = append(ids, row.conversation)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reasons, err := triageReasons(ctx, tx, p.TenantID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Demand, 0, len(cycles))
	for _, row := range cycles {
		reason := row.reason
		if reason == "" {
			reason = reasons[row.conversation]
		}
		item := Demand{
			Protocol:   row.protocol,
			Cycle:      row.cycle,
			OpenedAt:   row.opened.UTC().Format(time.RFC3339),
			Reason:     reason,
			Synthesis:  row.synthesis,
			Forwarding: forwarding(row.result, row.reference, row.external),
			Author:     authorOf(row.kind, row.author),
		}
		if row.closed != nil {
			closed := row.closed.UTC().Format(time.RFC3339)
			item.ClosedAt = &closed
		}
		out = append(out, item)
	}
	if out == nil {
		out = []Demand{}
	}
	return out, nil
}

func visibleSectors(ctx context.Context, tx pgx.Tx, p Principal) ([]uuid.UUID, error) {
	var query string
	var args []any
	if p.Role == "agent" {
		query = `
			SELECT sm.sector_id
			FROM sector_memberships sm
			JOIN sectors s ON s.tenant_id = sm.tenant_id AND s.id = sm.sector_id AND s.active
			WHERE sm.tenant_id = $1 AND sm.membership_id = $2
			ORDER BY sm.sector_id
		`
		args = []any{p.TenantID, p.MembershipID}
	} else {
		query = `SELECT id FROM sectors WHERE tenant_id = $1 ORDER BY id`
		args = []any{p.TenantID}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func triageReasons(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, conversations []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(conversations) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT conversation_id, detail
		FROM triage_events
		WHERE tenant_id = $1 AND conversation_id = ANY($2::uuid[])
		ORDER BY created_at, id
	`, tenant, conversations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var detail []byte
		if err := rows.Scan(&id, &detail); err != nil {
			return nil, err
		}
		if reason := reasonFromTriage(detail); reason != "" {
			out[id] = reason
		}
	}
	return out, rows.Err()
}

func reasonFromTriage(detail []byte) string {
	var outcome struct {
		Actions []struct {
			Kind   string `json:"kind"`
			Reason string `json:"reason"`
		} `json:"actions"`
	}
	if json.Unmarshal(detail, &outcome) != nil {
		return ""
	}
	var reason string
	for _, action := range outcome.Actions {
		if action.Kind == "record_reason" && action.Reason != "" {
			reason = action.Reason
		}
	}
	return reason
}

func forwarding(result, reference, externalProtocol string) string {
	if result != "referred_external" && reference == "" && externalProtocol == "" {
		return ""
	}
	text := "referência externa"
	if result == "referred_external" {
		text += "; encaminhado"
	}
	if reference != "" {
		text += "; ciclo " + reference
	}
	if externalProtocol != "" {
		text += "; protocolo externo " + externalProtocol
	}
	return text + ". Não é o histórico do ERP."
}

func authorOf(kind string, id *uuid.UUID) string {
	if id == nil || *id == uuid.Nil || kind == "" {
		return ""
	}
	who := "pessoa"
	if kind == "bot" {
		who = "bot"
	}
	return who + ":" + id.String()
}
