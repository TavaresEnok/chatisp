package embed

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"chatisp/internal/platform/urlguard"
	"github.com/google/uuid"
)

const (
	ContextType = "chatisp.embed.context"
	ContextV    = 1
)

var (
	ErrOrigin      = errors.New("origem do painel recusada")
	ErrURL         = errors.New("url do painel recusada")
	ErrInvalid     = errors.New("configuração inválida")
	ErrUnavailable = errors.New("serviço indisponível")
)

// Context is the only postMessage body sent to the external panel.
// It carries the conversation identifier, the local protocol, the sector and the inbox.
// Message text, documents and secrets are not fields of this type.
type Context struct {
	Type           string    `json:"type"`
	V              int       `json:"v"`
	ConversationID uuid.UUID `json:"conversation_id"`
	Protocol       string    `json:"protocol"`
	SectorID       uuid.UUID `json:"sector_id"`
	InboxID        uuid.UUID `json:"inbox_id"`
}

// Check accepts a provider origin and panel URL only when urlguard allows the
// destination and the URL origin is exactly the configured origin.
func Check(policy *urlguard.Policy, origin, panel string) (string, string, error) {
	if policy == nil {
		return "", "", urlguard.ErrConfig
	}
	normOrigin, err := normalizeOrigin(policy, origin)
	if err != nil {
		return "", "", err
	}
	panelOrigin, normURL, err := normalizePanel(policy, panel)
	if err != nil {
		return "", "", err
	}
	if panelOrigin != normOrigin {
		return "", "", ErrOrigin
	}
	return normOrigin, normURL, nil
}

// ContextFrom builds the postMessage payload. Extra conversation data cannot be passed here.
func ContextFrom(conversation, sector, inbox uuid.UUID, protocol string) (Context, error) {
	if conversation == uuid.Nil || sector == uuid.Nil || inbox == uuid.Nil || !validProtocol(protocol) {
		return Context{}, ErrInvalid
	}
	return Context{
		Type:           ContextType,
		V:              ContextV,
		ConversationID: conversation,
		Protocol:       protocol,
		SectorID:       sector,
		InboxID:        inbox,
	}, nil
}

// Decide hides the panel when the stored URL is no longer allowed or the context is incomplete.
func Decide(policy *urlguard.Policy, origin, panel string, conversation uuid.UUID, protocol string, sector, inbox uuid.UUID) View {
	normOrigin, normURL, err := Check(policy, origin, panel)
	if err != nil {
		return View{}
	}
	ctx, err := ContextFrom(conversation, sector, inbox, protocol)
	if err != nil {
		return View{}
	}
	return View{Embed: true, URL: normURL, Origin: normOrigin, Context: &ctx}
}

func normalizeOrigin(policy *urlguard.Policy, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrOrigin
	}
	if !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	target, err := policy.Inspect(urlguard.Scope{}, raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrOrigin, err)
	}
	if target.URL.Path != "/" || target.URL.RawQuery != "" || target.URL.Fragment != "" {
		return "", ErrOrigin
	}
	return target.URL.Scheme + "://" + target.URL.Host, nil
}

func normalizePanel(policy *urlguard.Policy, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, " \t\r\n") {
		return "", "", ErrURL
	}
	target, err := policy.Inspect(urlguard.Scope{}, raw)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrURL, err)
	}
	if target.URL.RawQuery != "" || target.URL.Fragment != "" {
		return "", "", ErrURL
	}
	return target.URL.Scheme + "://" + target.URL.Host, target.URL.String(), nil
}

func validProtocol(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		digit := c >= '0' && c <= '9'
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if c == '-' {
			if i == 0 {
				return false
			}
			continue
		}
		if !digit && !letter {
			return false
		}
	}
	return true
}

// MarshalJSON keeps the payload limited to the struct fields even if a caller embeds Context.
func (c Context) MarshalJSON() ([]byte, error) {
	if c.Type != ContextType || c.V != ContextV {
		return nil, ErrInvalid
	}
	type wire struct {
		Type           string    `json:"type"`
		V              int       `json:"v"`
		ConversationID uuid.UUID `json:"conversation_id"`
		Protocol       string    `json:"protocol"`
		SectorID       uuid.UUID `json:"sector_id"`
		InboxID        uuid.UUID `json:"inbox_id"`
	}
	return json.Marshal(wire(c))
}
