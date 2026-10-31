// Package cannedreplies stores shared plain-text drafts and attendant signatures.
package cannedreplies

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalid       = errors.New("comando inválido")
	ErrConflict      = errors.New("versão ou shortcode em conflito")
	ErrNotFound      = errors.New("resposta não encontrada")
	ErrForbidden     = sectors.ErrForbidden
	shortcodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

type Service struct{ DB *db.Store }

func New(database *db.Store) Service { return Service{DB: database} }

type Reply struct {
	ID        uuid.UUID `json:"id"`
	Shortcode string    `json:"shortcode"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Version   int64     `json:"version"`
}
type Edit struct {
	Shortcode string `json:"shortcode"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}
type Page struct {
	Replies []Reply `json:"replies"`
	HasMore bool    `json:"has_more"`
}
type Signature struct {
	Enabled bool   `json:"enabled"`
	Prefix  string `json:"prefix"`
	Version int64  `json:"version"`
}
type Draft struct {
	Text    string   `json:"text"`
	Missing []string `json:"missing"`
}

func principal(p auth.Principal) sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}
func validate(ctx context.Context, tx pgx.Tx, p auth.Principal) error {
	if p.EnrollmentOnly {
		return ErrForbidden
	}
	return sectors.ValidatePrincipal(ctx, tx, principal(p))
}
func (s Service) transaction(ctx context.Context, p auth.Principal, write bool, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrForbidden
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := validate(ctx, tx, p); err != nil {
			return err
		}
		if write && p.Role != "admin" && p.Role != "supervisor" {
			return ErrForbidden
		}
		err := fn(ctx, tx)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return ErrConflict
		}
		return err
	})
}
func validText(s string, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max && !strings.ContainsRune(s, 0)
}

// expand parses a closed, single-pass grammar. Values are never interpreted again.
func expand(template string, values map[string]string, signature bool) (Draft, error) {
	out := Draft{Missing: []string{}}
	var b strings.Builder
	seen := map[string]bool{}
	for len(template) > 0 {
		i := strings.IndexAny(template, "{}<>")
		if i < 0 {
			b.WriteString(template)
			break
		}
		b.WriteString(template[:i])
		template = template[i:]
		if !strings.HasPrefix(template, "{{") {
			return Draft{}, ErrInvalid
		}
		end := strings.Index(template[2:], "}}")
		if end < 0 {
			return Draft{}, ErrInvalid
		}
		end += 2
		key := template[2:end]
		if key != "nome_atendente" && (signature || (key != "nome_cliente" && key != "protocolo")) {
			return Draft{}, ErrInvalid
		}
		value := values[key]
		if value == "" {
			if !seen[key] {
				out.Missing = append(out.Missing, key)
				seen[key] = true
			}
			b.WriteString("{{" + key + "}}")
		} else {
			b.WriteString(value)
		}
		template = template[end+2:]
	}
	out.Text = b.String()
	return out, nil
}
func validEdit(e Edit) bool {
	if !shortcodePattern.MatchString(e.Shortcode) || !validText(e.Title, 120) || !validText(e.Content, 10000) {
		return false
	}
	_, err := expand(e.Content, nil, false)
	return err == nil
}
func audit(ctx context.Context, tx pgx.Tx, p auth.Principal, action string, id uuid.UUID, version int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,$5,'canned_reply',$6,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, fmt.Sprintf("%s version=%d", action, version), id)
	return err
}
func (s Service) List(ctx context.Context, p auth.Principal, search string, limit, offset int) (Page, error) {
	out := Page{Replies: []Reply{}}
	if limit < 1 || limit > 100 || offset < 0 || offset > 100000 || utf8.RuneCountInString(search) > 120 {
		return out, ErrInvalid
	}
	err := s.transaction(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,shortcode,title,content,version FROM canned_replies WHERE tenant_id=$1 AND ($2='' OR strpos(lower(shortcode||' '||title||' '||content),lower($2))>0) ORDER BY shortcode,id LIMIT $3 OFFSET $4`, p.TenantID, search, limit+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v Reply
			if err = rows.Scan(&v.ID, &v.Shortcode, &v.Title, &v.Content, &v.Version); err != nil {
				return err
			}
			out.Replies = append(out.Replies, v)
		}
		if len(out.Replies) > limit {
			out.HasMore = true
			out.Replies = out.Replies[:limit]
		}
		return rows.Err()
	})
	return out, err
}
func (s Service) Create(ctx context.Context, p auth.Principal, e Edit) (Reply, error) {
	v := Reply{ID: uuid.New(), Shortcode: e.Shortcode, Title: e.Title, Content: e.Content, Version: 1}
	if !validEdit(e) {
		return Reply{}, ErrInvalid
	}
	err := s.transaction(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO canned_replies(tenant_id,id,shortcode,title,content) VALUES($1,$2,$3,$4,$5)`, p.TenantID, v.ID, e.Shortcode, e.Title, e.Content); err != nil {
			return err
		}
		return audit(ctx, tx, p, "canned.create", v.ID, 1)
	})
	return v, err
}
func (s Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, version int64, e Edit) (Reply, error) {
	v := Reply{ID: id, Shortcode: e.Shortcode, Title: e.Title, Content: e.Content, Version: version + 1}
	if id == uuid.Nil || version < 1 || !validEdit(e) {
		return Reply{}, ErrInvalid
	}
	err := s.transaction(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockReply(ctx, tx, p, id, version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE canned_replies SET shortcode=$3,title=$4,content=$5,version=version+1 WHERE tenant_id=$1 AND id=$2`, p.TenantID, id, e.Shortcode, e.Title, e.Content); err != nil {
			return err
		}
		return audit(ctx, tx, p, "canned.update", id, version+1)
	})
	return v, err
}
func lockReply(ctx context.Context, tx pgx.Tx, p auth.Principal, id uuid.UUID, version int64) error {
	var actual int64
	err := tx.QueryRow(ctx, `SELECT version FROM canned_replies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, id).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if actual != version {
		return ErrConflict
	}
	return nil
}
func (s Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID, version int64) error {
	if id == uuid.Nil || version < 1 {
		return ErrInvalid
	}
	return s.transaction(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockReply(ctx, tx, p, id, version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM canned_replies WHERE tenant_id=$1 AND id=$2`, p.TenantID, id); err != nil {
			return err
		}
		return audit(ctx, tx, p, "canned.delete", id, version)
	})
}
func signature(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Signature, error) {
	v := Signature{Prefix: "{{nome_atendente}}: "}
	err := tx.QueryRow(ctx, `SELECT enabled,prefix,version FROM attendant_signature_settings WHERE tenant_id=$1 FOR SHARE`, tenant).Scan(&v.Enabled, &v.Prefix, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return v, err
}
func (s Service) Signature(ctx context.Context, p auth.Principal) (Signature, error) {
	var v Signature
	err := s.transaction(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		v, err = signature(ctx, tx, p.TenantID)
		return err
	})
	return v, err
}
func (s Service) SetSignature(ctx context.Context, p auth.Principal, v Signature) (Signature, error) {
	if v.Version < 0 || !validText(v.Prefix, 240) {
		return Signature{}, ErrInvalid
	}
	if _, err := expand(v.Prefix, nil, true); err != nil {
		return Signature{}, err
	}
	err := s.transaction(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		// Serialize first creation as well as versioned updates; before settings row locks.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('canned-signature'),hashtext($1::text))`, p.TenantID.String()); err != nil {
			return err
		}
		old, err := signature(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if old.Version != v.Version {
			return ErrConflict
		}
		v.Version++
		if _, err = tx.Exec(ctx, `INSERT INTO attendant_signature_settings(tenant_id,enabled,prefix,version) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id) DO UPDATE SET enabled=excluded.enabled,prefix=excluded.prefix,version=excluded.version`, p.TenantID, v.Enabled, v.Prefix, v.Version); err != nil {
			return err
		}
		return audit(ctx, tx, p, "signature.update", p.TenantID, v.Version)
	})
	return v, err
}

// Render returns a draft only; no message, queue or dispatch side effects.
func (s Service) Render(ctx context.Context, p auth.Principal, id, inbox, conversation uuid.UUID) (Draft, error) {
	var out Draft
	err := s.transaction(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireConversation(ctx, tx, principal(p), inbox, conversation); err != nil {
			return err
		}
		var content, customer, protocol string
		err := tx.QueryRow(ctx, `SELECT r.content,COALESCE(ct.name,''),COALESCE(c.protocol,'') FROM canned_replies r JOIN conversations c ON c.tenant_id=r.tenant_id AND c.id=$3 JOIN contacts ct ON ct.tenant_id=c.tenant_id AND ct.id=c.contact_id WHERE r.tenant_id=$1 AND r.id=$2`, p.TenantID, id, conversation).Scan(&content, &customer, &protocol)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = expand(content, map[string]string{"nome_cliente": customer, "nome_atendente": p.Email, "protocolo": protocol}, false)
		return err
	})
	return out, err
}

