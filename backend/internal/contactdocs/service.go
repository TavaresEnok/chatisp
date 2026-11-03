package contactdocs

import (
	"context"
	"encoding/json"
	"errors"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	opSet   = "set"
	opClear = "clear"

	actionSet    = "contact_document.set"
	actionClear  = "contact_document.clear"
	actionReveal = "contact_document.reveal"
)

var (
	ErrForbidden   = errors.New("acesso negado")
	ErrNotFound    = errors.New("contato não encontrado")
	ErrInvalid     = errors.New("documento inválido")
	ErrConflict    = errors.New("conflito de versão")
	ErrUnavailable = errors.New("serviço indisponível")
	errNoPepper    = errors.New("pepper ausente")
)

// Service seals, masks and finds an optional contact document.
// DB is the application store. Keys is the versioned master ring and never
// comes from the database. A nil dependency fails closed.
type Service struct {
	DB   *db.Store
	Keys *crypto.Ring
}

// New binds the store and the master-key ring.
func New(database *db.Store, keys *crypto.Ring) Service {
	return Service{DB: database, Keys: keys}
}

// Command is one idempotent set or clear. ExpectedVersion is the version the
// caller last observed. Zero matches only a contact that has never stored a
// document. Clear keeps that version and advances it. Document is ignored by
// Clear. The same RequestID repeats the stored masked result.
type Command struct {
	RequestID       uuid.UUID `json:"request_id"`
	ExpectedVersion int64     `json:"expected_version"`
	Document        string    `json:"document,omitempty"`
}

// View is the safe projection. Mask never contains the digits.
type View struct {
	ContactID uuid.UUID `json:"contact_id"`
	Present   bool      `json:"present"`
	Kind      string    `json:"kind,omitempty"`
	Mask      string    `json:"mask"`
	Version   int64     `json:"version"`
}

// Revealed is the authorized plaintext. Callers must not log it.
type Revealed struct {
	ContactID uuid.UUID `json:"contact_id"`
	Kind      string    `json:"kind"`
	Document  string    `json:"document"`
	Version   int64     `json:"version"`
}

// Match is an exact HMAC hit that the caller is allowed to see.
type Match struct {
	ContactID uuid.UUID `json:"contact_id"`
	Kind      string    `json:"kind"`
	Mask      string    `json:"mask"`
	Version   int64     `json:"version"`
}

type storedDoc struct {
	kind    string
	version string
	blob    []byte
	row     int64
	present bool
}

// Set replaces the optional document. Admin and supervisor only.
func (s Service) Set(ctx context.Context, p auth.Principal, contactID uuid.UUID, cmd Command) (View, error) {
	kind, digits, err := normalize(cmd.Document)
	if err != nil {
		return View{}, err
	}
	return s.mutate(ctx, p, contactID, cmd, opSet, kind, digits)
}

// Clear removes ciphertext and HMAC and advances the monotonic version.
// The row stays, so a later Set cannot reuse a version from before the clear.
// Admin and supervisor only.
func (s Service) Clear(ctx context.Context, p auth.Principal, contactID uuid.UUID, cmd Command) (View, error) {
	cmd.Document = ""
	return s.mutate(ctx, p, contactID, cmd, opClear, "", "")
}

// Masked returns the display mask for a contact the caller may see.
// An absent document is a successful empty view, not an error.
func (s Service) Masked(ctx context.Context, p auth.Principal, contactID uuid.UUID) (View, error) {
	view := emptyView(contactID)
	if s.DB == nil {
		return view, ErrUnavailable
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.gate(ctx, tx, p); err != nil {
			return err
		}
		if err := s.visible(ctx, tx, p, contactID); err != nil {
			return err
		}
		doc, ok, err := loadDocument(ctx, tx, p.TenantID, contactID, false)
		if err != nil {
			return err
		}
		if ok {
			view = project(contactID, doc)
		}
		return nil
	})
	return view, err
}

