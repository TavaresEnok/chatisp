package presence

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

// Service is the tenant-scoped presence API. Now, when set, replaces the clock
// for heartbeats and reads that go through the service. Effective and
// ListAvailable always use the instant passed by the caller.
type Service struct {
	DB  *db.Store
	Now func() time.Time
}

func New(database *db.Store) Service { return Service{DB: database} }

func (s Service) now() time.Time {
	if s.Now != nil {
		return normalize(s.Now())
	}
	return normalize(time.Now())
}

func (s Service) gate(p Principal, session bool) error {
	if s.DB == nil {
		return ErrUnavailable
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return ErrForbidden
	}
	if session && p.SessionID == uuid.Nil {
		return ErrForbidden
	}
	if p.Role != "admin" && p.Role != "supervisor" && p.Role != "agent" {
		return ErrForbidden
	}
	return nil
}

func (s Service) withWrite(ctx context.Context, p Principal, fn func(context.Context, pgx.Tx, time.Time) error) error {
	if err := s.gate(p, true); err != nil {
		return err
	}
	now := s.now()
	if now.IsZero() {
		return ErrInvalid
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			return err
		}
		if _, err := pinSettings(ctx, tx, p.TenantID); err != nil {
			return err
		}
		if err := ensureOperator(ctx, tx, p.TenantID, p.UserID, p.MembershipID); err != nil {
			return err
		}
		ok, err := sessionLive(ctx, tx, p.SessionID, p.TenantID, p.MembershipID, p.UserID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		return fn(ctx, tx, now)
	})
}

