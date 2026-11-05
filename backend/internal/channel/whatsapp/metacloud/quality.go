package metacloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Quality reading errors. None of them carries the token or the Graph body.
var (
	ErrQualityTokenInvalid = errors.New("token do canal inválido")
	ErrQualityRejected     = errors.New("leitura de qualidade recusada pela Graph")
	ErrQualityUnavailable  = errors.New("leitura de qualidade indisponível")
	ErrQualityMalformed    = errors.New("resposta de qualidade sem formato esperado")
)

// QualityReading is the phone-number node read with fields=quality_rating.
// The value is kept as Meta returns it. The public page shows GREEN, NA and
// UNKNOWN in examples without a closed list, so nothing is mapped here.
type QualityReading struct {
	Rating     string
	Simulation bool
}

// QualityRating reads GET /<version>/<phone_number_id>?fields=quality_rating.
// It is a read: it never sends a message. Official mode requires the same
// explicit Graph version and primary documentation as sending.
func (d *Driver) QualityRating(ctx context.Context) (QualityReading, error) {
	if d == nil || d.endpoint.client == nil || d.endpoint.token == "" {
		return QualityReading{}, errDriverUnavailable
	}
	if ctx == nil {
		return QualityReading{}, errContextMissing
	}
	target, err := d.endpoint.nodeURL()
	if err != nil {
		return QualityReading{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return QualityReading{}, ErrQualityUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.endpoint.token)
	if d.endpoint.simulation {
		req.Header.Set("User-Agent", userAgentSimulation)
	} else {
		req.Header.Set("User-Agent", userAgentOfficial)
	}
	resp, err := d.endpoint.client.Do(req)
	if err != nil {
		return QualityReading{}, ErrQualityUnavailable
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	var parsed struct {
		Quality string        `json:"quality_rating"`
		Error   *graphFailure `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return QualityReading{}, ErrQualityUnavailable
		}
		return QualityReading{}, ErrQualityMalformed
	}
	if parsed.Error != nil {
		if parsed.Error.Code == 190 {
			return QualityReading{}, ErrQualityTokenInvalid
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || parsed.Error.IsTransient {
			return QualityReading{}, ErrQualityUnavailable
		}
		return QualityReading{}, ErrQualityRejected
	}
	if resp.StatusCode != http.StatusOK {
		return QualityReading{}, ErrQualityUnavailable
	}
	rating := strings.TrimSpace(parsed.Quality)
	if !upperToken(rating) {
		return QualityReading{}, ErrQualityMalformed
	}
	return QualityReading{Rating: rating, Simulation: d.endpoint.simulation}, nil
}

func (e endpoint) nodeURL() (*url.URL, error) {
	if e.base == nil || e.version == "" || e.phoneID == "" {
		return nil, errMode
	}
	u := *e.base
	u.Fragment = ""
	u.Path = strings.TrimRight(e.base.Path, "/") + "/" + e.version + "/" + e.phoneID
	u.RawQuery = url.Values{"fields": {"quality_rating"}}.Encode()
	if e.simulation {
		if !loopback(u.Host) || u.Hostname() == OfficialHost {
			return nil, errSimulationEndpoint
		}
		return &u, nil
	}
	if u.Scheme != "https" || u.Hostname() != OfficialHost {
		return nil, errOfficialEndpoint
	}
	return &u, nil
}
