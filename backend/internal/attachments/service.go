package attachments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/urlguard"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const UploadTTL = 30 * time.Minute
const DownloadTTL = 60 * time.Second

// Hard ceiling bounds service memory even when the configured plan is invalid.
const MaxFileBytes int64 = 32 << 20

type Limits struct{ PlanBytes, IncomingReserveBytes, FileBytes int64 }

// ResolveLimits must read trusted plan/operational configuration; never request
// input. Zero/missing configuration fails closed. No commercial defaults exist.
type ResolveLimits func(context.Context, pgx.Tx, uuid.UUID) (Limits, error)
type Service struct {
	DB      *db.Store
	Storage Storage
	Limits  ResolveLimits
	Now     func() time.Time
}
type Record struct {
	ID                              uuid.UUID `json:"id"`
	State                           string    `json:"state"`
	Name                            string    `json:"name"`
	Bytes                           int64     `json:"bytes"`
	MediaType                       string    `json:"media_type"`
	ExpiresAt                       time.Time `json:"expires_at"`
	key, version, direction, digest string
}
type RegisterInput struct {
	RequestID uuid.UUID
	Name      string
	Bytes     int64
	Direction string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) run(ctx context.Context, p auth.Principal, inbox, conv uuid.UUID, fn func(context.Context, pgx.Tx) error) error {
	if s == nil || s.DB == nil || s.Storage == nil || p.SessionID == uuid.Nil || p.IsSuperadmin || p.EnrollmentOnly || inbox == uuid.Nil || conv == uuid.Nil {
		return ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return s.DB.WithTenantTx(ctx, p.TenantID, func(c context.Context, tx pgx.Tx) error {
		if e := sectors.RequireConversation(c, tx, sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, inbox, conv); e != nil {
			return e
		}
		// Existing narrow security-definer gate checks active user and session,
		// locks in user->session order, and never exposes global user data.
		var live bool
		if e := tx.QueryRow(c, `SELECT collision_live_session($1,$2,$3,$4)`, p.SessionID, p.TenantID, p.MembershipID, s.now()).Scan(&live); e != nil {
			return e
		}
		if !live {
			return ErrForbidden
		}
		return fn(c, tx)
	})
}
func load(ctx context.Context, tx pgx.Tx, tenant, conv, id uuid.UUID) (Record, error) {
	var r Record
	e := tx.QueryRow(ctx, `SELECT id,state,name,expected_bytes,media_type,expires_at,object_key,object_version,direction,sha256 FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3 FOR UPDATE`, tenant, conv, id).Scan(&r.ID, &r.State, &r.Name, &r.Bytes, &r.MediaType, &r.ExpiresAt, &r.key, &r.version, &r.direction, &r.digest)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrForbidden
	}
	return r, e
}
func (s *Service) Register(ctx context.Context, p auth.Principal, inbox, conv uuid.UUID, in RegisterInput) (Record, error) {
	var out Record
	name, e := safeName(in.Name)
	if e != nil || in.RequestID == uuid.Nil || in.Bytes <= 0 || in.Bytes > MaxFileBytes || (in.Direction != "incoming" && in.Direction != "outgoing") {
		return out, ErrInvalid
	}
	e = s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		if s.Limits == nil {
			return ErrLimit
		}
		l, e := s.Limits(c, tx, p.TenantID)
		if e != nil {
			return e
		}
		if l.PlanBytes <= 0 || l.IncomingReserveBytes < 0 || l.FileBytes <= 0 || l.FileBytes > MaxFileBytes || in.Bytes > l.FileBytes || l.PlanBytes >= 1<<62 || l.IncomingReserveBytes >= 1<<62 {
			return ErrLimit
		}
		if _, e = tx.Exec(c, `INSERT INTO attachment_budgets(tenant_id,namespace) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.TenantID, uuid.New()); e != nil {
			return e
		}
		var namespace uuid.UUID
		var used int64
		if e = tx.QueryRow(c, `SELECT namespace,reserved_bytes FROM attachment_budgets WHERE tenant_id=$1 FOR UPDATE`, p.TenantID).Scan(&namespace, &used); e != nil {
			return e
		}
		var id uuid.UUID
		e = tx.QueryRow(c, `SELECT id FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND request_id=$3`, p.TenantID, conv, in.RequestID).Scan(&id)
		if e == nil {
			out, e = load(c, tx, p.TenantID, conv, id)
			if e != nil {
				return e
			}
			if out.Name != name || out.Bytes != in.Bytes || out.direction != in.Direction {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		budget := l.PlanBytes
		if in.Direction == "incoming" {
			budget += l.IncomingReserveBytes
		}
		if in.Bytes > budget || used > budget-in.Bytes {
			return ErrLimit
		}
		out = Record{ID: uuid.New(), State: "upload", Name: name, Bytes: in.Bytes, ExpiresAt: s.now().Add(UploadTTL)}
		key := namespace.String() + "/" + uuid.NewString()
		if _, e = tx.Exec(c, `INSERT INTO attachments(tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.TenantID, out.ID, conv, in.RequestID, in.Direction, name, in.Bytes, key, out.ExpiresAt); e != nil {
			return e
		}
		_, e = tx.Exec(c, `UPDATE attachment_budgets SET reserved_bytes=reserved_bytes+$2 WHERE tenant_id=$1`, p.TenantID, in.Bytes)
		return e
	})
	return out, e
}

