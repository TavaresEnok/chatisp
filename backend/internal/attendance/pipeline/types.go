package pipeline

import (
	"context"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
)

// AppResolver opens the App Secret bound to one trusted endpoint.
type AppResolver interface {
	ResolveApp(ctx context.Context, appID string) (configuration.TrustedApp, error)
}

// RouteResolver reads the app–WABA–number binding to determine the tenant and inbox.
type RouteResolver interface {
	ResolveRoute(ctx context.Context, appID, phoneNumberID string) (store.Route, error)
}

// MaterialResolver opens the send token and app secret for one connector version.
type MaterialResolver interface {
	ResolveMaterial(ctx context.Context, tenantID, connectorID uuid.UUID, version int64) (configuration.Material, error)
}

// CredentialReporter marks a channel connector degraded when a credential is rejected.
type CredentialReporter interface {
	ReportInvalidCredential(ctx context.Context, tenantID, connectorID uuid.UUID, version int64) error
}

// DriverFactory returns a text driver for outbound dispatch.
type DriverFactory interface {
	DriverFor(ctx context.Context, tenantID, connectorID uuid.UUID, material configuration.Material) (whatsapp.TextDriver, error)
}

// VerifyTokenProvider provides the verification token used during Meta GET webhook challenges.
type VerifyTokenProvider interface {
	VerifyToken(ctx context.Context, appID string) (string, error)
}

// VerifyTokenFunc adapts a simple function to VerifyTokenProvider.
type VerifyTokenFunc func(ctx context.Context, appID string) (string, error)

// VerifyToken calls f.
func (f VerifyTokenFunc) VerifyToken(ctx context.Context, appID string) (string, error) {
	return f(ctx, appID)
}

// StaticVerifyTokens maps appID to static verification tokens.
type StaticVerifyTokens map[string]string

// VerifyToken returns the static token or ErrNotFound if missing.
func (s StaticVerifyTokens) VerifyToken(_ context.Context, appID string) (string, error) {
	tok, ok := s[appID]
	if !ok || tok == "" {
		return "", ErrNotFound
	}
	return tok, nil
}

// MessageItemPayload is the tenant-segregated payload stored in webhook_items for messages.
type MessageItemPayload struct {
	AppID         string                   `json:"app_id"`
	WABAID        string                   `json:"waba_id"`
	PhoneNumberID string                   `json:"phone_number_id"`
	From          string                   `json:"from"`
	Text          string                   `json:"text"`
	ExternalID    string                   `json:"external_id"`
	DedupKey      string                   `json:"dedup_key"`
	OriginAt      time.Time                `json:"origin_at"`
	ProfileName   string                   `json:"profile_name,omitempty"`
	Content       metacloud.InboundContent `json:"content,omitempty"`
}

// StatusItemPayload is the tenant-segregated payload stored in webhook_items for status events.
type StatusItemPayload struct {
	AppID             string    `json:"app_id"`
	WABAID            string    `json:"waba_id"`
	PhoneNumberID     string    `json:"phone_number_id"`
	ExternalMessageID string    `json:"external_message_id"`
	ExternalEventID   string    `json:"external_event_id,omitempty"`
	Kind              string    `json:"kind"`
	OriginAt          time.Time `json:"origin_at"`
	RecipientID       string    `json:"recipient_id,omitempty"`
	CallbackData      string    `json:"callback_data,omitempty"`
	BillingCategory   string    `json:"billing_category,omitempty"`
	Billable          string    `json:"billable,omitempty"`
	Detail            string    `json:"detail,omitempty"`
}