// Reveal returns the plaintext to an admin or supervisor who can see the
// contact. The attempt is audited without the value. Agents are denied.
func (s Service) Reveal(ctx context.Context, p auth.Principal, contactID uuid.UUID) (Revealed, error) {
	var out Revealed
	var denied bool
	if s.DB == nil || s.Keys == nil {
		return out, ErrUnavailable
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.gate(ctx, tx, p); err != nil {
			return err
		}
		// Contact and sector locks before the role decision, so a hidden
		// contact stays "not found" for every role.
		if err := s.visible(ctx, tx, p, contactID); err != nil {
			return err
		}
		if !manager(p.Role) {
			if err := insertAudit(ctx, tx, p, contactID, actionReveal, "denied"); err != nil {
				return err
			}
			denied = true
			return nil
		}
		doc, ok, err := loadDocument(ctx, tx, p.TenantID, contactID, true)
		if err != nil {
			return err
		}
		if !ok || !doc.present {
			return ErrNotFound
		}
		plain, err := s.Keys.Decrypt(doc.version, doc.blob, documentAAD(p.TenantID, contactID))
		if err != nil {
			wipe(plain)
			return ErrUnavailable
		}
		defer wipe(plain)
		if err := insertAudit(ctx, tx, p, contactID, actionReveal, "success"); err != nil {
			return err
		}
		out = Revealed{ContactID: contactID, Kind: doc.kind, Document: string(plain), Version: doc.row}
		return nil
	})
	if err != nil {
		return Revealed{}, err
	}
	if denied {
		return Revealed{}, ErrForbidden
	}
	return out, nil
}

// FindByDocumentHMAC finds one contact by the exact tenant HMAC of a CPF/CNPJ.
// A hidden contact and an unknown document are both not found. The result is
// the mask, never the digits. The document does not authenticate the caller.
func (s Service) FindByDocumentHMAC(ctx context.Context, p auth.Principal, document string) (Match, error) {
	var out Match
	_, digits, err := normalize(document)
	if err != nil {
		return out, err
	}
	if s.DB == nil || s.Keys == nil {
		return out, ErrUnavailable
	}
	err = s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.gate(ctx, tx, p); err != nil {
			return err
		}
		// Pepper before the contact row. Writers use the same order.
		pepper, err := s.pepper(ctx, tx, p.TenantID, false)
		if errors.Is(err, errNoPepper) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		defer wipe(pepper)
		mac := documentMAC(pepper, p.TenantID, digits)
		var id uuid.UUID
		var kind string
		var version int64
		err = tx.QueryRow(ctx, `SELECT contact_id, kind, version FROM contact_documents WHERE tenant_id=$1 AND document_hmac=$2 FOR SHARE`, p.TenantID, mac).Scan(&id, &kind, &version)
		wipe(mac)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return mapDB(err)
		}
		if err := s.visible(ctx, tx, p, id); err != nil {
			return err
		}
		out = Match{ContactID: id, Kind: kind, Mask: displayMask(kind), Version: version}
		return nil
	})
	return out, err
}

