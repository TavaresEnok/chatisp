// Package contactdirectory implements the tenant contact directory. It does not create channel identities or conversations.
package contactdirectory

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrForbidden   = errors.New("acesso negado")
	ErrNotFound    = errors.New("contato não encontrado")
	ErrInvalid     = errors.New("dados inválidos")
	ErrUnavailable = errors.New("autorização indisponível")
)

// Visibility must check sector/inbox authorization in the supplied tenant transaction.
// Contact is also responsible for the explicit scope of manual contacts without conversations.
type Visibility interface {
	Contact(context.Context, pgx.Tx, auth.Principal, uuid.UUID) (bool, error)
	Conversation(context.Context, pgx.Tx, auth.Principal, uuid.UUID, uuid.UUID) (bool, error)
}
type Service struct {
	DB         *db.Store
	Visibility Visibility
}
type Contact struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Phone           *string    `json:"phone"`
	Email           *string    `json:"email"`
	Notes           string     `json:"notes"`
	Blocked         bool       `json:"blocked"`
	BlockReason     string     `json:"block_reason"`
	AvatarReference *string    `json:"avatar_reference"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastContact     *time.Time `json:"last_contact"`
}
type Input struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Email string `json:"email"`
	Notes string `json:"notes"`
}
type Conversation struct {
	ID        uuid.UUID `json:"id"`
	InboxID   uuid.UUID `json:"inbox_id"`
	Protocol  string    `json:"protocol"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Page struct {
	Items      []Contact `json:"items"`
	NextOffset *int      `json:"next_offset"`
}
type HistoryPage struct {
	Items      []Conversation `json:"items"`
	NextOffset *int           `json:"next_offset"`
}

