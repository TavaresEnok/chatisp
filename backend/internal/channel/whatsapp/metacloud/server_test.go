package metacloud

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// The local protocol stand-in is compiled only by go test. The library does
// not import net/http/httptest, so that package's flags stay out of binaries
// that import metacloud. This server never dials Meta.

const (
	// ServerAccept returns the documented acceptance object.
	ServerAccept = "accept"
	// ServerTokenInvalid returns Graph error 190. The body echoes nothing
	// the driver is allowed to copy into a result.
	ServerTokenInvalid = "token_invalid"
	// ServerLoseResponse accepts internally and closes without a response.
	ServerLoseResponse = "lose_response"
	// ServerTimeout accepts internally and waits until the client gives up.
	ServerTimeout = "timeout"
	// ServerStatusBefore writes a status payload to the hook, then accepts.
	ServerStatusBefore = "status_before_response"
	// ServerTooMany returns HTTP 429. That status is not a safe retry.
	ServerTooMany = "http_429"
	// ServerUnavailable returns HTTP 500. That status is not a safe retry.
	ServerUnavailable = "http_500"
	// ServerRedirect returns 302 and must not be followed with Authorization.
	ServerRedirect = "redirect"

	// SimulationHeader marks every response of the local stand-in.
	SimulationHeader = "X-ChatISP-Simulation"
	simulationValue  = "meta-cloud"
)

// Accepted is one request the local server took, including when the HTTP
// response was withheld. MessageID is internal evidence, not a client result.
type Accepted struct {
	MessageID       string
	To              string
	Body            string
	CallbackData    string
	Version         string
	PhoneNumberID   string
	ResponseWritten bool
}

// Server is an explicit loopback stand-in for POST /{version}/{phone}/messages.
// It does not dial Meta. Closing it stops the listener.
type Server struct {
	http     *http.Server
	url      string
	token    string
	phoneID  string
	version  string
	mode     atomic.Value
	seq      atomic.Uint64
	statusFn atomic.Value

	mu       sync.Mutex
	accepted []Accepted
	stolen   int
}

// NewServer listens on 127.0.0.1 and expects version, phone id and bearer token.
func NewServer(version, phoneID, token string) (*Server, error) {
	version, err := parseVersion(version)
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errServerUnavailable
	}
	s := &Server{version: version, phoneID: phoneID, token: token, url: "http://" + ln.Addr().String()}
	s.mode.Store(ServerAccept)
	s.http = &http.Server{Handler: http.HandlerFunc(s.serve)}
	go func() { _ = s.http.Serve(ln) }()
	return s, nil
}

// URL is the loopback base URL. It is safe to pass as Config.BaseURL.
func (s *Server) URL() string {
	if s == nil {
		return ""
	}
	return s.url
}

// Close stops the listener.
func (s *Server) Close() {
	if s != nil && s.http != nil {
		_ = s.http.Close()
	}
}

// SetMode selects the next protocol outcome.
func (s *Server) SetMode(mode string) {
	if s == nil {
		return
	}
	s.mode.Store(mode)
}

// SetStatusHook receives a status webhook body before the HTTP acceptance
// response is written. The hook runs on the request goroutine.
func (s *Server) SetStatusHook(fn func([]byte)) {
	if s == nil {
		return
	}
	if fn == nil {
		s.statusFn.Store(statusHook(nil))
		return
	}
	s.statusFn.Store(statusHook(fn))
}

type statusHook func([]byte)

// AcceptedRequests returns a copy of requests the server received.
func (s *Server) AcceptedRequests() []Accepted {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Accepted, len(s.accepted))
	copy(out, s.accepted)
	return out
}

// StolenHits counts requests to the redirect target. A correct client stays at zero.
func (s *Server) StolenHits() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stolen
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(SimulationHeader, simulationValue)
	if r.URL.Path == "/stolen" {
		s.mu.Lock()
		s.stolen++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeGraph(w, http.StatusMethodNotAllowed, 100)
		return
	}
	want := "/" + s.version + "/" + s.phoneID + "/messages"
	if r.URL.Path != want {
		writeGraph(w, http.StatusNotFound, 100)
		return
	}
	mode, _ := s.mode.Load().(string)
	if !bearerMatch(r.Header.Get("Authorization"), s.token) || mode == ServerTokenInvalid {
		writeTokenInvalid(w, s.token)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxRawBytes))
	if err != nil {
		writeGraph(w, http.StatusBadRequest, 100)
		return
	}
	var payload textPayload
	if err := json.Unmarshal(body, &payload); err != nil || payload.Type != "text" || strings.TrimSpace(payload.Text.Body) == "" {
		writeGraph(w, http.StatusBadRequest, 100)
		return
	}
	id := "wamid.sim." + strconv.FormatUint(s.seq.Add(1), 10)
	switch mode {
	case ServerLoseResponse:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
		})
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil && conn != nil {
			_ = conn.Close()
		}
	case ServerTimeout:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
		})
		<-r.Context().Done()
	case ServerTooMany:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
			ResponseWritten: true,
		})
		writeGraph(w, http.StatusTooManyRequests, 130429)
	case ServerUnavailable:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
			ResponseWritten: true,
		})
		w.WriteHeader(http.StatusInternalServerError)
	case ServerRedirect:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
			ResponseWritten: true,
		})
		w.Header().Set("Location", s.URL()+"/stolen")
		w.WriteHeader(http.StatusFound)
	case ServerStatusBefore:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
			ResponseWritten: true,
		})
		s.emitStatus(id, payload)
		writeAccepted(w, payload.To, id)
	default:
		s.record(Accepted{
			MessageID: id, To: payload.To, Body: payload.Text.Body,
			CallbackData: payload.CallbackData, Version: s.version, PhoneNumberID: s.phoneID,
			ResponseWritten: true,
		})
		writeAccepted(w, payload.To, id)
	}
}

func (s *Server) emitStatus(id string, payload textPayload) {
	fn, _ := s.statusFn.Load().(statusHook)
	if fn == nil {
		return
	}
	raw, err := MarshalStatusEnvelope(StatusFixture{
		WABAID: "102290129340398", PhoneNumberID: s.phoneID, DisplayPhone: "15550783881",
		MessageID: id, Status: "delivered", Timestamp: "1750263773",
		RecipientID: payload.To, CallbackData: payload.CallbackData,
	})
	if err != nil {
		return
	}
	fn(raw)
}

func (s *Server) record(item Accepted) {
	s.mu.Lock()
	s.accepted = append(s.accepted, item)
	s.mu.Unlock()
}

func bearerMatch(header, token string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.HasPrefix(header, prefix) {
		return false
	}
	return subtleString(header[len(prefix):], token)
}

func subtleString(a, b string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func writeAccepted(w http.ResponseWriter, to, id string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"messaging_product": "whatsapp",
		"contacts": []any{map[string]string{
			"input": to,
			"wa_id": strings.TrimPrefix(to, "+"),
		}},
		"messages": []any{map[string]string{"id": id}},
	})
}

func writeTokenInvalid(w http.ResponseWriter, token string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message":       "Invalid OAuth access token " + token,
			"type":          "OAuthException",
			"code":          190,
			"error_subcode": 463,
			"fbtrace_id":    "TRACESECRET99",
			"is_transient":  false,
		},
	})
}

func writeGraph(w http.ResponseWriter, status, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "simulated",
			"type":    "OAuthException",
			"code":    code,
		},
	})
}
