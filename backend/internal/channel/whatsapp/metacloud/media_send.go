package metacloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/platform/urlguard"
)

// LoopbackOfficial binds the official media upload to an httptest stand-in.
// The host must be loopback. Production activation stays on Activate and does
// not accept this base URL.
func LoopbackOfficial(baseURL, version, phoneID, token string, client *http.Client) (*Driver, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || !loopback(u.Host) {
		return nil, errSimulationEndpoint
	}
	if u.Hostname() == OfficialHost || strings.HasSuffix(u.Hostname(), ".facebook.com") {
		return nil, errSimulationEndpoint
	}
	version, err = parseVersion(version)
	if err != nil {
		return nil, err
	}
	phoneID, err = parsePhoneID(phoneID)
	if err != nil {
		return nil, err
	}
	token, err = parseAccessToken(token)
	if err != nil {
		return nil, err
	}
	path := strings.TrimRight(u.Path, "/")
	return &Driver{endpoint: endpoint{
		mode: ModeOfficial, version: version, phoneID: phoneID, token: token,
		base:       &url.URL{Scheme: u.Scheme, Host: u.Host, Path: path},
		simulation: false, client: newClient(client),
	}}, nil
}

// SendMedia uploads bytes or sends a guarded link, then posts the message.
// Simulation accepts locally and does not call Graph. A link that urlguard
// rejects is a definitive failure and does not open a connection.
func (d *Driver) SendMedia(ctx context.Context, req whatsapp.MediaRequest) (whatsapp.Result, error) {
	if d == nil || d.endpoint.token == "" {
		return whatsapp.Result{}, errDriverUnavailable
	}
	if ctx == nil {
		return whatsapp.Result{}, errContextMissing
	}
	if err := whatsapp.ValidateMedia(req); err != nil {
		return whatsapp.Result{}, err
	}
	if req.ConnectorType != whatsapp.ConnectorMetaCloud {
		return whatsapp.Result{}, errConnectorUnavailable
	}
	if err := ctx.Err(); err != nil {
		obs := whatsapp.Observation{NotDispatched: true, Detail: "despacho não iniciado"}
		if errors.Is(err, context.Canceled) {
			obs.CancelBeforeDispatch = true
			obs.Detail = "envio cancelado antes do despacho"
		}
		return d.finish(req.AttemptID, obs), nil
	}
	if req.Link != "" && len(req.Body) == 0 {
		if err := guardMediaLink(req.Link); err != nil {
			return d.finish(req.AttemptID, whatsapp.Observation{
				RejectionCode: "url_recusada",
				Detail:        "link de mídia recusado",
			}), nil
		}
	}
	if d.endpoint.simulation {
		return d.finish(req.AttemptID, whatsapp.Observation{
			AcceptedMessageID: "sim-media:" + string(req.AttemptID),
			Detail:            "mídia aceita na simulação sem Graph",
		}), nil
	}
	if d.endpoint.client == nil {
		return whatsapp.Result{}, errDriverUnavailable
	}
	mediaID := ""
	link := ""
	if len(req.Body) > 0 {
		if req.MIME == "" {
			return whatsapp.Result{}, errors.New("tipo do arquivo inválido")
		}
		id, obs, err := d.uploadMedia(ctx, req)
		if err != nil {
			return whatsapp.Result{}, err
		}
		if id == "" {
			return d.finish(req.AttemptID, obs), nil
		}
		mediaID = id
	} else if req.Link != "" {
		link = req.Link
	} else {
		return d.finish(req.AttemptID, whatsapp.Observation{
			RejectionCode: "midia_indisponivel",
			Detail:        "mídia sem bytes para envio",
		}), nil
	}
	target, err := d.mediaMessageURL()
	if err != nil {
		return d.finish(req.AttemptID, whatsapp.Observation{
			NotDispatched: true,
			Detail:        "despacho não iniciado",
		}), nil
	}
	payload, err := marshalMedia(req, mediaID, link)
	if err != nil {
		return whatsapp.Result{}, err
	}
	obs := d.postJSON(ctx, target, payload)
	return d.finish(req.AttemptID, obs), nil
}

func guardMediaLink(raw string) error {
	policy, err := urlguard.NewPolicy()
	if err != nil {
		return err
	}
	target, err := policy.Inspect(urlguard.Scope{}, raw)
	if err != nil {
		return err
	}
	if target.URL == nil || target.URL.Scheme != "https" {
		return urlguard.ErrScheme
	}
	return nil
}

func (d *Driver) mediaMessageURL() (*url.URL, error) {
	if d.endpoint.base == nil || d.endpoint.version == "" || d.endpoint.phoneID == "" {
		return nil, errMode
	}
	u := *d.endpoint.base
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(d.endpoint.base.Path, "/") + "/" + d.endpoint.version + "/" + d.endpoint.phoneID + "/messages"
	return &u, nil
}