var phoneRE = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func normalize(in Input, requirePhone bool) (Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	var b strings.Builder
	for _, r := range strings.TrimSpace(in.Phone) {
		if r == '+' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if !unicode.IsSpace(r) && r != '-' && r != '(' && r != ')' && r != '.' {
			return in, ErrInvalid
		}
	}
	in.Phone = b.String()
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 200 || (requirePhone || in.Phone != "") && !phoneRE.MatchString(in.Phone) || utf8.RuneCountInString(in.Notes) > 4000 {
		return in, ErrInvalid
	}
	if in.Email != "" {
		a, e := mail.ParseAddress(in.Email)
		if e != nil || a.Address != in.Email || len(in.Email) > 320 || strings.ContainsAny(in.Email, " \t\r\n") {
			return in, ErrInvalid
		}
	}
	return in, nil
}
func valid(p auth.Principal) bool {
	return !p.IsSuperadmin && !p.EnrollmentOnly && p.TenantID != uuid.Nil && p.MembershipID != uuid.Nil && p.UserID != uuid.Nil && (p.Role == "admin" || p.Role == "supervisor" || p.Role == "agent")
}
func (s Service) tx(ctx context.Context, p auth.Principal, f func(pgx.Tx) error) error {
	if !valid(p) {
		return ErrForbidden
	}
	if s.DB == nil || s.Visibility == nil {
		return ErrUnavailable
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(_ context.Context, tx pgx.Tx) error { return f(tx) })
}
func (s Service) can(ctx context.Context, tx pgx.Tx, p auth.Principal, id uuid.UUID) error {
	ok, e := s.Visibility.Contact(ctx, tx, p, id)
	if e != nil {
		return e
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

const columns = `id,name,phone,email,notes,blocked,block_reason,avatar_reference,created_at,updated_at`

func scan(row pgx.Row) (Contact, error) {
	var c Contact
	e := row.Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.Notes, &c.Blocked, &c.BlockReason, &c.AvatarReference, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, e
}
func audit(ctx context.Context, tx pgx.Tx, p auth.Principal, id uuid.UUID, action string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_logs (id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES ($1,$2,$3,$4,$5,'contact',$6,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, action, id)
	return e
}
func (s Service) Create(ctx context.Context, p auth.Principal, in Input) (c Contact, err error) {
	in, err = normalize(in, true)
	if err != nil {
		return
	}
	err = s.tx(ctx, p, func(tx pgx.Tx) error {
		var e error
		c, e = scan(tx.QueryRow(ctx, `INSERT INTO contacts (id,tenant_id,name,phone,email,notes,created_by_membership_id) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7) RETURNING `+columns, uuid.New(), p.TenantID, in.Name, in.Phone, in.Email, in.Notes, p.MembershipID))
		if e != nil {
			return e
		}
		if e = s.can(ctx, tx, p, c.ID); e != nil {
			return e
		}
		return audit(ctx, tx, p, c.ID, "contact.create")
	})
	return
}
func (s Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (c Contact, err error) {
	in, err = normalize(in, false)
	if err != nil {
		return
	}
	err = s.tx(ctx, p, func(tx pgx.Tx) error {
		if e := s.can(ctx, tx, p, id); e != nil {
			return e
		}
		var e error
		c, e = scan(tx.QueryRow(ctx, `UPDATE contacts SET name=$3,phone=COALESCE(NULLIF($4,''),phone),email=NULLIF($5,''),notes=$6,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+columns, p.TenantID, id, in.Name, in.Phone, in.Email, in.Notes))
		if e != nil {
			return e
		}
		return audit(ctx, tx, p, id, "contact.update")
	})
	return
}
func (s Service) SetBlocked(ctx context.Context, p auth.Principal, id uuid.UUID, blocked bool, reason string) (c Contact, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return c, ErrInvalid
	}
	err = s.tx(ctx, p, func(tx pgx.Tx) error {
		if e := s.can(ctx, tx, p, id); e != nil {
			return e
		}
		var e error
		c, e = scan(tx.QueryRow(ctx, `UPDATE contacts SET blocked=$3,block_reason=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+columns, p.TenantID, id, blocked, reason))
		if e != nil {
			return e
		}
		action := "contact.unblock"
		if blocked {
			action = "contact.block"
		}
		return audit(ctx, tx, p, id, action)
	})
	return
}

// IsBlocked is an internal tenant-scoped integration hook; caller supplies its own transaction.
func IsBlocked(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (bool, error) {
	var b bool
	e := tx.QueryRow(ctx, `SELECT blocked FROM contacts WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return b, e
}
func (s Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (c Contact, err error) {
	err = s.tx(ctx, p, func(tx pgx.Tx) error {
		if e := s.can(ctx, tx, p, id); e != nil {
			return e
		}
		var e error
		c, e = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM contacts WHERE tenant_id=$1 AND id=$2`, p.TenantID, id))
		if e != nil {
			return e
		}
		h, e := s.history(ctx, tx, p, id)
		if e != nil {
			return e
		}
		for _, v := range h {
			if c.LastContact == nil || v.UpdatedAt.After(*c.LastContact) {
				t := v.UpdatedAt
				c.LastContact = &t
			}
		}
		return nil
	})
	return
}
func (s Service) history(ctx context.Context, tx pgx.Tx, p auth.Principal, id uuid.UUID) ([]Conversation, error) {
	rows, e := tx.Query(ctx, `SELECT id,inbox_id,protocol,status,created_at,updated_at FROM conversations WHERE tenant_id=$1 AND contact_id=$2 ORDER BY updated_at DESC,id DESC`, p.TenantID, id)
	if e != nil {
		return nil, e
	}
	var all []Conversation
	for rows.Next() {
		var v Conversation
		if e = rows.Scan(&v.ID, &v.InboxID, &v.Protocol, &v.Status, &v.CreatedAt, &v.UpdatedAt); e != nil {
			break
		}
		all = append(all, v)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := make([]Conversation, 0)
	for _, v := range all {
		ok, e := s.Visibility.Conversation(ctx, tx, p, id, v.ID)
		if e != nil {
			return nil, e
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}
func pageBounds(offset, limit, n int) (int, int, *int, error) {
	if offset < 0 || limit < 1 || limit > 100 || offset > 100000 {
		return 0, 0, nil, ErrInvalid
	}
	if offset > n {
		offset = n
	}
	end := offset + limit
	if end > n {
		end = n
	}
	var next *int
	if end < n {
		v := end
		next = &v
	}
	return offset, end, next, nil
}
func (s Service) History(ctx context.Context, p auth.Principal, id uuid.UUID, offset, limit int) (out HistoryPage, err error) {
	err = s.tx(ctx, p, func(tx pgx.Tx) error {
		if e := s.can(ctx, tx, p, id); e != nil {
			return e
		}
		if _, e := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM contacts WHERE tenant_id=$1 AND id=$2`, p.TenantID, id)); e != nil {
			return e
		}
		all, e := s.history(ctx, tx, p, id)
		if e != nil {
			return e
		}
		a, b, n, e := pageBounds(offset, limit, len(all))
		if e != nil {
			return e
		}
		out = HistoryPage{all[a:b], n}
		return nil
	})
	return
}
func (s Service) List(ctx context.Context, p auth.Principal, q, order string, offset, limit int) (out Page, err error) {
	if utf8.RuneCountInString(q) > 200 || (order != "name" && order != "recent") {
		return out, ErrInvalid
	}
	if _, _, _, err = pageBounds(offset, limit, 0); err != nil {
		return
	}
	q = strings.ToLower(strings.TrimSpace(q))
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, q)
	err = s.tx(ctx, p, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT `+columns+` FROM contacts WHERE tenant_id=$1 ORDER BY id`, p.TenantID)
		if e != nil {
			return e
		}
		all := []Contact{}
		for rows.Next() {
			c, e := scan(rows)
			if e != nil {
				rows.Close()
				return e
			}
			all = append(all, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		visible := []Contact{}
		for _, c := range all {
			if q != "" && !strings.Contains(strings.ToLower(c.Name), q) && (digits == "" || c.Phone == nil || !strings.Contains(*c.Phone, digits)) {
				continue
			}
			ok, e := s.Visibility.Contact(ctx, tx, p, c.ID)
			if e != nil {
				return e
			}
			if !ok {
				continue
			}
			h, e := s.history(ctx, tx, p, c.ID)
			if e != nil {
				return e
			}
			for _, v := range h {
				if c.LastContact == nil || v.UpdatedAt.After(*c.LastContact) {
					t := v.UpdatedAt
					c.LastContact = &t
				}
			}
			visible = append(visible, c)
		}
		sort.Slice(visible, func(i, j int) bool {
			a, b := visible[i], visible[j]
			if order == "recent" {
				if a.LastContact == nil && b.LastContact != nil {
					return false
				}
				if a.LastContact != nil && b.LastContact == nil {
					return true
				}
				if a.LastContact != nil && b.LastContact != nil && !a.LastContact.Equal(*b.LastContact) {
					return a.LastContact.After(*b.LastContact)
				}
			}
			an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
			if an != bn {
				return an < bn
			}
			return a.ID.String() < b.ID.String()
		})
		a, b, n, e := pageBounds(offset, limit, len(visible))
		if e != nil {
			return e
		}
		out = Page{visible[a:b], n}
		return nil
	})
	return
}
