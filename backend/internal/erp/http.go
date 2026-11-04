package erp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
)

// Dependencies are the panel session and the simulated ERP panel.
type Dependencies struct {
	Auth           *auth.Service
	Panel          *Panel
	TrustedOrigins []string
}

// Mount registers the panel routes on chi. They stay outside the OpenAPI document.
func Mount(r interface {
	Method(method, pattern string, h http.Handler)
}, deps Dependencies) {
	h := handler{deps: deps}
	base := "/api/panel/conversations/{conversation_id}/erp"
	r.Method(http.MethodGet, base+"/links", http.HandlerFunc(h.listLinks))
	r.Method(http.MethodPost, base+"/links", http.HandlerFunc(h.link))
	r.Method(http.MethodDelete, base+"/links/{link_id}", http.HandlerFunc(h.unlink))
	r.Method(http.MethodPost, base+"/identify", http.HandlerFunc(h.identify))
	r.Method(http.MethodGet, base+"/summary", http.HandlerFunc(h.summary))
	r.Method(http.MethodGet, base+"/invoices", http.HandlerFunc(h.invoices))
	r.Method(http.MethodPost, base+"/second-copy", http.HandlerFunc(h.secondCopy))
	r.Method(http.MethodPost, base+"/protocol", http.HandlerFunc(h.protocol))
}

type handler struct{ deps Dependencies }

type linkBody struct {
	ExternalCustomerID string `json:"external_customer_id"`
	ExternalContractID string `json:"external_contract_id"`
	Selected           bool   `json:"selected"`
}

type identifyBody struct {
	Reference string `json:"reference"`
	LocalKey  string `json:"local_key"`
}

type secondBody struct {
	InvoiceID       string `json:"invoice_id"`
	Verification    string `json:"verification"`
	LocalKey        string `json:"local_key"`
	ClientRequestID string `json:"client_request_id"`
	Artifact        string `json:"artifact"`
}

type protocolBody struct {
	ExternalProtocol string `json:"external_protocol"`
}

func (h handler) listLinks(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.read(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Panel.ListLinks(r.Context(), actor, conversation)
	h.write(w, out, err)
}

func (h handler) link(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.writeActor(w, r)
	if !ok {
		return
	}
	var body linkBody
	if !decode(w, r, &body) {
		return
	}
	out, err := h.deps.Panel.Link(r.Context(), actor, conversation, LinkInput{
		ExternalCustomerID: body.ExternalCustomerID, ExternalContractID: body.ExternalContractID, Selected: body.Selected,
	})
	h.write(w, out, err)
}

func (h handler) unlink(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.writeActor(w, r)
	if !ok {
		return
	}
	link, err := uuid.Parse(chi.URLParam(r, "link_id"))
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "comando inválido")
		return
	}
	out, err := h.deps.Panel.Unlink(r.Context(), actor, conversation, link)
	h.write(w, out, err)
}

func (h handler) identify(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.writeActor(w, r)
	if !ok {
		return
	}
	var body identifyBody
	if !decode(w, r, &body) {
		return
	}
	out, err := h.deps.Panel.Identify(r.Context(), actor, conversation, body.Reference, body.LocalKey)
	h.write(w, out, err)
}

func (h handler) summary(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.read(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Panel.Summary(r.Context(), actor, conversation)
	h.write(w, out, err)
}

func (h handler) invoices(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.read(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Panel.Invoices(r.Context(), actor, conversation, r.URL.Query().Get("verification"), r.URL.Query().Get("local_key"))
	h.write(w, out, err)
}

func (h handler) secondCopy(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.writeActor(w, r)
	if !ok {
		return
	}
	var body secondBody
	if !decode(w, r, &body) {
		return
	}
	out, err := h.deps.Panel.SecondCopy(r.Context(), actor, conversation, SecondCopyInput{
		InvoiceID: body.InvoiceID, Verification: body.Verification, LocalKey: body.LocalKey,
		ClientRequestID: body.ClientRequestID, Artifact: body.Artifact,
	})
	h.write(w, out, err)
}

func (h handler) protocol(w http.ResponseWriter, r *http.Request) {
	actor, conversation, ok := h.writeActor(w, r)
	if !ok {
		return
	}
	var body protocolBody
	if !decode(w, r, &body) {
		return
	}
	out, err := h.deps.Panel.Protocol(r.Context(), actor, conversation, body.ExternalProtocol)
	h.write(w, out, err)
}

func (h handler) read(w http.ResponseWriter, r *http.Request) (Principal, uuid.UUID, bool) {
	return h.actor(w, r, false)
}

func (h handler) writeActor(w http.ResponseWriter, r *http.Request) (Principal, uuid.UUID, bool) {
	if !originAllowed(h.deps.TrustedOrigins, r.Header.Get("Origin")) {
		writeProblem(w, http.StatusForbidden, "origem não permitida")
		return Principal{}, uuid.Nil, false
	}
	return h.actor(w, r, true)
}

func (h handler) actor(w http.ResponseWriter, r *http.Request, write bool) (Principal, uuid.UUID, bool) {
	if h.deps.Auth == nil || h.deps.Panel == nil || h.deps.Panel.db == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return Principal{}, uuid.Nil, false
	}
	conversation, err := uuid.Parse(chi.URLParam(r, "conversation_id"))
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "comando inválido")
		return Principal{}, uuid.Nil, false
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	raw := ""
	if err == nil {
		raw = cookie.Value
	}
	session, err := h.deps.Auth.Authenticate(r.Context(), raw, r.Header.Get(auth.CSRFHeader), write)
	if err != nil {
		writeAuth(w, err)
		return Principal{}, uuid.Nil, false
	}
	if session.IsSuperadmin || session.EnrollmentOnly || session.TenantID == uuid.Nil || session.MembershipID == uuid.Nil {
		writeProblem(w, http.StatusForbidden, "acesso negado")
		return Principal{}, uuid.Nil, false
	}
	return Principal{
		TenantID: session.TenantID, UserID: session.UserID, Membership: session.MembershipID, Role: session.Role,
	}, conversation, true
}

