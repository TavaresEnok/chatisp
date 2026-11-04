package erp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// InsertConnection stores the single fake connection of the tenant.
func InsertConnection(ctx context.Context, tx pgx.Tx, conn Connection) error {
	if tx == nil || conn.TenantID == uuid.Nil || conn.ID == uuid.Nil || conn.Driver != DriverFake || !conn.Simulated {
		return ErrInvalid
	}
	if conn.Availability == "" {
		conn.Availability = AvailabilityUp
	}
	names := make([]string, 0, len(conn.Capabilities))
	for name, on := range conn.Capabilities {
		if on {
			if !knownCapability(name) {
				return ErrInvalid
			}
			names = append(names, name)
		}
	}
	var sector any
	if conn.SectorID != uuid.Nil {
		sector = conn.SectorID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO erp_connections (tenant_id, id, driver, simulated, enabled, availability, sector_id, capabilities)
		VALUES ($1, $2, 'fake', true, $3, $4, $5, $6)`,
		conn.TenantID, conn.ID, conn.Enabled, conn.Availability, sector, names)
	return mapPG(err)
}

// LoadConnection reads the tenant connection under the current row security scope.
func LoadConnection(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Connection, error) {
	var conn Connection
	var names []string
	var sector *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT tenant_id, id, driver, simulated, enabled, availability, sector_id, capabilities
		FROM erp_connections
		WHERE tenant_id = $1`, tenant).Scan(
		&conn.TenantID, &conn.ID, &conn.Driver, &conn.Simulated, &conn.Enabled, &conn.Availability, &sector, &names)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, mapPG(err)
	}
	if sector != nil {
		conn.SectorID = *sector
	}
	conn.Capabilities = map[string]bool{}
	for _, name := range names {
		conn.Capabilities[name] = true
	}
	return conn, nil
}

// CommitAction inserts the action once. The same key with another hash conflicts.
func CommitAction(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	if tx == nil || tenant == uuid.Nil || action.ID == uuid.Nil || action.LocalKey == "" || action.ConnectionID == uuid.Nil {
		return ErrInvalid
	}
	if action.Outcome != OutcomeOK && action.Outcome != OutcomeUncertain {
		return nil
	}
	var conversation any
	if action.Conversation != uuid.Nil {
		conversation = action.Conversation
	}
	var ref any
	if action.ExternalRef != "" {
		ref = action.ExternalRef
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO erp_actions (
			tenant_id, id, connection_id, conversation_id, capability, local_key, request_hash, effect, outcome, external_ref
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		tenant, action.ID, action.ConnectionID, conversation, action.Capability, action.LocalKey, action.Hash[:], action.Effect, action.Outcome, ref)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			prior, loadErr := LoadActionByKey(ctx, tx, tenant, action.LocalKey)
			if loadErr != nil {
				return loadErr
			}
			if prior.Hash != action.Hash {
				return ErrConflict
			}
			return nil
		}
		return mapPG(err)
	}
	if action.Conversation != uuid.Nil {
		if err = insertTimeline(ctx, tx, tenant, action); err != nil {
			return err
		}
	}
	return insertAudit(ctx, tx, tenant, action)
}

func insertTimeline(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO conversation_events (
			tenant_id, id, conversation_id, cycle_number, kind, actor_id, at, version, controller_epoch, controller, assignee_id
		)
		SELECT tenant_id, $3, id, cycle_number, 'erp_action', NULL, now(), version, controller_epoch, controller, assignee_id
		FROM conversations
		WHERE tenant_id = $1 AND id = $2`, tenant, action.Conversation, action.ID)
	if err != nil {
		return mapPG(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (id, tenant_id, action, target_type, target_id, result, correlation_id)
		VALUES ($1, $2, $3, 'erp_action', $4, $5, $6)`,
		uuid.New(), tenant, "erp."+action.Capability, action.ID, action.Outcome, action.LocalKey)
	return mapPG(err)
}

// LoadActionByKey reads the stored action for a local key.
func LoadActionByKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, localKey string) (Action, error) {
	var action Action
	var hash []byte
	var ref *string
	var conversation *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id, connection_id, conversation_id, capability, local_key, request_hash, effect, outcome, external_ref
		FROM erp_actions
		WHERE tenant_id = $1 AND local_key = $2`, tenant, localKey).Scan(
		&action.ID, &action.ConnectionID, &conversation, &action.Capability, &action.LocalKey, &hash, &action.Effect, &action.Outcome, &ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	if err != nil {
		return Action{}, mapPG(err)
	}
	if conversation != nil {
		action.Conversation = *conversation
	}
	if ref != nil {
		action.ExternalRef = *ref
	}
	if len(hash) != sha256Len {
		return Action{}, ErrInvalid
	}
	copy(action.Hash[:], hash)
	action.View = View{
		Driver: DriverFake, Simulated: true, Capability: action.Capability,
		Outcome: action.Outcome, Effect: action.Effect, ExternalRef: action.ExternalRef,
	}
	return action, nil
}

const sha256Len = 32

// InsertLink stores an explicit customer binding.
func InsertLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, link Link) error {
	if link.ID == uuid.Nil || link.ContactID == uuid.Nil || link.ExternalCustomerID == "" {
		return ErrInvalid
	}
	var conversation any
	if link.ConversationID != uuid.Nil {
		conversation = link.ConversationID
	}
	var contract any
	if link.ExternalContractID != "" {
		contract = link.ExternalContractID
	}
	var protocol any
	if link.ExternalProtocol != "" {
		protocol = link.ExternalProtocol
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO erp_links (
			tenant_id, id, contact_id, conversation_id, external_customer_id, external_contract_id, external_protocol, selected
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		tenant, link.ID, link.ContactID, conversation, link.ExternalCustomerID, contract, protocol, link.Selected)
	return mapPG(err)
}

// ListLinks returns the bindings of one contact.
func ListLinks(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID) ([]Link, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, contact_id, conversation_id, external_customer_id, external_contract_id, external_protocol, selected
		FROM erp_links
		WHERE tenant_id = $1 AND contact_id = $2
		ORDER BY id`, tenant, contact)
	if err != nil {
		return nil, mapPG(err)
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var link Link
		var conversation *uuid.UUID
		var contract, protocol *string
		if err := rows.Scan(&link.ID, &link.ContactID, &conversation, &link.ExternalCustomerID, &contract, &protocol, &link.Selected); err != nil {
			return nil, mapPG(err)
		}
		if conversation != nil {
			link.ConversationID = *conversation
		}
		if contract != nil {
			link.ExternalContractID = *contract
		}
		if protocol != nil {
			link.ExternalProtocol = *protocol
		}
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPG(err)
	}
	return out, nil
}

func mapPG(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return errors.New("falha ao gravar o contrato do erp")
}
