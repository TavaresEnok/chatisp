package configuration

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/uuid"
)

const (
	entityConnector = "channel_connector"
	entityApp       = "meta_app"
	fieldToken      = "access_token"
	fieldAppSecret  = "app_secret"

	actionConfigured        = "channel.configure"
	actionCredentialInvalid = "channel.credential_invalid"

	connectorMeta = "meta_cloud"
)

// Manual is the administrator form. It has no tenant and no database setting.
// AccessToken and AppSecret are write-only. They are not returned or logged.
type Manual struct {
	InboxName     string
	DisplayNumber string
	AppID         string
	AppSecret     string
	WABAID        string
	PhoneNumberID string
	AccessToken   string
}

// Box is the panel view of one Meta inbox. It carries no ciphertext and no secret.
type Box struct {
	ConnectorID         uuid.UUID
	InboxID             uuid.UUID
	Name                string
	AppID               string
	WABAID              string
	PhoneNumberID       string
	DisplayNumber       string
	Status              string
	Enabled             bool
	Version             int64
	TokenKeyVersion     string
	AppSecretKeyVersion string
}

// Alert is one persistent administrative mark for an invalid credential.
type Alert struct {
	ID          uuid.UUID
	ConnectorID uuid.UUID
	Action      string
	Result      string
	CreatedAt   string
}

// Status is the tenant summary an administrator may read.
type Status struct {
	Boxes  []Box
	Alerts []Alert
}

// QuarantineRef identifies one ingress quarantine row for an app of this tenant.
// It does not include the raw envelope.
type QuarantineRef struct {
	ID         uuid.UUID
	DeliveryID uuid.UUID
	Reason     string
	CreatedAt  string
}

// ItemRef identifies one tenant webhook item held in quarantine.
// Payload and the original envelope stay out of this view.
type ItemRef struct {
	ID          uuid.UUID
	InboxID     uuid.UUID
	ExternalKey string
	Kind        string
	State       string
}

// DiscardedJob is one River job discarded for this tenant.
// Arguments and error text are omitted.
type DiscardedJob struct {
	ID      int64
	Kind    string
	Queue   string
	State   string
	Attempt int
}

// Inspection is the limited operations view for the active tenant.
type Inspection struct {
	Quarantine []QuarantineRef
	Items      []ItemRef
	Discarded  []DiscardedJob
}

// Material is the in-process credential used by a later dispatcher.
// It must not be serialized to the panel or written to a log.
type Material struct {
	Version       int64
	AppID         string
	WABAID        string
	PhoneNumberID string
	AccessToken   string
	AppSecret     string
}

// TrustedApp is the in-process App Secret for one endpoint binding.
// It has no send token. Webhook authentication uses it before any body is trusted.
// It must not be serialized to the panel or written to a log.
type TrustedApp struct {
	AppID     string
	AppSecret string
}

// String redacts the secrets.
func (Material) String() string { return "material redigido" }

// GoString redacts the secrets.
func (Material) GoString() string { return "material redigido" }

// LogValue redacts the secrets if a caller passes the value to slog.
func (Material) LogValue() slog.Value { return slog.StringValue("redigido") }

// MarshalJSON refuses to place credentials on a response body.
func (Material) MarshalJSON() ([]byte, error) {
	return nil, errors.New("material de credencial não é serializável")
}

// UnmarshalJSON refuses to rebuild credentials from an external payload.
func (*Material) UnmarshalJSON([]byte) error {
	return errors.New("material de credencial não é serializável")
}

// String redacts the App Secret.
func (TrustedApp) String() string { return "aplicativo redigido" }

// GoString redacts the App Secret.
func (TrustedApp) GoString() string { return "aplicativo redigido" }

// LogValue redacts the App Secret if a caller passes the value to slog.
func (TrustedApp) LogValue() slog.Value { return slog.StringValue("redigido") }

// MarshalJSON refuses to place the App Secret on a response body.
func (TrustedApp) MarshalJSON() ([]byte, error) {
	return nil, errors.New("segredo de aplicativo não é serializável")
}

// UnmarshalJSON refuses to rebuild the App Secret from an external payload.
func (*TrustedApp) UnmarshalJSON([]byte) error {
	return errors.New("segredo de aplicativo não é serializável")
}

// compile-time check that the redacted JSON path stays explicit.
var (
	_ json.Marshaler = Material{}
	_ json.Marshaler = TrustedApp{}
)
