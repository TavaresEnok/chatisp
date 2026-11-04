package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Mode selects the next simulated outcome. It is not a live connector.
type Mode string

const (
	// ModeAccept returns documented acceptance. It does not report delivery.
	ModeAccept Mode = "accept"
	// ModeReject returns a definitive rejection.
	ModeReject Mode = "reject"
	// ModeLoseResponse records an internal accept and returns uncertain,
	// without a provider message id.
	ModeLoseResponse Mode = "lose_response"
	// ModeHTTP429 returns uncertain with status 429 and a retry claim that
	// Classify must ignore.
	ModeHTTP429 Mode = "http_429"
	// ModeHTTP500 returns uncertain with status 500.
	ModeHTTP500 Mode = "http_500"
	// ModeNotDispatched returns retrying with proof the call never started.
	ModeNotDispatched Mode = "not_dispatched"
)

// FakeDriverName identifies every result produced by Fake.
const FakeDriverName = "simulation"

// Capture is one request observed by Fake.
// InternalAcceptID is set when the simulation accepted the send, including
// when that id was withheld because the response was lost.
type Capture struct {
	Request           TextRequest
	TemplateID        string
	Language          string
	MediaKind         string
	AttachmentID      string
	ReplyToProviderID string
	Caption           string
	Dispatched        bool
	ResponseLost      bool
	InternalAcceptID  string
	Result            Result
}

// Fake is an in-memory text driver for tests and named simulations.
// It does not open connections. Concurrent SendText calls are serialized
// on the capture log. Same callback data is not deduplicated.
type Fake struct {
	mu       sync.Mutex
	mode     Mode
	pause    bool
	notify   chan struct{}
	waiting  int
	seq      uint64
	captures []Capture
}

// NewFake returns a simulation that accepts text until the mode changes.
func NewFake() *Fake {
	return &Fake{mode: ModeAccept, notify: make(chan struct{})}
}

// Name identifies the driver as a simulation.
func (f *Fake) Name() string {
	return FakeDriverName
}

// Simulation reports that results from this driver are not live traffic.
func (f *Fake) Simulation() bool {
	return true
}

// Capabilities reports text and media. Templates stay an explicit gap.
func (f *Fake) Capabilities() Capabilities {
	return Capabilities{Text: true, Media: true}
}

// SetMode changes how the next dispatched attempt is classified.
func (f *Fake) SetMode(mode Mode) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

// Block holds later SendText calls before dispatch until Unblock or cancel.
func (f *Fake) Block() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.ensureLocked()
	f.pause = true
	f.mu.Unlock()
}

// Unblock lets paused calls proceed.
func (f *Fake) Unblock() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.ensureLocked()
	if f.pause {
		f.pause = false
		f.wakeLocked()
	}
	f.mu.Unlock()
}

// Waiting is the number of calls paused before dispatch.
func (f *Fake) Waiting() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waiting
}

// Captures returns a copy of the requests observed so far.
func (f *Fake) Captures() []Capture {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Capture, len(f.captures))
	copy(out, f.captures)
	return out
}

