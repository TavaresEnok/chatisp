package triage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"

	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Principal is the panel actor. Tenant and membership come from the session.
type Principal = sectors.Principal

// Service stores the inbox menu. It does not decide a conversation.
type Service struct{ DB *db.Store }

func New(database *db.Store) Service { return Service{DB: database} }

func (s Service) transaction(ctx context.Context, p Principal, inbox uuid.UUID, write bool, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil || (p.Role != "admin" && p.Role != "supervisor") || (write && p.Role != "admin") {
		return sectors.ErrForbidden
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if write {
			if err := s.DB.LockTenant(ctx); err != nil {
				return err
			}
		}
		if err := sectors.RequireInbox(ctx, tx, p, inbox); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func loadMenu(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID) (Menu, error) {
	menu := normalizeMenu(Menu{})
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT version, configured, max_attempts, options FROM triage_menus WHERE tenant_id=$1 AND inbox_id=$2 FOR SHARE`, tenant, inbox).Scan(&menu.Version, &menu.Configured, &menu.MaxAttempts, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return normalizeMenu(Menu{}), nil
	}
	if err != nil {
		return Menu{}, err
	}
	if err = json.Unmarshal(raw, &menu.Options); err != nil {
		return Menu{}, ErrInvalid
	}
	return normalizeMenu(menu), nil
}

// Get returns version 0 when the inbox has never had a menu.
func (s Service) Get(ctx context.Context, p Principal, inbox uuid.UUID) (Menu, error) {
	var menu Menu
	err := s.transaction(ctx, p, inbox, false, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		menu, err = loadMenu(ctx, tx, p.TenantID, inbox)
		return err
	})
	return menu, err
}

// Put replaces the menu. Sector options must be active and linked to the inbox.
func (s Service) Put(ctx context.Context, p Principal, inbox uuid.UUID, cmd Command) (Menu, error) {
	return s.change(ctx, p, inbox, cmd, false)
}

// Delete keeps a tombstone and increments the version. History stays.
func (s Service) Delete(ctx context.Context, p Principal, inbox uuid.UUID, requestID uuid.UUID, expectedVersion int64) (Menu, error) {
	return s.change(ctx, p, inbox, Command{RequestID: requestID, ExpectedVersion: expectedVersion, MaxAttempts: DefaultMaxAttempts, Options: []Option{}}, true)
}

func (s Service) change(ctx context.Context, p Principal, inbox uuid.UUID, cmd Command, remove bool) (Menu, error) {
	var out Menu
	if cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 0 {
		return out, ErrInvalid
	}
	if cmd.Options == nil {
		cmd.Options = []Option{}
	}
	if !remove {
		if err := Validate(cmd.MaxAttempts, cmd.Options); err != nil {
			return out, err
		}
	}
	raw, err := json.Marshal(struct {
		Inbox           uuid.UUID `json:"inbox"`
		Op              string    `json:"op"`
		RequestID       uuid.UUID `json:"request_id"`
		ExpectedVersion int64     `json:"expected_version"`
		MaxAttempts     int       `json:"max_attempts"`
		Options         []Option  `json:"options"`
	}{inbox, opName(remove), cmd.RequestID, cmd.ExpectedVersion, cmd.MaxAttempts, cmd.Options})
	if err != nil {
		return out, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	err = s.transaction(ctx, p, inbox, true, func(ctx context.Context, tx pgx.Tx) error {
		var previous []byte
		var response []byte
		err := tx.QueryRow(ctx, `SELECT request_hash, response FROM triage_commands WHERE tenant_id=$1 AND membership_id=$2 AND request_id=$3`, p.TenantID, p.MembershipID, cmd.RequestID).Scan(&previous, &response)
		if err == nil {
			if !bytes.Equal(previous, hash[:]) {
				return ErrConflict
			}
			return json.Unmarshal(response, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		current, err := loadMenu(ctx, tx, p.TenantID, inbox)
		if err != nil {
			return err
		}
		if current.Version != cmd.ExpectedVersion {
			return ErrConflict
		}
		if !remove {
			if err = lockOptionSectors(ctx, tx, p.TenantID, inbox, cmd.Options); err != nil {
				return err
			}
		}
		out = Menu{Version: current.Version + 1, Configured: !remove, MaxAttempts: cmd.MaxAttempts, Options: cmd.Options}
		if remove {
			out.MaxAttempts = DefaultMaxAttempts
			out.Options = []Option{}
		}
		out = normalizeMenu(out)
		encoded, err := json.Marshal(out.Options)
		if err != nil {
			return ErrInvalid
		}
		if _, err = tx.Exec(ctx, `INSERT INTO triage_menus(tenant_id,inbox_id,version,configured,max_attempts,options) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,inbox_id) DO UPDATE SET version=excluded.version, configured=excluded.configured, max_attempts=excluded.max_attempts, options=excluded.options`, p.TenantID, inbox, out.Version, out.Configured, out.MaxAttempts, encoded); err != nil {
			return err
		}
		action := "triage_menu.put"
		if remove {
			action = "triage_menu.delete"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,$5,'inbox',$6,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, action, inbox); err != nil {
			return err
		}
		response, err = json.Marshal(out)
		if err != nil {
			return ErrInvalid
		}
		_, err = tx.Exec(ctx, `INSERT INTO triage_commands(tenant_id,membership_id,request_id,request_hash,response) VALUES($1,$2,$3,$4,$5)`, p.TenantID, p.MembershipID, cmd.RequestID, hash[:], response)
		return err
	})
	return out, err
}

func opName(remove bool) string {
	if remove {
		return "delete"
	}
	return "put"
}

func lockOptionSectors(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID, options []Option) error {
	ids := make([]uuid.UUID, 0, len(options))
	seen := map[uuid.UUID]bool{}
	for _, option := range options {
		if option.Destination != DestSector || option.SectorID == nil {
			continue
		}
		if seen[*option.SectorID] {
			continue
		}
		seen[*option.SectorID] = true
		ids = append(ids, *option.SectorID)
	}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	for _, id := range ids {
		ok, err := sectorLinked(ctx, tx, tenant, inbox, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalid
		}
	}
	return nil
}

func sectorLinked(ctx context.Context, tx pgx.Tx, tenant, inbox, sector uuid.UUID) (bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT s.id FROM sectors s JOIN sector_inboxes si ON si.tenant_id=s.tenant_id AND si.sector_id=s.id WHERE s.tenant_id=$1 AND si.inbox_id=$2 AND s.id=$3 AND s.active FOR SHARE OF s, si`, tenant, inbox, sector).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