// AcceptPublicHumanTx must be called only by the future public-human acceptance
// path, AFTER conversation authorization, in the SAME tenant tx as the message.
// p must come from Auth.Authenticate; never accept identity or kind from JSON.
// Persist acceptanceID with the message, reuse it on retries, and pass ORIGINAL
// text. A changed payload/actor for the same key conflicts. Bot/notes must bypass.
func AcceptPublicHumanTx(ctx context.Context, tx pgx.Tx, p auth.Principal, acceptanceID uuid.UUID, text string) (string, error) {
	if acceptanceID == uuid.Nil || !validText(text, 16000) {
		return "", ErrInvalid
	}
	if err := validate(ctx, tx, p); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('signature-acceptance'),hashtext($1))`, p.TenantID.String()+acceptanceID.String()); err != nil {
		return "", err
	}
	var original, accepted string
	var actor uuid.UUID
	err := tx.QueryRow(ctx, `SELECT membership_id,original_text,accepted_text FROM attendant_signature_receipts WHERE tenant_id=$1 AND acceptance_id=$2`, p.TenantID, acceptanceID).Scan(&actor, &original, &accepted)
	if err == nil {
		if actor != p.MembershipID || original != text {
			return "", ErrConflict
		}
		return accepted, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	config, err := signature(ctx, tx, p.TenantID)
	if err != nil {
		return "", err
	}
	accepted = text
	if config.Enabled {
		prefix, err := expand(config.Prefix, map[string]string{"nome_atendente": p.Email}, true)
		if err != nil {
			return "", err
		}
		if len(prefix.Missing) > 0 {
			return "", ErrInvalid
		}
		accepted = prefix.Text + text
	}
	if utf8.RuneCountInString(accepted) > 16000 {
		return "", ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO attendant_signature_receipts(tenant_id,acceptance_id,membership_id,original_text,accepted_text) VALUES($1,$2,$3,$4,$5)`, p.TenantID, acceptanceID, p.MembershipID, text, accepted)
	return accepted, err
}
