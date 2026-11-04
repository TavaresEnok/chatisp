package outwebhook

import (
	"io"
	"net/http"
	"sync"
)

// Receiver is the identified fake subscriber. It applies each event id once.
type Receiver struct {
	secret []byte
	// Failures is how many times the next valid, unseen event returns 500
	// without being stored. A later retry of the same id can still be accepted.
	Failures int

	mu      sync.Mutex
	seen    map[string]struct{}
	applied int
}

// NewReceiver verifies signatures with secret.
func NewReceiver(secret []byte) *Receiver {
	return &Receiver{secret: append([]byte(nil), secret...), seen: map[string]struct{}{}}
}

// Applied is the number of effects the receiver performed.
func (r *Receiver) Applied() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applied
}

// ServeHTTP rejects a bad signature and a repeated event id.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r == nil || req.Method != http.MethodPost {
		http.Error(w, "método", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<16))
	if err != nil {
		http.Error(w, "corpo", http.StatusBadRequest)
		return
	}
	if err = Verify(r.secret, body, req.Header.Get(HeaderSignature)); err != nil {
		http.Error(w, "assinatura", http.StatusUnauthorized)
		return
	}
	id := req.Header.Get(HeaderEventID)
	if id == "" || req.Header.Get(HeaderReplay) != replayValue {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[id]; ok {
		http.Error(w, "replay", http.StatusConflict)
		return
	}
	if r.Failures > 0 {
		r.Failures--
		http.Error(w, "indisponível", http.StatusInternalServerError)
		return
	}
	r.seen[id] = struct{}{}
	r.applied++
	w.WriteHeader(http.StatusOK)
}
