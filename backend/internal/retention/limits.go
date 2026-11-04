package retention

import (
	"bytes"
	"context"
	"encoding/json"
	"math"

	"github.com/jackc/pgx/v5"
)

// PlanUsage reads attendants, numbers and storage against the stored plan.
// Missing caps mean the dimension is not limited. Nothing is deleted.
func (s Service) PlanUsage(ctx context.Context, p Principal) (PlanUsage, error) {
	var out PlanUsage
	err := s.read(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadUsage(ctx, tx, p)
		out = row
		return err
	})
	return out, err
}

func loadUsage(ctx context.Context, tx pgx.Tx, p Principal) (PlanUsage, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT limits FROM tenants WHERE id = $1`, p.TenantID).Scan(&raw); err != nil {
		return PlanUsage{}, err
	}
	agents, numbers, storage, err := caps(raw)
	if err != nil {
		return PlanUsage{}, err
	}
	var out PlanUsage
	out.MaxAgents = agents
	out.MaxNumbers = numbers
	out.MaxStorageBytes = storage
	out.Note = PlanNote
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM memberships WHERE tenant_id = $1 AND role = 'agent' AND status IN ('invited', 'active')),
			(SELECT count(*) FROM inboxes WHERE tenant_id = $1),
			(SELECT COALESCE(sum(expected_bytes), 0) FROM attachments WHERE tenant_id = $1 AND state <> 'failed')
	`, p.TenantID).Scan(&out.Agents, &out.Numbers, &out.StorageBytes); err != nil {
		return PlanUsage{}, err
	}
	return out, nil
}

func caps(raw []byte) (*int, *int, *int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		return nil, nil, nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, nil, nil, ErrInvalid
	}
	agents, err := optionalInt(object, "max_agents")
	if err != nil {
		return nil, nil, nil, err
	}
	numbers, err := optionalInt(object, "max_numbers")
	if err != nil {
		return nil, nil, nil, err
	}
	storage, err := optionalInt64(object, "max_storage_bytes")
	if err != nil {
		return nil, nil, nil, err
	}
	return agents, numbers, storage, nil
}

func optionalInt(object map[string]json.RawMessage, key string) (*int, error) {
	value, ok := object[key]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	parsed, err := decodeInt(value)
	if err != nil || parsed < 0 || parsed > math.MaxInt {
		return nil, ErrInvalid
	}
	n := int(parsed)
	return &n, nil
}

func optionalInt64(object map[string]json.RawMessage, key string) (*int64, error) {
	value, ok := object[key]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	parsed, err := decodeInt(value)
	if err != nil || parsed < 0 {
		return nil, ErrInvalid
	}
	return &parsed, nil
}

func decodeInt(value json.RawMessage) (int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, ErrInvalid
	}
	if decoder.More() {
		return 0, ErrInvalid
	}
	return number.Int64()
}
