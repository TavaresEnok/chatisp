package httpapi

import (
	"encoding/json"
	"errors"

	"chatisp/internal/channel/configuration"
	"github.com/google/uuid"
)

type manualBody struct {
	InboxName     string `json:"inbox_name"`
	DisplayNumber string `json:"display_number"`
	AppID         string `json:"app_id"`
	AppSecret     string `json:"app_secret" doc:"Segredo somente para escrita."`
	WABAID        string `json:"waba_id"`
	PhoneNumberID string `json:"phone_number_id"`
	AccessToken   string `json:"access_token" doc:"Token somente para escrita."`
}

// Do not reflect malformed bodies or field values in automatic Huma errors.
func (b *manualBody) UnmarshalJSON(data []byte) error {
	type plain manualBody
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return errors.New("corpo inválido")
	}
	*b = manualBody(v)
	return nil
}

type box struct {
	ConnectorID         uuid.UUID `json:"connector_id"`
	InboxID             uuid.UUID `json:"inbox_id"`
	Name                string    `json:"name"`
	AppID               string    `json:"app_id"`
	WABAID              string    `json:"waba_id"`
	PhoneNumberID       string    `json:"phone_number_id"`
	DisplayNumber       string    `json:"display_number"`
	Status              string    `json:"status"`
	Enabled             bool      `json:"enabled"`
	Version             int64     `json:"version"`
	TokenKeyVersion     string    `json:"token_key_version"`
	AppSecretKeyVersion string    `json:"app_secret_key_version"`
}

func boxFrom(b configuration.Box) box {
	return box{b.ConnectorID, b.InboxID, b.Name, b.AppID, b.WABAID, b.PhoneNumberID, b.DisplayNumber, b.Status, b.Enabled, b.Version, b.TokenKeyVersion, b.AppSecretKeyVersion}
}

type alert struct {
	ID          uuid.UUID `json:"id"`
	ConnectorID uuid.UUID `json:"connector_id"`
	Action      string    `json:"action"`
	Result      string    `json:"result"`
	CreatedAt   string    `json:"created_at"`
}
type quarantine struct {
	ID         uuid.UUID `json:"id"`
	DeliveryID uuid.UUID `json:"delivery_id"`
	Reason     string    `json:"reason"`
	CreatedAt  string    `json:"created_at"`
}
type item struct {
	ID          uuid.UUID `json:"id"`
	InboxID     uuid.UUID `json:"inbox_id"`
	ExternalKey string    `json:"external_key"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
}
type job struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Queue   string `json:"queue"`
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
}
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type configureInput struct {
	Credentials
	RawBody []byte
}
type ackInput struct {
	Credentials
	AlertID string `path:"alert_id"`
}
type boxOutput struct{ Body box }
type statusOutput struct {
	Body struct {
		Boxes  []box   `json:"boxes"`
		Alerts []alert `json:"alerts"`
	}
}
type inspectionOutput struct {
	Body struct {
		Quarantine []quarantine `json:"quarantine"`
		Items      []item       `json:"items"`
		Discarded  []job        `json:"discarded"`
	}
}
