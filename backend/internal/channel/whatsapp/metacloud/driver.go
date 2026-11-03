package metacloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"chatisp/internal/channel/whatsapp"
)

const (
	userAgentOfficial   = "chatisp-metacloud"
	userAgentSimulation = "chatisp-metacloud-simulation"
	maxResponseBytes    = 64 << 10
)

// Driver sends plain text through one configured Cloud API endpoint.
// Acceptance is a provider message id. Delivery and read stay on webhooks.
type Driver struct {
	endpoint endpoint
}

// Activate checks the endpoint and returns a driver. It does not dial out.
// Official mode requires an explicit Graph version and a primary documentation
// URL on developers.facebook.com. The URL is recorded, not fetched.
func Activate(cfg Config) (*Driver, error) {
	ep, err := activate(cfg)
	if err != nil {
		return nil, err
	}
	return &Driver{endpoint: ep}, nil
}

// Simulation reports whether this driver talks to the local stand-in.
func (d *Driver) Simulation() bool {
	return d != nil && d.endpoint.simulation
}

// Capabilities reports text plus the receipt kinds this decoder understands.
// Media, templates, interactive messages, pricing and the customer window
// are explicit gaps: this driver does not implement them.
func (d *Driver) Capabilities() whatsapp.Capabilities {
	if d == nil {
		return whatsapp.Capabilities{}
	}
	return whatsapp.Capabilities{
		Text:             true,
		DeliveryReceipts: true,
		ReadReceipts:     true,
	}
}

// SendText posts one text message. Invalid input fails before dispatch.
// The context should carry a deadline. If it does not, the client applies
// maxRequestTimeout so the read cannot block forever. After the request is
// written, a missing response, a timeout, HTTP 429 and HTTP 5xx become
// uncertain. They are not acceptance and they are not a safe retry.
// Graph error 190 becomes RejectionTokenInvalid.
func (d *Driver) SendText(ctx context.Context, req whatsapp.TextRequest) (whatsapp.Result, error) {
	if d == nil || d.endpoint.client == nil || d.endpoint.token == "" {
		return whatsapp.Result{}, errDriverUnavailable
	}
	if ctx == nil {
		return whatsapp.Result{}, errContextMissing
	}
	if err := whatsapp.Validate(req); err != nil {
		return whatsapp.Result{}, err
	}
	if req.ConnectorType != whatsapp.ConnectorMetaCloud {
		return whatsapp.Result{}, errConnectorUnavailable
	}
	if utf8.RuneCountInString(req.Text) > MaxTextRunes {
		return whatsapp.Result{}, errTextInvalid
	}
	if err := ctx.Err(); err != nil {
		obs := whatsapp.Observation{NotDispatched: true, Detail: "despacho não iniciado"}
		if errors.Is(err, context.Canceled) {
			obs.CancelBeforeDispatch = true
			obs.Detail = "envio cancelado antes do despacho"
		}
		return d.finish(req.AttemptID, obs), nil
	}
	target, err := d.endpoint.messageURL()
	if err != nil {
		return d.finish(req.AttemptID, whatsapp.Observation{
			NotDispatched: true,
			Detail:        "despacho não iniciado",
		}), nil
	}
	payload, err := marshalText(req)
	if err != nil {
		return whatsapp.Result{}, errTextInvalid
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return d.finish(req.AttemptID, whatsapp.Observation{
			NotDispatched: true,
			Detail:        "despacho não iniciado",
		}), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.endpoint.token)
	if d.endpoint.simulation {
		httpReq.Header.Set("User-Agent", userAgentSimulation)
	} else {
		httpReq.Header.Set("User-Agent", userAgentOfficial)
	}

	var gotConn, getConn atomic.Bool
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(httpReq.Context(), &httptrace.ClientTrace{
		GetConn: func(string) { getConn.Store(true) },
		GotConn: func(httptrace.GotConnInfo) { gotConn.Store(true) },
	}))

	resp, err := d.endpoint.client.Do(httpReq)
	if err != nil {
		// Only the standard transport's connection acquisition trace establishes
		// that no connection was available for any request bytes. Once a
		// connection is acquired, even a failed or absent WroteRequest callback
		// cannot rule out a partial write. Custom transports need not trace at all.
		_, standardTransport := d.endpoint.client.Transport.(*http.Transport)
		if standardTransport && getConn.Load() && !gotConn.Load() {
			obs := whatsapp.Observation{NotDispatched: true, Detail: "despacho não iniciado"}
			if errors.Is(ctx.Err(), context.Canceled) {
				obs.CancelBeforeDispatch = true
				obs.Detail = "envio cancelado antes do despacho"
			}
			return d.finish(req.AttemptID, obs), nil
		}
		return d.finish(req.AttemptID, whatsapp.Observation{
			Detail: "resposta perdida depois do despacho",
		}), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return d.finish(req.AttemptID, classifyHTTP(resp.StatusCode, body)), nil
}

