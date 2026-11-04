// Package evolution is the local contract for the conditioned WhatsApp Web
// connector. The product flag stays off. This package does not open a
// network connection, embed Baileys, persist a QR code or claim a release.
package evolution

import (
	"errors"
	"sync"
)

const authorExternal = "dispositivo externo"

var (
	// ErrDisabled means the product flag is off. The call does not leave the process.
	ErrDisabled = errors.New("conector evolution desligado")
	// ErrForbidden means a non-admin asked for the QR.
	ErrForbidden = errors.New("qr restrito ao administrador")
	// ErrNotPersisted means a caller tried to store the QR.
	ErrNotPersisted = errors.New("qr não é persistido")
	// ErrInvalid means the event class is unknown.
	ErrInvalid = errors.New("evento evolution inválido")
)

// Class is one event the specification distinguishes for Web traffic.
type Class string

const (
	ClassInbound     Class = "inbound"
	ClassEcho        Class = "echo"
	ClassExternal    Class = "external_device"
	ClassHistory     Class = "history"
	ClassEdit        Class = "edit"
	ClassRevoke      Class = "revoke"
	ClassUnsupported Class = "unsupported"
)

// Event is one simulated Web event. Text is accepted so tests can prove it
// does not come back in the outcome.
type Event struct {
	Class        Class
	ConnectionID string
	ExternalID   string
	Text         string
}

// Outcome is the local decision. Automation stays false for echo and history.
type Outcome struct {
	Accept      bool
	Automation  bool
	Author      string
	Duplicate   bool
	ReviewHuman bool
}

// Enabled reports the product flag. It is false until D2-04 names a release,
// a license and an explicit acceptance. There is no setter.
func Enabled() bool { return false }

// Classify decides what a simulated event may do. It does not send or persist.
func Classify(ev Event) (Outcome, error) {
	if ev.ConnectionID == "" {
		return Outcome{}, ErrInvalid
	}
	switch ev.Class {
	case ClassInbound:
		return Outcome{Accept: true, Automation: true}, nil
	case ClassEcho:
		return Outcome{Accept: true, Duplicate: true}, nil
	case ClassExternal:
		return Outcome{Accept: true, Author: authorExternal, ReviewHuman: true}, nil
	case ClassHistory, ClassEdit, ClassRevoke:
		return Outcome{Accept: true}, nil
	case ClassUnsupported:
		return Outcome{}, nil
	default:
		return Outcome{}, ErrInvalid
	}
}

// Fake is an in-memory double. It never calls a provider.
type Fake struct {
	mu    sync.Mutex
	up    map[string]bool
	sends int
	qr    string
}

// NewFake returns a simulation with no live connection.
func NewFake() *Fake {
	return &Fake{up: map[string]bool{}}
}

// Connect marks one inbox of this connector. It does not enable the product flag.
func (f *Fake) Connect(connectionID string) error {
	if f == nil || connectionID == "" || !Enabled() {
		return ErrDisabled
	}
	return ErrDisabled
}

// Send refuses while the flag is off and does not record the attempt.
func (f *Fake) Send(connectionID string) error {
	if f == nil || connectionID == "" || !Enabled() {
		return ErrDisabled
	}
	return ErrDisabled
}

// Sends is the number of attempts the fake accepted. It stays zero while the flag is off.
func (f *Fake) Sends() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends
}

// Disconnect drops only the named connection. Another connection stays as it was.
func (f *Fake) Disconnect(connectionID string) {
	if f == nil || connectionID == "" {
		return
	}
	f.mu.Lock()
	f.up[connectionID] = false
	f.mu.Unlock()
}

// MarkUp is a test seam for a connection that already existed before a drop.
// It does not enable sending.
func (f *Fake) MarkUp(connectionID string) {
	if f == nil || connectionID == "" {
		return
	}
	f.mu.Lock()
	f.up[connectionID] = true
	f.mu.Unlock()
}

// Up reports the simulated session of one connection.
func (f *Fake) Up(connectionID string) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.up[connectionID]
}

// IssueQR returns nothing while the flag is off. An agent is refused before that.
// The token, when a future flag allows it, must not be stored by this package.
func IssueQR(role string) (string, error) {
	if role != "admin" {
		return "", ErrForbidden
	}
	if !Enabled() {
		return "", ErrDisabled
	}
	return "", ErrDisabled
}

// StoreQR always refuses. The QR is not a row, a log field or a cache entry.
func (f *Fake) StoreQR(token string) error {
	if f == nil {
		return ErrNotPersisted
	}
	f.mu.Lock()
	f.qr = ""
	f.mu.Unlock()
	_ = token
	return ErrNotPersisted
}

// StoredQR reports the persisted token. It is empty.
func (f *Fake) StoredQR() string {
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.qr
}
