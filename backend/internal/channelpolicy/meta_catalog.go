package channelpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"chatisp/internal/channel/whatsapp/metacloud"
)

var ErrCatalogUnavailable = errors.New("catálogo Meta indisponível")
var ErrCatalogMalformed = errors.New("catálogo Meta incompleto ou malformado")
var ErrCatalogTokenInvalid = errors.New("credencial do catálogo Meta inválida")

// MetaCatalogConfig requires an explicitly authorized Graph version and a primary
// source. Construction performs no request. The runtime installs this driver only
// when official integration is enabled. HTTPClient permits local contract tests.
type MetaCatalogConfig struct {
	GraphVersion, PrimaryDocURL, AccessToken string
	HTTPClient                               *http.Client
	CredentialInvalid                        func(context.Context) error
}

type MetaCatalog struct {
	version, token    string
	client            *http.Client
	credentialInvalid func(context.Context) error
}

func (MetaCatalog) String() string   { return "catálogo Meta (credencial redigida)" }
func (MetaCatalog) GoString() string { return "catálogo Meta (credencial redigida)" }
func (*MetaCatalog) IsFake() bool    { return false }

func NewMetaCatalog(cfg MetaCatalogConfig) (*MetaCatalog, error) {
	// Reuse the same validation as the sending driver, including host, version,
	// primary documentation, and token rules. The placeholder node is never read.
	if _, err := metacloud.Activate(metacloud.Config{Mode: metacloud.ModeOfficial, GraphVersion: cfg.GraphVersion, PrimaryDocURL: cfg.PrimaryDocURL, AccessToken: cfg.AccessToken, PhoneNumberID: "1"}); err != nil {
		return nil, ErrUnsupported
	}
	var transport http.RoundTripper
	if cfg.HTTPClient != nil {
		transport = cfg.HTTPClient.Transport
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	if t, ok := transport.(*http.Transport); ok {
		cloned := t.Clone()
		cloned.Proxy = nil
		transport = cloned
	}
	timeout := 30 * time.Second
	if cfg.HTTPClient != nil && cfg.HTTPClient.Timeout > 0 && cfg.HTTPClient.Timeout < timeout {
		timeout = cfg.HTTPClient.Timeout
	}
	return &MetaCatalog{version: strings.TrimSpace(cfg.GraphVersion), token: cfg.AccessToken, client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, credentialInvalid: cfg.CredentialInvalid}, nil
}

// Templates reads the complete WABA snapshot. A failure on any page returns no
// snapshot, so SyncTemplates cannot replace a good catalog with partial data.
// Only opaque cursors are reused; paging URLs never receive credentials.
func (d *MetaCatalog) Templates(ctx context.Context, waba string) ([]Template, error) {
	if d == nil || d.client == nil || len(waba) == 0 || len(waba) > 32 || strings.Trim(waba, "0123456789") != "" {
		return nil, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	path := "/" + d.version + "/" + waba + "/message_templates"
	target := url.URL{Scheme: "https", Host: metacloud.OfficialHost, Path: path}
	query := url.Values{"fields": {"id,name,language,category,status,components"}, "limit": {"100"}}
	out := []Template{}
	seen := map[string]bool{}
	totalBytes := 0
	for page := 0; page < 100; page++ {
		target.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, ErrCatalogUnavailable
		}
		req.Header.Set("Authorization", "Bearer "+d.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "chatisp-metacloud")
		resp, err := d.client.Do(req)
		if err != nil {
			return nil, ErrCatalogUnavailable
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		resp.Body.Close()
		totalBytes += len(body)
		if readErr != nil || len(body) > 4<<20 || totalBytes > 16<<20 {
			return nil, ErrCatalogMalformed
		}
		var parsed struct {
			Data []struct {
				ID         string          `json:"id"`
				Name       string          `json:"name"`
				Language   string          `json:"language"`
				Category   string          `json:"category"`
				Status     string          `json:"status"`
				Components json.RawMessage `json:"components"`
			} `json:"data"`
			Paging struct {
				Next    string `json:"next"`
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
			} `json:"paging"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &parsed) != nil {
			return nil, ErrCatalogMalformed
		}
		if parsed.Error != nil && parsed.Error.Code == 190 {
			if d.credentialInvalid != nil {
				_ = d.credentialInvalid(ctx)
			}
			return nil, ErrCatalogTokenInvalid
		}
		if resp.StatusCode != http.StatusOK || parsed.Error != nil {
			return nil, ErrCatalogUnavailable
		}
		if parsed.Data == nil {
			return nil, ErrCatalogMalformed
		}
		for _, t := range parsed.Data {
			var components []json.RawMessage
			if t.ID == "" || t.Name == "" || t.Language == "" || t.Category == "" || t.Status == "" || len(t.Components) > 65536 || json.Unmarshal(t.Components, &components) != nil || components == nil {
				return nil, ErrCatalogMalformed
			}
			out = append(out, Template{ExternalID: t.ID, Name: t.Name, Language: t.Language, Category: t.Category, State: t.Status, Components: t.Components})
			if len(out) > 10000 {
				return nil, ErrCatalogMalformed
			}
		}
		if parsed.Paging.Next == "" {
			return out, nil
		}
		next, err := url.Parse(parsed.Paging.Next)
		cursor := parsed.Paging.Cursors.After
		if err != nil || next.Scheme != "https" || next.Host != metacloud.OfficialHost || next.User != nil || next.Path != path || next.Fragment != "" || cursor == "" || len(cursor) > 4096 || seen[cursor] || next.Query().Get("after") != cursor {
			return nil, ErrCatalogMalformed
		}
		seen[cursor] = true
		query.Set("after", cursor)
	}
	return nil, fmt.Errorf("%w: limite de paginação", ErrCatalogMalformed)
}