// SendText validates, optionally waits, and classifies a simulated outcome.
// Invalid requests are refused before dispatch and are not captured.
// The error text never includes the message body or the callback data.
func (f *Fake) SendText(ctx context.Context, req TextRequest) (Result, error) {
	if f == nil {
		return Result{}, errors.New("driver de simulação indisponível")
	}
	if ctx == nil {
		return Result{}, errors.New("contexto ausente")
	}
	if err := Validate(req); err != nil {
		return Result{}, err
	}
	if req.ConnectorType != ConnectorMetaCloud {
		return Result{}, errors.New("conector indisponível")
	}
	for {
		f.mu.Lock()
		f.ensureLocked()
		if f.pause {
			notify := f.notify
			f.waiting++
			f.mu.Unlock()
			select {
			case <-ctx.Done():
				f.mu.Lock()
				f.waiting--
				f.mu.Unlock()
				return cancelledBeforeDispatch(req.AttemptID), nil
			case <-notify:
				f.mu.Lock()
				f.waiting--
				f.mu.Unlock()
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			f.mu.Unlock()
			return cancelledBeforeDispatch(req.AttemptID), nil
		}
		result := f.dispatchLocked(req)
		f.mu.Unlock()
		return result, nil
	}
}

// SendTemplate classifies a simulated template outcome. It does not open a
// connection and does not call Graph. The capture keeps the template id apart
// from a freeform body. Capabilities.Templates remains false.
func (f *Fake) SendTemplate(ctx context.Context, req TemplateRequest) (Result, error) {
	if strings.TrimSpace(req.Language) != "" && !validToken(req.Language, 40) {
		return Result{}, errors.New("idioma do template inválido")
	}
	text := TextRequest{
		AttemptID: req.AttemptID, ConnectionID: req.ConnectionID, ConnectorType: req.ConnectorType,
		RecipientID: req.RecipientID, CallbackData: req.CallbackData, Text: req.TemplateID,
	}
	if err := Validate(text); err != nil {
		return Result{}, err
	}
	if !validToken(req.TemplateID, 256) {
		return Result{}, errors.New("template inválido")
	}
	result, err := f.SendText(ctx, text)
	if err != nil || f == nil {
		return result, err
	}
	f.mu.Lock()
	if n := len(f.captures); n > 0 {
		last := &f.captures[n-1]
		if last.Request.AttemptID == req.AttemptID && last.TemplateID == "" {
			last.TemplateID = req.TemplateID
			last.Language = req.Language
		}
	}
	f.mu.Unlock()
	return result, nil
}

// SendMedia classifies a simulated media outcome. It does not open a
// connection and does not call Graph. The capture keeps the media class,
// the internal attachment id and the cited remote id apart from the caption.
func (f *Fake) SendMedia(ctx context.Context, req MediaRequest) (Result, error) {
	if err := ValidateMedia(req); err != nil {
		return Result{}, err
	}
	text := req.Caption
	if strings.TrimSpace(text) == "" {
		text = req.AttachmentID
	}
	result, err := f.SendText(ctx, TextRequest{
		AttemptID: req.AttemptID, ConnectionID: req.ConnectionID, ConnectorType: req.ConnectorType,
		RecipientID: req.RecipientID, CallbackData: req.CallbackData, Text: text,
	})
	if err != nil || f == nil {
		return result, err
	}
	f.mu.Lock()
	if n := len(f.captures); n > 0 {
		last := &f.captures[n-1]
		if last.Request.AttemptID == req.AttemptID && last.MediaKind == "" {
			last.MediaKind = string(req.Kind)
			last.AttachmentID = req.AttachmentID
			last.ReplyToProviderID = req.ReplyToProviderID
			last.Caption = req.Caption
		}
	}
	f.mu.Unlock()
	return result, nil
}

func (f *Fake) dispatchLocked(req TextRequest) Result {
	f.seq++
	seq := f.seq
	mode := f.mode
	acceptID := fmt.Sprintf("sim:%s:%d", req.AttemptID, seq)
	var obs Observation
	dispatched := true
	lost := false
	internalID := ""
	switch mode {
	case ModeAccept:
		internalID = acceptID
		obs = Observation{
			AcceptedMessageID: acceptID,
			Detail:            "aceito pelo conector de simulação",
		}
	case ModeReject:
		obs = Observation{
			RejectionCode: "simulated_rejection",
			Detail:        "rejeição definitiva do conector de simulação",
		}
	case ModeLoseResponse:
		lost = true
		internalID = acceptID
		obs = Observation{
			Detail: "resposta perdida depois da aceitação simulada",
		}
	case ModeHTTP429:
		obs = Observation{
			HTTPStatus:       429,
			ClaimedSafeRetry: true,
			Detail:           "resultado incerto; HTTP 429 não autoriza repetição",
		}
	case ModeHTTP500:
		obs = Observation{
			HTTPStatus:       500,
			ClaimedSafeRetry: true,
			Detail:           "resultado incerto; HTTP 500 não autoriza repetição",
		}
	case ModeNotDispatched:
		dispatched = false
		obs = Observation{
			NotDispatched: true,
			Detail:        "despacho não iniciado na simulação",
		}
	default:
		obs = Observation{Detail: "modo de simulação desconhecido; resultado incerto"}
	}
	result := Classify(obs)
	result.AttemptID = req.AttemptID
	result.Simulation = true
	f.captures = append(f.captures, Capture{
		Request:          req,
		Dispatched:       dispatched,
		ResponseLost:     lost,
		InternalAcceptID: internalID,
		Result:           result,
	})
	return result
}

func cancelledBeforeDispatch(id AttemptID) Result {
	result := Classify(Observation{
		NotDispatched:        true,
		CancelBeforeDispatch: true,
		Detail:               "envio cancelado antes do despacho na simulação",
	})
	result.AttemptID = id
	result.Simulation = true
	return result
}

func (f *Fake) ensureLocked() {
	if f.notify == nil {
		f.notify = make(chan struct{})
	}
}

func (f *Fake) wakeLocked() {
	close(f.notify)
	f.notify = make(chan struct{})
}

var _ TextDriver = (*Fake)(nil)
var _ TemplateSender = (*Fake)(nil)
var _ MediaSender = (*Fake)(nil)