func (d *Driver) mediaUploadURL() (*url.URL, error) {
	u, err := d.mediaMessageURL()
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimSuffix(u.Path, "/messages") + "/media"
	return u, nil
}

func (d *Driver) uploadMedia(ctx context.Context, req whatsapp.MediaRequest) (string, whatsapp.Observation, error) {
	target, err := d.mediaUploadURL()
	if err != nil {
		return "", whatsapp.Observation{NotDispatched: true, Detail: "despacho não iniciado"}, nil
	}
	body, contentType, err := mediaMultipart(req)
	if err != nil {
		return "", whatsapp.Observation{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return "", whatsapp.Observation{NotDispatched: true, Detail: "despacho não iniciado"}, nil
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Authorization", "Bearer "+d.endpoint.token)
	httpReq.Header.Set("User-Agent", userAgentOfficial)
	resp, err := d.endpoint.client.Do(httpReq)
	if err != nil {
		return "", whatsapp.Observation{Detail: "resposta perdida depois do despacho"}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	obs := classifyHTTP(resp.StatusCode, raw)
	if obs.RejectionCode != "" || (obs.AcceptedMessageID == "" && resp.StatusCode != http.StatusOK) {
		if obs.Detail == "" {
			obs.Detail = "upload de mídia recusado"
		}
		return "", obs, nil
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || strings.TrimSpace(parsed.ID) == "" {
		return "", whatsapp.Observation{HTTPStatus: resp.StatusCode, Detail: "resultado remoto desconhecido"}, nil
	}
	return strings.TrimSpace(parsed.ID), whatsapp.Observation{}, nil
}

func mediaMultipart(req whatsapp.MediaRequest) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("messaging_product", "whatsapp"); err != nil {
		return nil, "", err
	}
	mime := graphUploadMIME(req)
	if err := w.WriteField("type", mime); err != nil {
		return nil, "", err
	}
	name := req.FileName
	if name == "" {
		name = "arquivo.bin"
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	header.Set("Content-Type", mime)
	part, err := w.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(req.Body); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// graphUploadMIME is the type the Cloud API accepts for the media upload.
// Converted OGG/Opus is stored as application/ogg (detected signature) and
// still recognized by needsOpus. Graph documents only audio/ogg for that codec.
func graphUploadMIME(req whatsapp.MediaRequest) string {
	if req.Kind == whatsapp.MediaAudio && (req.MIME == "application/ogg" || req.MIME == "video/ogg") {
		return "audio/ogg"
	}
	return req.MIME
}

func (d *Driver) postJSON(ctx context.Context, target *url.URL, payload []byte) whatsapp.Observation {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return whatsapp.Observation{NotDispatched: true, Detail: "despacho não iniciado"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.endpoint.token)
	httpReq.Header.Set("User-Agent", userAgentOfficial)
	resp, err := d.endpoint.client.Do(httpReq)
	if err != nil {
		return whatsapp.Observation{Detail: "resposta perdida depois do despacho"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return classifyHTTP(resp.StatusCode, raw)
}

type mediaObject struct {
	ID       string `json:"id,omitempty"`
	Link     string `json:"link,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type mediaEnvelope struct {
	MessagingProduct string       `json:"messaging_product"`
	RecipientType    string       `json:"recipient_type"`
	To               string       `json:"to"`
	Type             string       `json:"type"`
	Image            *mediaObject `json:"image,omitempty"`
	Document         *mediaObject `json:"document,omitempty"`
	Audio            *mediaObject `json:"audio,omitempty"`
	Video            *mediaObject `json:"video,omitempty"`
	Context          *struct {
		MessageID string `json:"message_id"`
	} `json:"context,omitempty"`
	CallbackData string `json:"biz_opaque_callback_data,omitempty"`
}

func marshalMedia(req whatsapp.MediaRequest, mediaID, link string) ([]byte, error) {
	obj := &mediaObject{ID: mediaID, Link: link}
	if req.Kind != whatsapp.MediaAudio && req.Caption != "" {
		obj.Caption = req.Caption
	}
	if req.Kind == whatsapp.MediaDocument && req.FileName != "" {
		obj.Filename = req.FileName
	}
	payload := mediaEnvelope{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               string(req.RecipientID),
		Type:             string(req.Kind),
	}
	switch req.Kind {
	case whatsapp.MediaImage:
		payload.Image = obj
	case whatsapp.MediaDocument:
		payload.Document = obj
	case whatsapp.MediaAudio:
		payload.Audio = obj
	case whatsapp.MediaVideo:
		payload.Video = obj
	default:
		return nil, errors.New("tipo de mídia inválido")
	}
	if req.ReplyToProviderID != "" {
		payload.Context = &struct {
			MessageID string `json:"message_id"`
		}{MessageID: req.ReplyToProviderID}
	}
	if req.CallbackData != "" {
		payload.CallbackData = req.CallbackData
	}
	return json.Marshal(payload)
}