func (s Service) mutate(ctx context.Context, p auth.Principal, contactID uuid.UUID, cmd Command, op, kind, digits string) (View, error) {
	view := emptyView(contactID)
	if cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 0 || contactID == uuid.Nil {
		return view, ErrInvalid
	}
	if s.DB == nil || s.Keys == nil {
		return view, ErrUnavailable
	}
	var denied bool
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Exclusive suspension gate, then principal, then pepper, then the
		// contact/sector locks used by the directory. Document row is last.
		if err := s.DB.LockTenant(ctx); err != nil {
			return err
		}
		if err := s.gate(ctx, tx, p); err != nil {
			return err
		}
		pepper, err := s.pepper(ctx, tx, p.TenantID, true)
		if err != nil {
			return err
		}
		defer wipe(pepper)
		if err := s.visible(ctx, tx, p, contactID); err != nil {
			return err
		}
		if !manager(p.Role) {
			action := actionSet
			if op == opClear {
				action = actionClear
			}
			if err := insertAudit(ctx, tx, p, contactID, action, "denied"); err != nil {
				return err
			}
			denied = true
			return nil
		}
		current, found, err := loadDocument(ctx, tx, p.TenantID, contactID, true)
		if err != nil {
			return err
		}
		have := int64(0)
		if found {
			have = current.row
		}
		sum := requestMAC(pepper, op, contactID, cmd.ExpectedVersion, digits)
		defer wipe(sum)
		var previous []byte
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT request_hmac, response FROM contact_document_commands WHERE tenant_id=$1 AND membership_id=$2 AND request_id=$3`, p.TenantID, p.MembershipID, cmd.RequestID).Scan(&previous, &raw)
		if err == nil {
			if !macsEqual(previous, sum) {
				return ErrConflict
			}
			if err = json.Unmarshal(raw, &view); err != nil {
				return ErrUnavailable
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return mapDB(err)
		}
		if have != cmd.ExpectedVersion {
			return ErrConflict
		}
		if op == opClear {
			if found && current.present {
				next := have + 1
				tag, err := tx.Exec(ctx, `UPDATE contact_documents SET kind=NULL, key_version=NULL, ciphertext=NULL, document_hmac=NULL, version=$3, updated_by_membership_id=$4, updated_at=now() WHERE tenant_id=$1 AND contact_id=$2 AND version=$5`, p.TenantID, contactID, next, p.MembershipID, have)
				if err != nil {
					return mapDB(err)
				}
				if tag.RowsAffected() != 1 {
					return ErrConflict
				}
				view = absentView(contactID, next)
			} else if found {
				view = absentView(contactID, have)
			} else {
				view = emptyView(contactID)
			}
			if err = insertAudit(ctx, tx, p, contactID, actionClear, "success"); err != nil {
				return err
			}
		} else {
			sealed, err := s.Keys.Encrypt([]byte(digits), documentAAD(p.TenantID, contactID))
			if err != nil {
				return ErrUnavailable
			}
			mac := documentMAC(pepper, p.TenantID, digits)
			next := have + 1
			var tag pgconn.CommandTag
			if !found {
				tag, err = tx.Exec(ctx, `INSERT INTO contact_documents (tenant_id, contact_id, kind, key_version, ciphertext, document_hmac, version, updated_by_membership_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, p.TenantID, contactID, kind, sealed.Version, sealed.Ciphertext, mac, next, p.MembershipID)
			} else {
				tag, err = tx.Exec(ctx, `UPDATE contact_documents SET kind=$3, key_version=$4, ciphertext=$5, document_hmac=$6, version=$7, updated_by_membership_id=$8, updated_at=now() WHERE tenant_id=$1 AND contact_id=$2 AND version=$9`, p.TenantID, contactID, kind, sealed.Version, sealed.Ciphertext, mac, next, p.MembershipID, have)
			}
			wipe(mac)
			if err != nil {
				return mapDB(err)
			}
			if tag.RowsAffected() != 1 {
				return ErrConflict
			}
			view = maskedView(contactID, kind, next)
			if err = insertAudit(ctx, tx, p, contactID, actionSet, "success"); err != nil {
				return err
			}
		}
		body, err := json.Marshal(view)
		if err != nil {
			return ErrUnavailable
		}
		_, err = tx.Exec(ctx, `INSERT INTO contact_document_commands (tenant_id, membership_id, request_id, request_hmac, response) VALUES ($1,$2,$3,$4,$5)`, p.TenantID, p.MembershipID, cmd.RequestID, sum, body)
		return mapDB(err)
	})
	if err != nil {
		return View{}, err
	}
	if denied {
		return View{}, ErrForbidden
	}
	return view, nil
}

func (s Service) gate(ctx context.Context, tx pgx.Tx, p auth.Principal) error {
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return ErrForbidden
	}
	return mapSector(sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)))
}

// visible follows the contact directory: a manual contact with no conversation
// is limited to the admin or its author; any conversation requires sector and
// inbox access. Denial is not found, so another sector cannot probe existence.
func (s Service) visible(ctx context.Context, tx pgx.Tx, p auth.Principal, contactID uuid.UUID) error {
	var creator *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT created_by_membership_id FROM contacts WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, contactID).Scan(&creator)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapDB(err)
	}
	rows, err := tx.Query(ctx, `SELECT id, inbox_id FROM conversations WHERE tenant_id=$1 AND contact_id=$2 AND inbox_id IS NOT NULL ORDER BY id`, p.TenantID, contactID)
	if err != nil {
		return mapDB(err)
	}
	type pair struct{ id, inbox uuid.UUID }
	var conversations []pair
	for rows.Next() {
		var c pair
		if err = rows.Scan(&c.id, &c.inbox); err != nil {
			break
		}
		conversations = append(conversations, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return mapDB(err)
	}
	if len(conversations) == 0 {
		if p.Role == "admin" || (creator != nil && *creator == p.MembershipID) {
			return nil
		}
		return ErrNotFound
	}
	sp := sectorPrincipal(p)
	for _, c := range conversations {
		ok, err := sectors.CanAccessConversation(ctx, tx, sp, c.inbox, c.id)
		if err != nil {
			return mapSector(err)
		}
		if ok {
			return nil
		}
	}
	return ErrNotFound
}

