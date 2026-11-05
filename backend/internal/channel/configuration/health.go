package configuration

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Quality reading errors returned by a QualityReader. They never carry the
// token or the remote body.
var (
	ErrQualityUnavailable = errors.New("leitura de qualidade indisponível")
	ErrQualityRefused     = errors.New("leitura de qualidade recusada")
	ErrQualityToken       = errors.New("token do canal inválido na leitura de qualidade")
)

// QualityResult is one quality_rating read, kept as Meta returns it.
type QualityResult struct {
	Rating     string
	Simulation bool
}

// QualityReader reads quality_rating for one number. It must not send messages.
type QualityReader interface {
	ReadQuality(ctx context.Context, m Material) (QualityResult, error)
}

// healthWindow is the period of the essential error counters.
const healthWindow = 7 * 24 * time.Hour

// NumberHealth is the admin diagnosis of one Meta number.
// Limit and quality are what Meta reported, never a derived count.
type NumberHealth struct {
	ConnectorID   uuid.UUID
	Name          string
	DisplayNumber string
	Status        string
	Enabled       bool

	Limit   *LimitReport
	Quality *QualityReport

	WindowDays          int
	FailedSends         int
	FailuresByCode      []CodeCount
	UncertainSends      int
	QuarantinedItems    int
	LastInboundAt       string
	OpenCredentialAlert bool
}

// LimitReport is the latest phone_number_quality_update for the number.
type LimitReport struct {
	Event        string
	CurrentLimit string
	OldLimit     string
	MaxDaily     *int64
	ReceivedAt   string
}

// QualityReport is the latest quality_rating read.
type QualityReport struct {
	Rating     string
	Simulation bool
	ReadAt     string
}

// CodeCount is a failed-send count per stored error code.
type CodeCount struct {
	Code  string
	Count int
}

// Health lists every Meta number of the tenant with its last reported limit,
// last quality read and the essential error counters of the last 7 days.
func (s *Service) Health(ctx context.Context, sessionHash []byte) ([]NumberHealth, error) {
	actor, err := s.authorize(ctx, sessionHash)
	if err != nil {
		return nil, err
	}
	var out []NumberHealth
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireAdmin(ctx, actor); err != nil {
			return err
		}
		var err error
		out, err = listHealth(ctx, tx, actor.TenantID)
		return err
	})
	if err != nil {
		return nil, mapWriteErr(err)
	}
	return out, nil
}

