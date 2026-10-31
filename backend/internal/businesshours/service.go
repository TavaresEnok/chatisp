package businesshours

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Principal = sectors.Principal
type Service struct{ DB *db.Store }

func New(database *db.Store) Service { return Service{DB: database} }

type Command struct {
	RequestID       uuid.UUID `json:"request_id"`
	ExpectedVersion int64     `json:"expected_version"`
	Config          Config    `json:"config"`
}

func (s Service) transaction(ctx context.Context, p Principal, inbox uuid.UUID, write bool, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil || (p.Role != "admin" && p.Role != "supervisor") || (write && p.Role != "admin") {
		return sectors.ErrForbidden
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if write {
			if e := s.DB.LockTenant(ctx); e != nil {
				return e
			}
		}
		if e := sectors.RequireInbox(ctx, tx, p, inbox); e != nil {
			return e
		}
		return fn(ctx, tx)
	})
}
func load(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID) (Configuration, error) {
	c := Configuration{Config: Config{Intervals: []Interval{}}}
	if e := tx.QueryRow(ctx, `SELECT timezone FROM tenants WHERE id=$1 AND status='active'`, tenant).Scan(&c.Timezone); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return c, sectors.ErrForbidden
		}
		return c, e
	}
	if _, e := location(c.Timezone); e != nil {
		return c, e
	}
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT version,configured,config FROM inbox_business_hours WHERE tenant_id=$1 AND inbox_id=$2 FOR SHARE`, tenant, inbox).Scan(&c.Version, &c.Configured, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(raw, &c.Config)
	return c, e
}
func (s Service) Get(ctx context.Context, p Principal, inbox uuid.UUID) (Configuration, error) {
	var c Configuration
	e := s.transaction(ctx, p, inbox, false, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		c, e = load(ctx, tx, p.TenantID, inbox)
		return e
	})
	return c, e
}
func (s Service) Put(ctx context.Context, p Principal, inbox uuid.UUID, cmd Command) (Configuration, error) {
	return s.change(ctx, p, inbox, cmd, false)
}
func (s Service) Delete(ctx context.Context, p Principal, inbox uuid.UUID, cmd Command) (Configuration, error) {
	cmd.Config = Config{Intervals: []Interval{}}
	return s.change(ctx, p, inbox, cmd, true)
}
func (s Service) change(ctx context.Context, p Principal, inbox uuid.UUID, cmd Command, remove bool) (Configuration, error) {
	var out Configuration
	if cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 0 || cmd.ExpectedVersion == int64(^uint64(0)>>1) {
		return out, ErrInvalid
	}
	if e := Validate(cmd.Config); e != nil {
		return out, e
	}
	if cmd.Config.Intervals == nil {
		cmd.Config.Intervals = []Interval{}
	}
	raw, _ := json.Marshal(struct {
		Inbox   uuid.UUID
		Command Command
		Remove  bool
	}{inbox, cmd, remove})
	hash := sha256.Sum256(raw)
	e := s.transaction(ctx, p, inbox, true, func(ctx context.Context, tx pgx.Tx) error {
		var previousHash, response []byte
		e := tx.QueryRow(ctx, `SELECT request_hash,response FROM business_hours_commands WHERE tenant_id=$1 AND membership_id=$2 AND request_id=$3`, p.TenantID, p.MembershipID, cmd.RequestID).Scan(&previousHash, &response)
		if e == nil {
			if !bytes.Equal(previousHash, hash[:]) {
				return ErrConflict
			}
			return json.Unmarshal(response, &out)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		current, e := load(ctx, tx, p.TenantID, inbox)
		if e != nil {
			return e
		}
		if current.Version != cmd.ExpectedVersion {
			return ErrConflict
		}
		if cmd.Config.OnCallSectorID != nil {
			id, e := authorizedSector(ctx, tx, p.TenantID, inbox, cmd.Config.OnCallSectorID, false)
			if e != nil {
				return e
			}
			if id == nil {
				return ErrInvalid
			}
		}
		out = Configuration{Version: current.Version + 1, Configured: !remove, Timezone: current.Timezone, Config: cmd.Config}
		config, _ := json.Marshal(out.Config)
		_, e = tx.Exec(ctx, `INSERT INTO inbox_business_hours(tenant_id,inbox_id,version,configured,config) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,inbox_id) DO UPDATE SET version=excluded.version,configured=excluded.configured,config=excluded.config`, p.TenantID, inbox, out.Version, out.Configured, config)
		if e != nil {
			return e
		}
		action := "business_hours.put"
		if remove {
			action = "business_hours.delete"
		}
		_, e = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,$5,'inbox',$6,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, action, inbox)
		if e != nil {
			return e
		}
		response, _ = json.Marshal(out)
		_, e = tx.Exec(ctx, `INSERT INTO business_hours_commands(tenant_id,membership_id,request_id,request_hash,response) VALUES($1,$2,$3,$4,$5)`, p.TenantID, p.MembershipID, cmd.RequestID, hash[:], response)
		return e
	})
	return out, e
}

// authorizedSector locks the live sector and inbox binding until tx ends.
func authorizedSector(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID, id *uuid.UUID, def bool) (*uuid.UUID, error) {
	var v uuid.UUID
	q := `SELECT s.id FROM sectors s JOIN sector_inboxes si ON si.tenant_id=s.tenant_id AND si.sector_id=s.id WHERE s.tenant_id=$1 AND si.inbox_id=$2 AND s.active AND s.id=$3 FOR SHARE OF s,si`
	var e error
	if def {
		q = `SELECT s.id FROM sectors s JOIN sector_inboxes si ON si.tenant_id=s.tenant_id AND si.sector_id=s.id JOIN inbox_sector_defaults d ON d.tenant_id=si.tenant_id AND d.sector_id=si.sector_id AND d.inbox_id=si.inbox_id WHERE s.tenant_id=$1 AND si.inbox_id=$2 AND s.active FOR SHARE OF s,si,d`
		e = tx.QueryRow(ctx, q, tenant, inbox).Scan(&v)
	} else {
		if id == nil {
			return nil, nil
		}
		e = tx.QueryRow(ctx, q, tenant, inbox, *id).Scan(&v)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &v, nil
}

// EvaluateTx requires an already tenant-scoped transaction. It holds the tenant
// suspension gate and live destination locks until commit. Callers must authorize
// the human principal (if any) in this SAME transaction before using the decision.
// It neither sends, assigns, nor reads capacity, presence or operational clocks.
func EvaluateTx(ctx context.Context, tx pgx.Tx, instant time.Time, tenant, inbox uuid.UUID) (Decision, error) {
	var out Decision
	if tx == nil || tenant == uuid.Nil || inbox == uuid.Nil || instant.IsZero() {
		return out, ErrInvalid
	}
	var scope string
	if e := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id',true),'')`).Scan(&scope); e != nil {
		return out, e
	}
	if scope != tenant.String() {
		return out, sectors.ErrForbidden
	}
	if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'),hashtext($1::text))`, tenant.String()); e != nil {
		return out, e
	}
	var exists uuid.UUID
	if e := tx.QueryRow(ctx, `SELECT id FROM inboxes WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenant, inbox).Scan(&exists); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return out, sectors.ErrNotFound
		}
		return out, e
	}
	c, e := load(ctx, tx, tenant, inbox)
	if e != nil {
		return out, e
	}
	out, e = Evaluate(instant, c)
	if e != nil {
		return out, e
	}
	out.DefaultSectorID, e = authorizedSector(ctx, tx, tenant, inbox, nil, true)
	if e != nil {
		return out, e
	}
	if c.Configured {
		out.OnCallSectorID, e = authorizedSector(ctx, tx, tenant, inbox, c.Config.OnCallSectorID, false)
		if e != nil {
			return out, e
		}
	}
	out.DestinationSectorID = out.DefaultSectorID
	if c.Configured && !out.Open && out.OnCallSectorID != nil {
		out.DestinationSectorID = out.OnCallSectorID
	}
	return out, nil
}
func (s Service) Evaluate(ctx context.Context, p Principal, inbox uuid.UUID, instant time.Time) (Decision, error) {
	var out Decision
	e := s.transaction(ctx, p, inbox, false, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		out, e = EvaluateTx(ctx, tx, instant, p.TenantID, inbox)
		return e
	})
	return out, e
}
