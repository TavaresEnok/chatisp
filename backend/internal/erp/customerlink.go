package erp

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CustomerPlaceholder is replaced by the escaped external customer id.
const CustomerPlaceholder = "{cliente}"

// Customer link refusals. They name the rule, never the rejected value.
var (
	ErrLinkTemplate   = errors.New("modelo de link do erp inválido")
	ErrLinkCredential = errors.New("modelo de link do erp não pode carregar credencial nem documento")
)

// Parameter and path words that would put a credential or a CPF/CNPJ in the URL.
var forbiddenLinkWords = []string{
	"token", "key", "secret", "senha", "password", "passwd", "pwd", "auth", "apikey", "api_key",
	"signature", "sig", "session", "sessao", "cpf", "cnpj", "documento", "document",
}

// ValidateCustomerTemplate checks the provider's template. It must be HTTPS
// with a host, no userinfo or fragment, exactly one {cliente}, and no query
// key or path segment that looks like a credential or a document.
func ValidateCustomerTemplate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 12 || len(raw) > 512 || strings.ContainsAny(raw, " \t\r\n\x00") {
		return "", ErrLinkTemplate
	}
	if strings.Count(raw, CustomerPlaceholder) != 1 || strings.Count(raw, "{") != 1 || strings.Count(raw, "}") != 1 {
		return "", ErrLinkTemplate
	}
	probe := strings.Replace(raw, CustomerPlaceholder, "cliente", 1)
	u, err := url.Parse(probe)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(u.Host, "@") {
		return "", ErrLinkTemplate
	}
	if !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return "", ErrLinkTemplate
	}
	if hostPart := raw[len("https://"):]; strings.Contains(strings.SplitN(hostPart, "/", 2)[0], CustomerPlaceholder) {
		return "", ErrLinkTemplate
	}
	for key, values := range u.Query() {
		if forbiddenWord(key) {
			return "", ErrLinkCredential
		}
		for _, value := range values {
			if value != "cliente" && forbiddenWord(value) {
				return "", ErrLinkCredential
			}
		}
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment != "" && segment != "cliente" && forbiddenWord(segment) {
			return "", ErrLinkCredential
		}
	}
	return raw, nil
}

func forbiddenWord(value string) bool {
	value = strings.ToLower(value)
	for _, word := range forbiddenLinkWords {
		if value == word || strings.HasPrefix(value, word+"_") || strings.HasSuffix(value, "_"+word) || strings.HasPrefix(value, word+"-") || strings.HasSuffix(value, "-"+word) {
			return true
		}
	}
	return strings.Contains(value, "token") || strings.Contains(value, "secret") || strings.Contains(value, "senha") || strings.Contains(value, "password")
}

// looksLikeDocument is true for an id that is only a CPF (11) or CNPJ (14)
// digits, with or without the usual separators. Such an id is not placed in
// a URL: the requirement forbids CPF in the link.
func looksLikeDocument(id string) bool {
	digits := 0
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || r == '-' || r == '/':
		default:
			return false
		}
	}
	return digits == 11 || digits == 14
}

// CustomerURL fills the template with the external customer id. It returns
// "" when there is no template or when the id looks like a CPF/CNPJ.
func CustomerURL(template, customerID string) string {
	customerID = strings.TrimSpace(customerID)
	if template == "" || customerID == "" || looksLikeDocument(customerID) {
		return ""
	}
	if _, err := ValidateCustomerTemplate(template); err != nil {
		return ""
	}
	at := strings.Index(template, CustomerPlaceholder)
	escaped := url.PathEscape(customerID)
	if q := strings.IndexByte(template, '?'); q >= 0 && q < at {
		escaped = url.QueryEscape(customerID)
	}
	return template[:at] + escaped + template[at+len(CustomerPlaceholder):]
}

// CustomerLinkSettings is the tenant template, empty when not configured.
type CustomerLinkSettings struct {
	Template string
	Version  int64
}

func loadCustomerTemplate(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (CustomerLinkSettings, error) {
	var out CustomerLinkSettings
	err := tx.QueryRow(ctx, `SELECT url_template, version FROM erp_customer_link_settings WHERE tenant_id = $1`, tenant).Scan(&out.Template, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerLinkSettings{}, nil
	}
	if err != nil {
		return CustomerLinkSettings{}, mapPG(err)
	}
	return out, nil
}

// CustomerLink reads the tenant template. Only an administrator calls it.
func (p *Panel) CustomerLink(ctx context.Context, actor Principal) (CustomerLinkSettings, error) {
	if actor.Role != "admin" || actor.TenantID == uuid.Nil {
		return CustomerLinkSettings{}, ErrDenied
	}
	var out CustomerLinkSettings
	err := p.db.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadCustomerTemplate(ctx, tx, actor.TenantID)
		return err
	})
	return out, err
}

// SetCustomerLink stores or clears (empty template) the tenant template with
// optimistic version control. Version 0 means "not configured yet".
func (p *Panel) SetCustomerLink(ctx context.Context, actor Principal, template string, version int64) (CustomerLinkSettings, error) {
	if actor.Role != "admin" || actor.TenantID == uuid.Nil {
		return CustomerLinkSettings{}, ErrDenied
	}
	clean := strings.TrimSpace(template)
	if clean != "" {
		var err error
		if clean, err = ValidateCustomerTemplate(clean); err != nil {
			return CustomerLinkSettings{}, err
		}
	}
	var out CustomerLinkSettings
	err := p.db.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadCustomerTemplate(ctx, tx, actor.TenantID)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrConflict
		}
		switch {
		case clean == "" && current.Version == 0:
			return nil
		case clean == "":
			_, err = tx.Exec(ctx, `DELETE FROM erp_customer_link_settings WHERE tenant_id = $1 AND version = $2`, actor.TenantID, version)
			return mapPG(err)
		case current.Version == 0:
			out = CustomerLinkSettings{Template: clean, Version: 1}
			_, err = tx.Exec(ctx, `INSERT INTO erp_customer_link_settings (tenant_id, url_template) VALUES ($1, $2)`, actor.TenantID, clean)
			return mapPG(err)
		default:
			out = CustomerLinkSettings{Template: clean, Version: version + 1}
			tag, err := tx.Exec(ctx, `UPDATE erp_customer_link_settings SET url_template = $3, version = version + 1, updated_at = now() WHERE tenant_id = $1 AND version = $2`, actor.TenantID, version, clean)
			if err != nil {
				return mapPG(err)
			}
			if tag.RowsAffected() != 1 {
				return ErrConflict
			}
			return nil
		}
	})
	if err != nil {
		return CustomerLinkSettings{}, err
	}
	return out, nil
}
