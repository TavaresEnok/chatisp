package metacloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/platform/urlguard"
)

var (
	errMediaSimulation = errors.New("download oficial desligado em simulação")
	errMediaFetch      = errors.New("download de mídia falhou")
)

const mediaBodyLimit = 32 << 20

// GraphFetcher downloads Cloud API media through urlguard.
// It is constructed only for official mode. Simulation must not use it.
type GraphFetcher struct {
	version string
	guard   *urlguard.Client
}

// NewGraphFetcher binds an already configured guard. A nil guard fails closed.
func NewGraphFetcher(version string, guard *urlguard.Client) (*GraphFetcher, error) {
	version = strings.TrimSpace(version)
	if version == "" || guard == nil {
		return nil, errVersionMissing
	}
	if _, err := parseVersion(version); err != nil {
		return nil, err
	}
	return &GraphFetcher{version: version, guard: guard}, nil
}

// NewOfficialMediaFetcher builds the guarded Graph client with the system
// resolver and dialer. Callers outside simulation pass the result to the job.
// This function does not dial.
func NewOfficialMediaFetcher(version string) (*GraphFetcher, error) {
	policy, err := urlguard.NewPolicy()
	if err != nil {
		return nil, err
	}
	guard, err := urlguard.NewClient(policy,
		urlguard.WithResolver(urlguard.SystemResolver()),
		urlguard.WithDialer(urlguard.SystemDialer()),
		urlguard.WithMaxBytes(mediaBodyLimit),
		urlguard.WithTimeout(20*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return NewGraphFetcher(version, guard)
}

// OfficialMediaFetcher returns the Graph fetcher only when simulation is off.
func OfficialMediaFetcher(simulation bool, version string) (whatsapp.MediaFetcher, error) {
	if simulation {
		return nil, errMediaSimulation
	}
	return NewOfficialMediaFetcher(version)
}

// Fetch reads the media descriptor and then the file URL. Both requests go
// through the guard. The access token is not included in errors.
func (g *GraphFetcher) Fetch(ctx context.Context, ref whatsapp.MediaRef) (whatsapp.MediaBlob, error) {
	if g == nil || g.guard == nil || strings.TrimSpace(ref.AccessToken) == "" || !numericID(ref.MediaID) {
		return whatsapp.MediaBlob{}, errMediaFetch
	}
	scope := urlguard.Scope{TenantID: ref.TenantID, ConnectionID: ref.ConnectorID}
	metaURL := "https://graph.facebook.com/" + g.version + "/" + ref.MediaID
	meta, err := g.get(ctx, scope, metaURL, ref.AccessToken, 1<<20)
	if err != nil {
		return whatsapp.MediaBlob{}, errMediaFetch
	}
	var doc struct {
		URL  string `json:"url"`
		MIME string `json:"mime_type"`
	}
	if err := json.Unmarshal(meta, &doc); err != nil || strings.TrimSpace(doc.URL) == "" {
		return whatsapp.MediaBlob{}, errMediaFetch
	}
	body, err := g.get(ctx, scope, doc.URL, ref.AccessToken, mediaBodyLimit)
	if err != nil {
		return whatsapp.MediaBlob{}, errMediaFetch
	}
	return whatsapp.MediaBlob{MIME: doc.MIME, Body: body}, nil
}

func (g *GraphFetcher) get(ctx context.Context, scope urlguard.Scope, rawURL, token string, limit int64) ([]byte, error) {
	if ctx == nil {
		return nil, errContextMissing
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errMediaFetch
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.guard.Do(ctx, scope, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errMediaFetch
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, errMediaFetch
	}
	return body, nil
}

func numericID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
