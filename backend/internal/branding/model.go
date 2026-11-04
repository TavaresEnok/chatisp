package branding

import (
	"errors"

	"github.com/google/uuid"
)

const (
	// FallbackName is the shell label when the provider has no stored name.
	FallbackName = "Chat ISP"
	// MaxLogoBytes is the stored and uploaded ceiling, 256 KiB.
	MaxLogoBytes = 256 * 1024
	// MaxLogoEdge is the maximum width and height in pixels.
	MaxLogoEdge = 1024
	// MaxNameRunes matches tenant_retention_profiles.display_name.
	MaxNameRunes = 80
)

var (
	// ErrForbidden means the caller cannot read or change this provider.
	ErrForbidden = errors.New("acesso negado")
	// ErrInvalid means the name or the image was refused.
	ErrInvalid = errors.New("dados inválidos")
	// ErrNotFound means this provider has no logo.
	ErrNotFound = errors.New("logo não encontrado")
	// ErrUnavailable means the database cannot answer.
	ErrUnavailable = errors.New("serviço indisponível")
)

// Principal is the panel actor. Superadmin is rejected for operational reads.
type Principal struct {
	TenantID       uuid.UUID
	UserID         uuid.UUID
	MembershipID   uuid.UUID
	Role           string
	IsSuperadmin   bool
	EnrollmentOnly bool
}

// Identity is the name already stored for retention, plus logo metadata.
// It never includes image bytes, deadlines or automatic purge.
type Identity struct {
	DisplayName  string `json:"display_name"`
	FallbackName string `json:"fallback_name"`
	HasLogo      bool   `json:"has_logo"`
	ContentType  string `json:"content_type,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

// Image is a re-encoded PNG or JPEG that already passed the limits.
type Image struct {
	ContentType string
	Bytes       []byte
	Width       int
	Height      int
}
