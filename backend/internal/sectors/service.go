package sectors

import (
	"context"
	"errors"
	"strings"

	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrForbidden = errors.New("acesso negado")
	ErrNotFound  = errors.New("registro não encontrado")
	ErrInvalid   = errors.New("comando inválido")
	ErrConflict  = errors.New("conflito")
)

type Principal struct {
	TenantID, UserID, MembershipID uuid.UUID
	Role                           string
	IsSuperadmin                   bool
}
type Sector struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Active        bool      `json:"active"`
	LegacyDefault bool      `json:"legacy_default"`
}
type Details struct {
	MembershipIDs   []uuid.UUID `json:"membership_ids"`
	InboxIDs        []uuid.UUID `json:"inbox_ids"`
	DefaultInboxIDs []uuid.UUID `json:"default_inbox_ids"`
}
type Service struct{ DB *db.Store }

func New(database *db.Store) Service { return Service{DB: database} }

// ValidatePrincipal must run in the tenant-scoped transaction used for the operation.
func ValidatePrincipal(ctx context.Context, tx pgx.Tx, p Principal) error {
	if tx == nil || p.IsSuperadmin || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return ErrForbidden
	}
	// Shared side of the tenant status gate used by db.Store.LockTenant/SetTenantStatus.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, p.TenantID.String()); err != nil {
		return err
	}
	var role string
	err := tx.QueryRow(ctx, `SELECT m.role FROM memberships m JOIN tenants t ON t.id=m.tenant_id WHERE m.tenant_id=$1 AND m.id=$2 AND m.user_id=$3 AND m.status='active' AND t.status='active' FOR SHARE OF m`, p.TenantID, p.MembershipID, p.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if role != p.Role || (role != "admin" && role != "supervisor" && role != "agent") {
		return ErrForbidden
	}
	return nil
}
func (s Service) tx(ctx context.Context, p Principal, admin bool, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrForbidden
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if admin {
			// Exclusive tenant gate before validation, held until commit, ordering mutations against suspension.
			if err := s.DB.LockTenant(ctx); err != nil {
				return err
			}
		}
		if err := ValidatePrincipal(ctx, tx, p); err != nil {
			return err
		}
		if admin && p.Role != "admin" {
			return ErrForbidden
		}
		return translate(fn(ctx, tx))
	})
}
func translate(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "23503", "23505", "23514":
			return ErrConflict
		case "22P02":
			return ErrInvalid
		}
	}
	return err
}
func (s Service) List(ctx context.Context, p Principal) ([]Sector, error) {
	out := []Sector{}
	err := s.tx(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT s.id,s.name,s.active,s.legacy_default FROM sectors s WHERE s.tenant_id=$1 AND ($2 IN ('admin','supervisor') OR (s.active AND EXISTS (SELECT 1 FROM sector_memberships sm WHERE sm.tenant_id=s.tenant_id AND sm.sector_id=s.id AND sm.membership_id=$3))) ORDER BY s.name,s.id`, p.TenantID, p.Role, p.MembershipID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v Sector
			if e = rows.Scan(&v.ID, &v.Name, &v.Active, &v.LegacyDefault); e != nil {
				return e
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

// Details returns relationships for a sector visible to the caller.
func (s Service) Details(ctx context.Context, p Principal, sectorID uuid.UUID) (Details, error) {
	out := Details{MembershipIDs: []uuid.UUID{}, InboxIDs: []uuid.UUID{}, DefaultInboxIDs: []uuid.UUID{}}
	err := s.tx(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		var visible bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sectors s WHERE s.tenant_id=$1 AND s.id=$2 AND ($3 IN ('admin','supervisor') OR (s.active AND EXISTS (SELECT 1 FROM sector_memberships sm WHERE sm.tenant_id=s.tenant_id AND sm.sector_id=s.id AND sm.membership_id=$4))))`, p.TenantID, sectorID, p.Role, p.MembershipID).Scan(&visible); e != nil {
			return e
		}
		if !visible {
			return ErrNotFound
		}
		queries := []struct {
			q    string
			dest *[]uuid.UUID
		}{
			{`SELECT membership_id FROM sector_memberships WHERE tenant_id=$1 AND sector_id=$2 AND ($3 IN ('admin','supervisor') OR membership_id=$4) ORDER BY membership_id`, &out.MembershipIDs},
			{`SELECT inbox_id FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND $3::text <> '' AND $4::uuid IS NOT NULL ORDER BY inbox_id`, &out.InboxIDs},
			{`SELECT inbox_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND sector_id=$2 AND $3::text <> '' AND $4::uuid IS NOT NULL ORDER BY inbox_id`, &out.DefaultInboxIDs},
		}
		for _, item := range queries {
			rows, e := tx.Query(ctx, item.q, p.TenantID, sectorID, p.Role, p.MembershipID)
			if e != nil {
				return e
			}
			for rows.Next() {
				var id uuid.UUID
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return e
				}
				*item.dest = append(*item.dest, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
		}
		return nil
	})
	return out, err
}
func (s Service) Create(ctx context.Context, p Principal, name string) (Sector, error) {
	name = strings.TrimSpace(name)
	v := Sector{ID: uuid.New(), Name: name, Active: true}
	if len(name) == 0 || len([]rune(name)) > 120 {
		return Sector{}, ErrInvalid
	}
	err := s.tx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,$3)`, p.TenantID, v.ID, name); e != nil {
			return e
		}
		return audit(ctx, tx, p, "sector.create", "sector", v.ID)
	})
	return v, err
}
func (s Service) Update(ctx context.Context, p Principal, id uuid.UUID, name *string, active *bool) error {
	if id == uuid.Nil || (name == nil && active == nil) {
		return ErrInvalid
	}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || len([]rune(n)) > 120 {
			return ErrInvalid
		}
		name = &n
	}
	return s.tx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		var oldActive bool
		e := tx.QueryRow(ctx, `SELECT active FROM sectors WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, id).Scan(&oldActive)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if active != nil && !*active {
			var used bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbox_sector_defaults WHERE tenant_id=$1 AND sector_id=$2)`, p.TenantID, id).Scan(&used); e != nil {
				return e
			}
			if used {
				return ErrConflict
			}
		}
		if _, e = tx.Exec(ctx, `UPDATE sectors SET name=COALESCE($3,name),active=COALESCE($4,active) WHERE tenant_id=$1 AND id=$2`, p.TenantID, id, name, active); e != nil {
			return e
		}
		return audit(ctx, tx, p, "sector.update", "sector", id)
	})
}

// Bind changes one membership or inbox relationship; adding requires active targets.
func (s Service) Bind(ctx context.Context, p Principal, sectorID, targetID uuid.UUID, kind string, add bool) error {
	if sectorID == uuid.Nil || targetID == uuid.Nil || (kind != "membership" && kind != "inbox") {
		return ErrInvalid
	}
	return s.tx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sectors WHERE tenant_id=$1 AND id=$2 AND active)`, p.TenantID, sectorID).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
		var q string
		if kind == "membership" {
			if add {
				q = `INSERT INTO sector_memberships SELECT $1,$2,m.id FROM memberships m WHERE m.tenant_id=$1 AND m.id=$3 AND m.status='active'`
			} else {
				q = `DELETE FROM sector_memberships WHERE tenant_id=$1 AND sector_id=$2 AND membership_id=$3`
			}
		} else {
			if add {
				q = `INSERT INTO sector_inboxes SELECT $1,$2,i.id FROM inboxes i WHERE i.tenant_id=$1 AND i.id=$3`
			} else {
				q = `DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3`
			}
		}
		tag, e := tx.Exec(ctx, q, p.TenantID, sectorID, targetID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		action := "remove"
		if add {
			action = "add"
		}
		return audit(ctx, tx, p, "sector."+kind+"."+action+" sector="+sectorID.String(), kind, targetID)
	})
}
func (s Service) SetDefault(ctx context.Context, p Principal, sectorID, inboxID uuid.UUID) error {
	if sectorID == uuid.Nil || inboxID == uuid.Nil {
		return ErrInvalid
	}
	return s.tx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		var active bool
		if e := tx.QueryRow(ctx, `SELECT active FROM sectors WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, sectorID).Scan(&active); errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		} else if e != nil {
			return e
		}
		if !active {
			return ErrConflict
		}
		var prev string
		switch e := tx.QueryRow(ctx, `SELECT sector_id::text FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2 FOR UPDATE`, p.TenantID, inboxID).Scan(&prev); {
		case errors.Is(e, pgx.ErrNoRows):
			prev = "none"
		case e != nil:
			return e
		}
		tag, e := tx.Exec(ctx, `INSERT INTO inbox_sector_defaults(tenant_id,inbox_id,sector_id) SELECT si.tenant_id,si.inbox_id,si.sector_id FROM sector_inboxes si JOIN sectors s ON s.tenant_id=si.tenant_id AND s.id=si.sector_id AND s.active WHERE si.tenant_id=$1 AND si.sector_id=$2 AND si.inbox_id=$3 ON CONFLICT (tenant_id,inbox_id) DO UPDATE SET sector_id=excluded.sector_id`, p.TenantID, sectorID, inboxID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return audit(ctx, tx, p, "sector.default sector="+sectorID.String()+" previous="+prev, "inbox", inboxID)
	})
}
func audit(ctx context.Context, tx pgx.Tx, p Principal, action, targetType string, target uuid.UUID) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,$5,$7,$6,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, action, target, targetType)
	return e
}