func (s Service) pepper(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, create bool) ([]byte, error) {
	if s.Keys == nil {
		return nil, ErrUnavailable
	}
	aad := crypto.Context{TenantID: tenant.String(), Entity: "contact_document_pepper", EntityID: tenant.String(), Field: "pepper"}
	lock := `SELECT key_version, ciphertext FROM contact_document_peppers WHERE tenant_id=$1 FOR SHARE`
	if create {
		lock = `SELECT key_version, ciphertext FROM contact_document_peppers WHERE tenant_id=$1 FOR UPDATE`
	}
	for attempt := 0; attempt < 2; attempt++ {
		var version string
		var blob []byte
		err := tx.QueryRow(ctx, lock, tenant).Scan(&version, &blob)
		if errors.Is(err, pgx.ErrNoRows) {
			if !create {
				return nil, errNoPepper
			}
			raw := make([]byte, 32)
			if _, err = randRead(raw); err != nil {
				return nil, ErrUnavailable
			}
			sealed, err := s.Keys.Encrypt(raw, aad)
			if err != nil {
				wipe(raw)
				return nil, ErrUnavailable
			}
			tag, err := tx.Exec(ctx, `INSERT INTO contact_document_peppers (tenant_id, key_version, ciphertext) VALUES ($1,$2,$3) ON CONFLICT (tenant_id) DO NOTHING`, tenant, sealed.Version, sealed.Ciphertext)
			if err != nil {
				wipe(raw)
				return nil, mapDB(err)
			}
			if tag.RowsAffected() == 1 {
				return raw, nil
			}
			wipe(raw)
			continue
		}
		if err != nil {
			return nil, mapDB(err)
		}
		plain, err := s.Keys.Decrypt(version, blob, aad)
		if err != nil || len(plain) != 32 {
			wipe(plain)
			return nil, ErrUnavailable
		}
		return plain, nil
	}
	return nil, ErrUnavailable
}

func sectorPrincipal(p auth.Principal) sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}

func manager(role string) bool {
	return role == "admin" || role == "supervisor"
}

func documentAAD(tenant, contact uuid.UUID) crypto.Context {
	return crypto.Context{TenantID: tenant.String(), Entity: "contact_document", EntityID: contact.String(), Field: "document"}
}

func emptyView(id uuid.UUID) View {
	return View{ContactID: id, Mask: ""}
}

func absentView(id uuid.UUID, version int64) View {
	return View{ContactID: id, Mask: "", Version: version}
}

func maskedView(id uuid.UUID, kind string, version int64) View {
	return View{ContactID: id, Present: true, Kind: kind, Mask: displayMask(kind), Version: version}
}

func project(id uuid.UUID, doc storedDoc) View {
	if !doc.present {
		return absentView(id, doc.row)
	}
	return maskedView(id, doc.kind, doc.row)
}

func loadDocument(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID, update bool) (storedDoc, bool, error) {
	q := `SELECT kind, key_version, ciphertext, version FROM contact_documents WHERE tenant_id=$1 AND contact_id=$2 FOR SHARE`
	if update {
		q = `SELECT kind, key_version, ciphertext, version FROM contact_documents WHERE tenant_id=$1 AND contact_id=$2 FOR UPDATE`
	}
	var doc storedDoc
	var kind, keyVersion *string
	err := tx.QueryRow(ctx, q, tenant, contact).Scan(&kind, &keyVersion, &doc.blob, &doc.row)
	if errors.Is(err, pgx.ErrNoRows) {
		return doc, false, nil
	}
	if err != nil {
		return doc, false, mapDB(err)
	}
	if kind != nil {
		doc.kind = *kind
	}
	if keyVersion != nil {
		doc.version = *keyVersion
	}
	doc.present = kind != nil && keyVersion != nil && doc.blob != nil
	return doc, true, nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, p auth.Principal, contact uuid.UUID, action, result string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs (id, tenant_id, membership_id, actor_user_id, action, target_type, target_id, result) VALUES ($1,$2,$3,$4,$5,'contact',$6,$7)`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, action, contact, result)
	return mapDB(err)
}

func mapSector(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sectors.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, sectors.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, sectors.ErrInvalid):
		return ErrInvalid
	default:
		return err
	}
}

func mapDB(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalid
		}
	}
	return err
}