func (h handler) write(w http.ResponseWriter, out Result, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewJSON(out))
}

func decode(w http.ResponseWriter, r *http.Request, dest any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil && !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusUnprocessableEntity, "comando inválido")
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	status, detail := http.StatusServiceUnavailable, "serviço indisponível"
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		status, detail = http.StatusUnauthorized, "não autenticado"
	case errors.Is(err, ErrDenied), errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, sectors.ErrForbidden):
		status, detail = http.StatusForbidden, "acesso negado"
	case errors.Is(err, ErrNotFound), errors.Is(err, sectors.ErrNotFound):
		status, detail = http.StatusNotFound, "registro não encontrado"
	case errors.Is(err, ErrConflict):
		status, detail = http.StatusConflict, "conflito de chave local"
	case errors.Is(err, ErrChoiceRequired):
		status, detail = http.StatusConflict, "vínculo precisa ser escolhido"
	case errors.Is(err, ErrDispatch):
		status, detail = http.StatusConflict, "janela ou consentimento recusou o envio"
	case errors.Is(err, ErrPhoneNotSelector):
		status, detail = http.StatusUnprocessableEntity, "telefone não escolhe o vínculo"
	case errors.Is(err, ErrReferenceRequired), errors.Is(err, ErrKeyRequired), errors.Is(err, ErrInvalid):
		status, detail = http.StatusUnprocessableEntity, "comando inválido"
	case errors.Is(err, ErrTimeout):
		status, detail = http.StatusServiceUnavailable, "tempo do erp esgotado"
	case errors.Is(err, ErrOpen):
		status, detail = http.StatusServiceUnavailable, "circuito do erp aberto"
	case errors.Is(err, ErrDown):
		status, detail = http.StatusServiceUnavailable, "erp indisponível"
	}
	writeProblem(w, status, detail)
}

func writeAuth(w http.ResponseWriter, err error) {
	writeErr(w, err)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": detail, "status": status, "detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func originAllowed(origins []string, got string) bool {
	for _, origin := range origins {
		if origin != "" && origin == got {
			return true
		}
	}
	return false
}

type customerJSON struct {
	ExternalID  string `json:"external_id"`
	ContractID  string `json:"contract_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type invoiceJSON struct {
	ExternalID string `json:"external_id"`
	Label      string `json:"label"`
}

type linkJSON struct {
	ID                 string `json:"id"`
	ContactID          string `json:"contact_id"`
	ConversationID     string `json:"conversation_id,omitempty"`
	ExternalCustomerID string `json:"external_customer_id"`
	ExternalContractID string `json:"external_contract_id,omitempty"`
	ExternalProtocol   string `json:"external_protocol,omitempty"`
	Selected           bool   `json:"selected"`
}

type resultJSON struct {
	Driver        string        `json:"driver,omitempty"`
	Simulated     bool          `json:"simulated"`
	Capability    string        `json:"capability,omitempty"`
	Outcome       string        `json:"outcome,omitempty"`
	Reason        string        `json:"reason,omitempty"`
	ConsultedAt   *time.Time    `json:"consulted_at,omitempty"`
	Customer      *customerJSON `json:"customer,omitempty"`
	Invoices      []invoiceJSON `json:"invoices,omitempty"`
	Document      string        `json:"document,omitempty"`
	PixCode       string        `json:"pix_code,omitempty"`
	ExternalRef   string        `json:"external_ref,omitempty"`
	LocalProtocol string        `json:"local_protocol,omitempty"`
	Replayed      bool          `json:"replayed,omitempty"`
	MessageID     string        `json:"message_id,omitempty"`
	AttemptID     string        `json:"attempt_id,omitempty"`
	Links         []linkJSON    `json:"links,omitempty"`
}

func viewJSON(out Result) resultJSON {
	body := resultJSON{
		Driver: out.View.Driver, Simulated: out.View.Simulated, Capability: out.View.Capability,
		Outcome: out.View.Outcome, Reason: out.View.Reason, ExternalRef: out.View.ExternalRef,
		PixCode: out.View.PixCode, LocalProtocol: out.LocalProtocol, Replayed: out.Replayed,
	}
	if !out.View.ConsultedAt.IsZero() {
		at := out.View.ConsultedAt
		body.ConsultedAt = &at
	}
	if len(out.View.Document) > 0 {
		body.Document = string(out.View.Document)
	}
	if out.View.Customer != nil {
		body.Customer = &customerJSON{
			ExternalID: out.View.Customer.ExternalID, ContractID: out.View.Customer.ContractID, DisplayName: out.View.Customer.DisplayName,
		}
	}
	for _, item := range out.View.Invoices {
		body.Invoices = append(body.Invoices, invoiceJSON{ExternalID: item.ExternalID, Label: item.Label})
	}
	for _, link := range out.Links {
		body.Links = append(body.Links, linkJSON{
			ID: link.ID.String(), ContactID: link.ContactID.String(),
			ConversationID: uuidText(link.ConversationID), ExternalCustomerID: link.ExternalCustomerID,
			ExternalContractID: link.ExternalContractID, ExternalProtocol: link.ExternalProtocol, Selected: link.Selected,
		})
	}
	body.MessageID = uuidText(out.MessageID)
	body.AttemptID = uuidText(out.AttemptID)
	return body
}

func uuidText(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