func listHealth(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]NumberHealth, error) {
	since := time.Now().Add(-healthWindow)
	rows, err := tx.Query(ctx, `
		SELECT c.id, i.id, p.id, i.name, COALESCE(p.display_number, ''), c.status, c.enabled,
		       EXISTS (
		           SELECT 1 FROM audit_logs AS al
		           WHERE al.tenant_id = c.tenant_id AND al.action = $2
		             AND al.result = 'open' AND al.target_id = c.id
		       )
		FROM channel_connectors AS c
		JOIN inboxes AS i ON i.tenant_id = c.tenant_id AND i.connector_id = c.id
		JOIN meta_phone_numbers AS p ON p.tenant_id = c.tenant_id AND p.connector_id = c.id
		WHERE c.tenant_id = $1
		  AND c.tenant_id = `+tenantSetting+`
		  AND c.connector_type = 'meta_cloud'
		ORDER BY i.name, c.id
	`, tenantID, actionCredentialInvalid)
	if err != nil {
		return nil, err
	}
	type key struct{ inbox, phone uuid.UUID }
	var out []NumberHealth
	var keys []key
	for rows.Next() {
		var h NumberHealth
		var k key
		if err := rows.Scan(&h.ConnectorID, &k.inbox, &k.phone, &h.Name, &h.DisplayNumber, &h.Status, &h.Enabled, &h.OpenCredentialAlert); err != nil {
			rows.Close()
			return nil, err
		}
		h.WindowDays = int(healthWindow / (24 * time.Hour))
		out = append(out, h)
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for n := range out {
		h, k := &out[n], keys[n]
		var limit LimitReport
		var received time.Time
		var old *string
		err := tx.QueryRow(ctx, `
			SELECT event, COALESCE(current_limit, ''), old_limit, max_daily, received_at
			FROM meta_number_health
			WHERE tenant_id = $1 AND phone_id = $2 AND kind = 'limit'
			ORDER BY received_at DESC, id DESC LIMIT 1
		`, tenantID, k.phone).Scan(&limit.Event, &limit.CurrentLimit, &old, &limit.MaxDaily, &received)
		if err == nil {
			if old != nil {
				limit.OldLimit = *old
			}
			limit.ReceivedAt = received.UTC().Format(time.RFC3339)
			h.Limit = &limit
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var quality QualityReport
		err = tx.QueryRow(ctx, `
			SELECT quality_rating, simulation, received_at
			FROM meta_number_health
			WHERE tenant_id = $1 AND phone_id = $2 AND kind = 'quality'
			ORDER BY received_at DESC, id DESC LIMIT 1
		`, tenantID, k.phone).Scan(&quality.Rating, &quality.Simulation, &received)
		if err == nil {
			quality.ReadAt = received.UTC().Format(time.RFC3339)
			h.Quality = &quality
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		codes, err := tx.Query(ctx, `
			SELECT COALESCE(NULLIF(a.error_code, ''), 'sem_codigo'), a.state, count(*)
			FROM outbound_attempts AS a
			JOIN conversations AS cv ON cv.tenant_id = a.tenant_id AND cv.id = a.conversation_id
			WHERE a.tenant_id = $1 AND cv.inbox_id = $2
			  AND a.state IN ('failed', 'uncertain') AND a.created_at >= $3
			GROUP BY 1, 2
			ORDER BY 3 DESC, 1
		`, tenantID, k.inbox, since)
		if err != nil {
			return nil, err
		}
		for codes.Next() {
			var c CodeCount
			var state string
			if err := codes.Scan(&c.Code, &state, &c.Count); err != nil {
				codes.Close()
				return nil, err
			}
			if state == "uncertain" {
				h.UncertainSends += c.Count
				continue
			}
			h.FailedSends += c.Count
			if len(h.FailuresByCode) < 5 {
				h.FailuresByCode = append(h.FailuresByCode, c)
			}
		}
		codes.Close()
		if err := codes.Err(); err != nil {
			return nil, err
		}
		var last *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE state = 'quarantined' AND created_at >= $3),
			       max(created_at) FILTER (WHERE kind = 'message')
			FROM webhook_items
			WHERE tenant_id = $1 AND inbox_id = $2
		`, tenantID, k.inbox, since).Scan(&h.QuarantinedItems, &last); err != nil {
			return nil, err
		}
		if last != nil {
			h.LastInboundAt = last.UTC().Format(time.RFC3339)
		}
	}
	return out, nil
}

// CheckQuality reads quality_rating for one connector and records it.
// The read happens outside any transaction. A Graph 190 degrades the channel
// exactly like a send would; other failures record nothing.
func (s *Service) CheckQuality(ctx context.Context, sessionHash []byte, connectorID uuid.UUID) (QualityReport, error) {
	actor, err := s.authorize(ctx, sessionHash)
	if err != nil {
		return QualityReport{}, err
	}
	if connectorID == uuid.Nil {
		return QualityReport{}, ErrInvalid
	}
	reader := s.quality
	var material Material
	var phoneRow uuid.UUID
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireAdmin(ctx, actor); err != nil {
			return err
		}
		if reader == nil {
			return ErrQualityUnavailable
		}
		var versionText string
		err := tx.QueryRow(ctx, `
			SELECT c.version, p.id
			FROM channel_connectors AS c
			JOIN meta_phone_numbers AS p ON p.tenant_id = c.tenant_id AND p.connector_id = c.id
			WHERE c.tenant_id = $1 AND c.tenant_id = `+tenantSetting+`
			  AND c.id = $2 AND c.connector_type = 'meta_cloud'
		`, actor.TenantID, connectorID).Scan(&versionText, &phoneRow)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			return err
		}
		material, err = s.loadMaterial(ctx, tx, actor.TenantID, connectorID, version)
		return err
	})
	if err != nil {
		return QualityReport{}, mapWriteErr(err)
	}
	if reader == nil {
		return QualityReport{}, ErrQualityUnavailable
	}
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	result, err := reader.ReadQuality(readCtx, material)
	cancel()
	if errors.Is(err, ErrQualityToken) {
		if rerr := s.Server().ReportInvalidCredential(ctx, actor.TenantID, connectorID, material.Version); rerr != nil && !errors.Is(rerr, ErrStaleVersion) {
			return QualityReport{}, rerr
		}
		return QualityReport{}, ErrQualityToken
	}
	if err != nil {
		if errors.Is(err, ErrQualityRefused) {
			return QualityReport{}, ErrQualityRefused
		}
		return QualityReport{}, ErrQualityUnavailable
	}
	var readAt time.Time
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		id := uuid.New()
		return tx.QueryRow(ctx, `
			INSERT INTO meta_number_health (tenant_id, id, phone_id, kind, dedup_key, quality_rating, simulation)
			VALUES ($1, $2, $3, 'quality', $4, $5, $6)
			RETURNING received_at
		`, actor.TenantID, id, phoneRow, "quality:"+id.String(), result.Rating, result.Simulation).Scan(&readAt)
	})
	if err != nil {
		return QualityReport{}, mapWriteErr(err)
	}
	return QualityReport{Rating: result.Rating, Simulation: result.Simulation, ReadAt: readAt.UTC().Format(time.RFC3339)}, nil
}
