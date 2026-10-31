package collision

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const TTL = 30 * time.Second

var ErrForbidden = errors.New("acesso negado")
var ErrInvalid = errors.New("identificador inválido")

type Service struct {
	DB  *db.Store
	Now func() time.Time
}
type Indicator struct {
	MembershipID uuid.UUID `json:"membership_id"`
	Typing       bool      `json:"typing"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func sectorPrincipal(p auth.Principal) sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}
func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func live(ctx context.Context, tx pgx.Tx, tenant, member, session uuid.UUID, now time.Time) (bool, error) {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT collision_live_session($1,$2,$3,$4)`, session, tenant, member, now).Scan(&ok)
	return ok, e
}
func (s Service) run(ctx context.Context, p auth.Principal, inbox, conversation uuid.UUID, fn func(context.Context, pgx.Tx, time.Time) error) error {
	if s.DB == nil || p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil || p.SessionID == uuid.Nil {
		return ErrForbidden
	}
	if inbox == uuid.Nil || conversation == uuid.Nil {
		return ErrInvalid
	}
	now := s.now()
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireConversation(ctx, tx, sectorPrincipal(p), inbox, conversation); err != nil {
			return err
		}
		ok, err := live(ctx, tx, p.TenantID, p.MembershipID, p.SessionID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		// Bound opportunistic cleanup to 100 expired rows per request and tenant.
		if _, err = tx.Exec(ctx, `DELETE FROM collision_indicators WHERE ctid IN (SELECT ctid FROM collision_indicators WHERE tenant_id=$1 AND expires_at <= $2 ORDER BY expires_at LIMIT 100 FOR UPDATE SKIP LOCKED)`, p.TenantID, now); err != nil {
			return err
		}
		return fn(ctx, tx, now)
	})
}
func (s Service) Heartbeat(ctx context.Context, p auth.Principal, inbox, conversation, tab uuid.UUID, typing bool) error {
	if tab == uuid.Nil {
		return ErrInvalid
	}
	return s.run(ctx, p, inbox, conversation, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx, `INSERT INTO collision_indicators (tenant_id,conversation_id,membership_id,session_id,tab_id,typing,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (tenant_id,conversation_id,membership_id,session_id,tab_id) DO UPDATE SET typing=EXCLUDED.typing,expires_at=EXCLUDED.expires_at`, p.TenantID, conversation, p.MembershipID, p.SessionID, tab, typing, now.Add(TTL))
		return err
	})
}
func (s Service) Release(ctx context.Context, p auth.Principal, inbox, conversation, tab uuid.UUID) error {
	if tab == uuid.Nil {
		return ErrInvalid
	}
	return s.run(ctx, p, inbox, conversation, func(ctx context.Context, tx pgx.Tx, _ time.Time) error {
		_, err := tx.Exec(ctx, `DELETE FROM collision_indicators WHERE tenant_id=$1 AND conversation_id=$2 AND membership_id=$3 AND session_id=$4 AND tab_id=$5`, p.TenantID, conversation, p.MembershipID, p.SessionID, tab)
		return err
	})
}
func (s Service) List(ctx context.Context, p auth.Principal, inbox, conversation uuid.UUID) ([]Indicator, error) {
	out := []Indicator{}
	err := s.run(ctx, p, inbox, conversation, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		rows, err := tx.Query(ctx, `SELECT ci.membership_id,ci.session_id,ci.typing,ci.expires_at,m.user_id,m.role FROM collision_indicators ci JOIN memberships m ON m.tenant_id=ci.tenant_id AND m.id=ci.membership_id WHERE ci.tenant_id=$1 AND ci.conversation_id=$2 AND ci.expires_at>$3 AND m.status='active' ORDER BY ci.membership_id,ci.session_id,ci.tab_id`, p.TenantID, conversation, now)
		if err != nil {
			return err
		}
		type candidate struct {
			member, session, user uuid.UUID
			role                  string
			typing                bool
			expires               time.Time
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.member, &c.session, &c.typing, &c.expires, &c.user, &c.role); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		byMember := map[uuid.UUID]int{}
		for _, c := range candidates {
			ok, e := live(ctx, tx, p.TenantID, c.member, c.session, now)
			if e != nil {
				return e
			}
			if !ok {
				continue
			}
			e = sectors.RequireConversation(ctx, tx, sectors.Principal{TenantID: p.TenantID, UserID: c.user, MembershipID: c.member, Role: c.role}, inbox, conversation)
			if e != nil {
				if errors.Is(e, sectors.ErrForbidden) {
					continue
				}
				return e
			}
			if i, ok := byMember[c.member]; ok {
				out[i].Typing = out[i].Typing || c.typing
				if c.expires.After(out[i].ExpiresAt) {
					out[i].ExpiresAt = c.expires
				}
			} else {
				byMember[c.member] = len(out)
				out = append(out, Indicator{c.member, c.typing, c.expires})
			}
		}
		return nil
	})
	return out, err
}