// Heartbeat refreshes one tab of the caller's session.
func (s Service) Heartbeat(ctx context.Context, p Principal, tab uuid.UUID) (Snapshot, error) {
	var out Snapshot
	if tab == uuid.Nil {
		return out, ErrInvalid
	}
	err := s.withWrite(ctx, p, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		if _, err := tx.Exec(ctx, `UPDATE presence_operator SET explicit_offline_at=NULL WHERE tenant_id=$1 AND user_id=$2`, p.TenantID, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO presence_connections(tenant_id,session_id,tab_id,user_id,membership_id,heartbeat_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,session_id,tab_id) DO UPDATE SET user_id=EXCLUDED.user_id, membership_id=EXCLUDED.membership_id, heartbeat_at=EXCLUDED.heartbeat_at, expires_at=EXCLUDED.expires_at`, p.TenantID, p.SessionID, tab, p.UserID, p.MembershipID, now, now.Add(HeartbeatTTL)); err != nil {
			return err
		}
		var err error
		out, err = Effective(ctx, tx, p.TenantID, p.UserID, now)
		return err
	})
	return out, err
}

// Close drops one tab. Other live tabs keep the attendant connected.
// Offline starts at this close only when the removed tab is still inside
// its heartbeat and nothing else is live. An already expired tab keeps the
// latest expires_at, and a prior explicit instant stays when it is later.
// Deleting nothing does not create or move that instant.
func (s Service) Close(ctx context.Context, p Principal, tab uuid.UUID) (Snapshot, error) {
	var out Snapshot
	if tab == uuid.Nil {
		return out, ErrInvalid
	}
	err := s.withWrite(ctx, p, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		var removedExp time.Time
		err := tx.QueryRow(ctx, `DELETE FROM presence_connections WHERE tenant_id=$1 AND session_id=$2 AND tab_id=$3 AND user_id=$4 RETURNING expires_at`, p.TenantID, p.SessionID, tab, p.UserID).Scan(&removedExp)
		removed := err == nil
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT session_id, membership_id, expires_at FROM presence_connections WHERE tenant_id=$1 AND user_id=$2 ORDER BY session_id, tab_id`, p.TenantID, p.UserID)
		if err != nil {
			return err
		}
		type rest struct {
			session, member uuid.UUID
			exp             time.Time
		}
		var left []rest
		for rows.Next() {
			var item rest
			if err = rows.Scan(&item.session, &item.member, &item.exp); err != nil {
				rows.Close()
				return err
			}
			left = append(left, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		live := false
		seen := map[uuid.UUID]bool{}
		for _, item := range left {
			if !item.exp.After(now) || seen[item.session] {
				continue
			}
			seen[item.session] = true
			ok, err := sessionLive(ctx, tx, item.session, p.TenantID, item.member, p.UserID, now)
			if err != nil {
				return err
			}
			if ok {
				live = true
				break
			}
		}
		if live {
			_, err = tx.Exec(ctx, `UPDATE presence_operator SET explicit_offline_at=NULL WHERE tenant_id=$1 AND user_id=$2`, p.TenantID, p.UserID)
		} else if removed {
			start := normalize(removedExp)
			if start.After(now) {
				start = now
			} else {
				for _, item := range left {
					exp := normalize(item.exp)
					if exp.After(start) {
						start = exp
					}
				}
				var prior *time.Time
				if err = tx.QueryRow(ctx, `SELECT explicit_offline_at FROM presence_operator WHERE tenant_id=$1 AND user_id=$2`, p.TenantID, p.UserID).Scan(&prior); err != nil {
					return err
				}
				if prior != nil {
					ex := normalize(*prior)
					if ex.After(start) {
						start = ex
					}
				}
			}
			_, err = tx.Exec(ctx, `UPDATE presence_operator SET explicit_offline_at=$3 WHERE tenant_id=$1 AND user_id=$2`, p.TenantID, p.UserID, start)
		}
		if err != nil {
			return err
		}
		out, err = Effective(ctx, tx, p.TenantID, p.UserID, now)
		return err
	})
	return out, err
}

// SetManual stores available or busy. Busy wins over a live heartbeat.
// The same request id and body replay the stored response.
func (s Service) SetManual(ctx context.Context, p Principal, cmd ManualCommand) (Snapshot, error) {
	var out Snapshot
	if cmd.RequestID == uuid.Nil || (cmd.State != ManualAvailable && cmd.State != ManualBusy) {
		return out, ErrInvalid
	}
	sum := hashCommand(struct {
		Op    string `json:"op"`
		State string `json:"state"`
	}{"presence.manual", cmd.State})
	err := s.withWrite(ctx, p, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		hit, err := readCommand(ctx, tx, p, cmd.RequestID, sum, &out)
		if err != nil || hit {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE presence_operator SET manual=$3, version=version+1 WHERE tenant_id=$1 AND user_id=$2`, p.TenantID, p.UserID, cmd.State)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		out, err = Effective(ctx, tx, p.TenantID, p.UserID, now)
		if err != nil {
			return err
		}
		return finishCommand(ctx, tx, p, "presence.manual", "user", p.UserID, cmd.RequestID, sum, out)
	})
	return out, err
}

// Current reads the caller's aggregate at the service clock.
func (s Service) Current(ctx context.Context, p Principal) (Snapshot, error) {
	var out Snapshot
	if err := s.gate(p, true); err != nil {
		return out, err
	}
	now := s.now()
	if now.IsZero() {
		return out, ErrInvalid
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			return err
		}
		var err error
		out, err = Effective(ctx, tx, p.TenantID, p.UserID, now)
		if err != nil {
			return err
		}
		ok, err := sessionLive(ctx, tx, p.SessionID, p.TenantID, p.MembershipID, p.UserID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		return nil
	})
	return out, err
}

// GetSettings returns the tenant grace, or the default when unset.
func (s Service) GetSettings(ctx context.Context, p Principal) (Settings, error) {
	var out Settings
	if err := s.gate(p, true); err != nil {
		return out, err
	}
	now := s.now()
	if now.IsZero() {
		return out, ErrInvalid
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			return err
		}
		var err error
		out, err = pinSettings(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		ok, err := sessionLive(ctx, tx, p.SessionID, p.TenantID, p.MembershipID, p.UserID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		return nil
	})
	return out, err
}

// PutTolerance replaces the offline grace. It does not unassign conversations.
func (s Service) PutTolerance(ctx context.Context, p Principal, cmd ToleranceCommand) (Settings, error) {
	var out Settings
	maxSeconds := int(MaxOfflineTolerance / time.Second)
	if cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 0 || cmd.OfflineToleranceSeconds < 0 || cmd.OfflineToleranceSeconds > maxSeconds {
		return out, ErrInvalid
	}
	sum := hashCommand(struct {
		Op       string `json:"op"`
		Seconds  int    `json:"seconds"`
		Expected int64  `json:"expected"`
	}{"presence.tolerance", cmd.OfflineToleranceSeconds, cmd.ExpectedVersion})
	err := s.withAdmin(ctx, p, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		var version int64
		err := tx.QueryRow(ctx, `SELECT version FROM presence_settings WHERE tenant_id=$1 FOR UPDATE`, p.TenantID).Scan(&version)
		missing := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !missing {
			return err
		}
		ok, err := sessionLive(ctx, tx, p.SessionID, p.TenantID, p.MembershipID, p.UserID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		hit, err := readCommand(ctx, tx, p, cmd.RequestID, sum, &out)
		if err != nil || hit {
			return err
		}
		if missing {
			if cmd.ExpectedVersion != 0 {
				return ErrConflict
			}
			if _, err = tx.Exec(ctx, `INSERT INTO presence_settings(tenant_id,offline_tolerance_seconds,version) VALUES($1,$2,1)`, p.TenantID, cmd.OfflineToleranceSeconds); err != nil {
				return err
			}
			version = 1
		} else {
			if version != cmd.ExpectedVersion {
				return ErrConflict
			}
			tag, err := tx.Exec(ctx, `UPDATE presence_settings SET offline_tolerance_seconds=$3, version=version+1 WHERE tenant_id=$1 AND version=$2`, p.TenantID, cmd.ExpectedVersion, cmd.OfflineToleranceSeconds)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrConflict
			}
			version++
		}
		out = Settings{Version: version, Configured: true, OfflineToleranceSeconds: cmd.OfflineToleranceSeconds}
		return finishCommand(ctx, tx, p, "presence.tolerance", "tenant", p.TenantID, cmd.RequestID, sum, out)
	})
	return out, err
}

func (s Service) withAdmin(ctx context.Context, p Principal, fn func(context.Context, pgx.Tx, time.Time) error) error {
	if err := s.gate(p, true); err != nil {
		return err
	}
	if p.Role != "admin" {
		return ErrForbidden
	}
	now := s.now()
	if now.IsZero() {
		return ErrInvalid
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.DB.LockTenant(ctx); err != nil {
			return err
		}
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			return err
		}
		return fn(ctx, tx, now)
	})
}

// List reads assignable attendants of one active sector for admin or supervisor.
func (s Service) List(ctx context.Context, p Principal, sector uuid.UUID) ([]Snapshot, error) {
	if err := s.gate(p, true); err != nil {
		return nil, err
	}
	if p.Role != "admin" && p.Role != "supervisor" {
		return nil, ErrForbidden
	}
	if sector == uuid.Nil {
		return nil, ErrInvalid
	}
	now := s.now()
	if now.IsZero() {
		return nil, ErrInvalid
	}
	var out []Snapshot
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := pinScope(ctx, tx, p.TenantID); err != nil {
			return err
		}
		var active bool
		err := tx.QueryRow(ctx, `SELECT active FROM sectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, sector).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = ListAvailable(ctx, tx, p.TenantID, sector, now)
		if err != nil {
			return err
		}
		if err = sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			return err
		}
		ok, err := sessionLive(ctx, tx, p.SessionID, p.TenantID, p.MembershipID, p.UserID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Snapshot{}
	}
	return out, nil
}

func hashCommand(v any) []byte {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return sum[:]
}

func readCommand(ctx context.Context, tx pgx.Tx, p Principal, request uuid.UUID, sum []byte, dest any) (bool, error) {
	var previous, response []byte
	err := tx.QueryRow(ctx, `SELECT request_hash, response FROM presence_commands WHERE tenant_id=$1 AND membership_id=$2 AND request_id=$3`, p.TenantID, p.MembershipID, request).Scan(&previous, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(previous, sum) {
		return false, ErrConflict
	}
	if err = json.Unmarshal(response, dest); err != nil {
		return false, err
	}
	return true, nil
}

func finishCommand(ctx context.Context, tx pgx.Tx, p Principal, action, targetType string, target, request uuid.UUID, sum []byte, body any) error {
	response, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,$5,$6,$7,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, action, targetType, target); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO presence_commands(tenant_id,membership_id,request_id,request_hash,response) VALUES($1,$2,$3,$4,$5)`, p.TenantID, p.MembershipID, request, sum, response)
	return err
}