func (d *Driver) finish(id whatsapp.AttemptID, obs whatsapp.Observation) whatsapp.Result {
	result := whatsapp.Classify(obs)
	result.AttemptID = id
	result.Simulation = d.endpoint.simulation
	return result
}

type textPayload struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Text             struct {
		Body string `json:"body"`
	} `json:"text"`
	CallbackData string `json:"biz_opaque_callback_data,omitempty"`
}

func marshalText(req whatsapp.TextRequest) ([]byte, error) {
	payload := textPayload{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               string(req.RecipientID),
		Type:             "text",
	}
	payload.Text.Body = req.Text
	if req.CallbackData != "" {
		payload.CallbackData = req.CallbackData
	}
	return json.Marshal(payload)
}

type graphEnvelope struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Error *graphFailure `json:"error"`
}

type graphFailure struct {
	Code        int  `json:"code"`
	Subcode     int  `json:"error_subcode"`
	IsTransient bool `json:"is_transient"`
}

func classifyHTTP(status int, body []byte) whatsapp.Observation {
	if status >= 300 && status < 400 {
		return whatsapp.Observation{
			HTTPStatus: status,
			Detail:     "redirecionamento recusado",
		}
	}
	var parsed graphEnvelope
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			parsed = graphEnvelope{}
		}
	}
	messageID := ""
	if len(parsed.Messages) > 0 {
		messageID = strings.TrimSpace(parsed.Messages[0].ID)
	}
	code := 0
	transient := false
	if parsed.Error != nil {
		code = parsed.Error.Code
		transient = parsed.Error.IsTransient
	}
	if messageID != "" && code != 0 {
		return whatsapp.Observation{
			HTTPStatus: status,
			Detail:     "evidências conflitantes; resultado incerto",
		}
	}
	if code == 190 {
		return whatsapp.Observation{
			HTTPStatus:    status,
			RejectionCode: RejectionTokenInvalid,
			Detail:        "token do canal inválido",
		}
	}
	if status == http.StatusOK && messageID != "" {
		return whatsapp.Observation{
			AcceptedMessageID: messageID,
			HTTPStatus:        status,
			Detail:            "envio aceito pelo conector",
		}
	}
	if uncertainStatus(status, code, transient) {
		detail := "resultado remoto desconhecido"
		switch {
		case status == http.StatusTooManyRequests || code == 130429:
			detail = "resultado incerto; HTTP 429 não autoriza repetição"
		case status >= 500:
			detail = "resultado incerto; HTTP 5xx não autoriza repetição"
		case status == http.StatusRequestTimeout:
			detail = "resposta perdida depois do despacho"
		}
		return whatsapp.Observation{HTTPStatus: status, Detail: detail}
	}
	if status >= 400 && status < 500 && code > 0 && code < 1_000_000 {
		return whatsapp.Observation{
			HTTPStatus:    status,
			RejectionCode: "graph_" + strconv.Itoa(code),
			Detail:        "rejeição definitiva do conector",
		}
	}
	return whatsapp.Observation{
		HTTPStatus: status,
		Detail:     "resultado remoto desconhecido",
	}
}

func uncertainStatus(status, code int, transient bool) bool {
	if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500 {
		return true
	}
	if status == http.StatusConflict || status == 425 {
		return true
	}
	if transient || code == 1 || code == 2 || code == 130429 || code == 131016 {
		return true
	}
	return false
}

// SendTemplate refuses before any HTTP client, trace or Graph URL is used.
// Official template delivery is not this batch. The simulation driver is the
// only identified template sender.
func (d *Driver) SendTemplate(context.Context, whatsapp.TemplateRequest) (whatsapp.Result, error) {
	return whatsapp.Result{
		Local:         whatsapp.StateFailed,
		RejectionCode: "template_not_authorized",
		Detail:        "template oficial não chama Graph neste marco",
	}, nil
}

var _ whatsapp.TextDriver = (*Driver)(nil)
var _ whatsapp.TemplateSender = (*Driver)(nil)