// Upload writes once to a server-generated key. Clients never receive PUT URLs.
// Network operations are bounded and remain under the authorization locks.
// On storage failure the registration remains reserved for explicit cleanup.
func (s *Service) Upload(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID, body io.Reader) error {
	return s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		r, e := load(c, tx, p.TenantID, conv, id)
		if e != nil {
			return e
		}
		return s.upload(c, tx, p.TenantID, r, body)
	})
}
func (s *Service) upload(c context.Context, tx pgx.Tx, tenant uuid.UUID, r Record, body io.Reader) error {
	if r.State != "upload" || !s.now().Before(r.ExpiresAt) {
		return ErrConflict
	}
	b, e := io.ReadAll(io.LimitReader(body, r.Bytes+1))
	if e != nil {
		return ErrInvalid
	}
	if int64(len(b)) != r.Bytes {
		return ErrInvalid
	}
	version, e := s.Storage.Create(c, r.key, bytes.NewReader(b), r.Bytes)
	if e != nil || version == "" || version == "null" {
		return ErrStorage
	}
	_, e = tx.Exec(c, `UPDATE attachments SET state='quarantine',object_version=$3 WHERE tenant_id=$1 AND id=$2`, tenant, r.ID, version)
	return e
}

// Validate inspects the exact immutable version, never the current key. Rejected
// bytes enter durable failed state and remain unavailable until cleanup.
func (s *Service) Validate(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID) (Record, error) {
	var out Record
	rejected := false
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		r, e := load(c, tx, p.TenantID, conv, id)
		if e != nil {
			return e
		}
		out = r
		if r.State == "validated" {
			return nil
		}
		if r.State != "quarantine" || !s.now().Before(r.ExpiresAt) {
			return ErrConflict
		}
		reader, e := s.Storage.Read(c, r.key, r.version)
		if e != nil {
			return ErrStorage
		}
		defer reader.Close()
		b, e := io.ReadAll(io.LimitReader(reader, r.Bytes+1))
		if e != nil {
			return ErrStorage
		}
		typ, digest, validation := inspect(b, r.direction)
		state := "validated"
		if int64(len(b)) != r.Bytes || validation != nil {
			state = "failed"
			rejected = true
			typ = ""
			digest = ""
		}
		_, e = tx.Exec(c, `UPDATE attachments SET state=$3,media_type=$4,sha256=$5 WHERE tenant_id=$1 AND id=$2`, p.TenantID, id, state, typ, digest)
		out.State = state
		out.MediaType = typ
		return e
	})
	if e == nil && rejected {
		e = ErrInvalid
	}
	return out, e
}
func (s *Service) Get(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID) (Record, error) {
	var out Record
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		var e error
		out, e = load(c, tx, p.TenantID, conv, id)
		return e
	})
	return out, e
}
func (s *Service) Download(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID) (string, error) {
	var link string
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		r, e := load(c, tx, p.TenantID, conv, id)
		if e != nil {
			return e
		}
		if r.State != "validated" || r.version == "" {
			return ErrConflict
		}
		link, e = s.Storage.Sign(c, r.key, r.version, r.Name, DownloadTTL)
		if e != nil {
			return ErrStorage
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	return link, nil
}

// ReceiveRemote is deliberately a service-only worker contract. The caller must
// supply an authorized live principal and an existing incoming registration.
// The guard rechecks DNS, redirects and the effective destination. No raw client.
func (s *Service) ReceiveRemote(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID, guard *urlguard.Client, scope urlguard.Scope, remote string) error {
	if guard == nil || scope.TenantID != p.TenantID.String() || scope.ConnectionID == "" {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		r, e := load(c, tx, p.TenantID, conv, id)
		if e != nil {
			return e
		}
		if r.direction != "incoming" || r.State != "upload" || !s.now().Before(r.ExpiresAt) {
			return ErrConflict
		}
		var connector uuid.UUID
		if e = tx.QueryRow(c, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, p.TenantID, inbox).Scan(&connector); e != nil {
			return e
		}
		if scope.ConnectionID != connector.String() {
			return ErrForbidden
		}
		resp, e := guard.Get(c, scope, remote)
		if e != nil {
			return ErrStorage
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ErrStorage
		}
		return s.upload(c, tx, p.TenantID, r, resp.Body)
	})
}

// Cleanup removes at most 100 expired unfinished records for one authorized
// conversation. Budget->attachment lock order matches Register. No validated
// media is removed. Retention deletion is a separate authorized policy.
func (s *Service) Cleanup(ctx context.Context, p auth.Principal, inbox, conv uuid.UUID) (int, error) {
	count := 0
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(c, `SELECT tenant_id FROM attachment_budgets WHERE tenant_id=$1 FOR UPDATE`, p.TenantID); e != nil {
			return e
		}
		rows, e := tx.Query(c, `SELECT id FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND state<>'validated' AND expires_at<=$3 ORDER BY id LIMIT 100 FOR UPDATE`, p.TenantID, conv, s.now())
		if e != nil {
			return e
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			r, e := load(c, tx, p.TenantID, conv, id)
			if e != nil {
				return e
			}
			if e = s.Storage.Delete(c, r.key, ""); e != nil {
				return ErrStorage
			}
			if _, e = tx.Exec(c, `DELETE FROM attachments WHERE tenant_id=$1 AND id=$2`, p.TenantID, id); e != nil {
				return e
			}
			if _, e = tx.Exec(c, `UPDATE attachment_budgets SET reserved_bytes=reserved_bytes-$2 WHERE tenant_id=$1`, p.TenantID, r.Bytes); e != nil {
				return e
			}
			count++
		}
		return nil
	})
	if e != nil {
		return 0, e
	}
	return count, nil
}
