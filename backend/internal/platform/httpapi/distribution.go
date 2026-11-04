package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"chatisp/internal/distribution"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/db/dbgen"
	"chatisp/internal/sectors"
)

// ErrArrivalAssign marks a distribution failure after inbound already returned.
// The wrapped error is the Pick failure. A retry still sees the inbound item.
var ErrArrivalAssign = errors.New("distribuição da chegada falhou")

// InboundApplier is the existing inbound handler. Arrive does not replace it.
type InboundApplier interface {
	ApplyInbound(ctx context.Context, tenantID, itemID uuid.UUID) error
}

type distributionGetInput struct {
	Session  string    `cookie:"__Host-session"`
	CSRF     string    `header:"X-CSRF-Token"`
	Origin   string    `header:"Origin"`
	SectorID uuid.UUID `path:"sector_id"`
	InboxID  uuid.UUID `query:"inbox_id"`
}

type distributionPutInput struct {
	Session  string    `cookie:"__Host-session"`
	CSRF     string    `header:"X-CSRF-Token"`
	Origin   string    `header:"Origin"`
	SectorID uuid.UUID `path:"sector_id"`
	Body     struct {
		ExpectedVersion int64 `json:"expected_version"`
		Enabled         bool  `json:"enabled"`
	}
}

type distributionItemBody struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Priority       string    `json:"priority"`
	QueuedAt       time.Time `json:"queued_at"`
	Alert          bool      `json:"alert"`
}

type distributionStateBody struct {
	Enabled          bool                   `json:"enabled"`
	Version          int64                  `json:"version"`
	Configured       bool                   `json:"configured"`
	Reason           string                 `json:"reason,omitempty"`
	WaitAlertSeconds int                    `json:"wait_alert_seconds,omitempty"`
	Items            []distributionItemBody `json:"items,omitempty"`
}

type distributionOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         distributionStateBody
}

// registerDistribution publishes the sector flag and the queue view.
// A missing distribution_sector_auto row is off. The library treats that
// absence as on; this process does not, so existing tenants keep manual assignment.
func registerDistribution(api huma.API, deps Dependencies) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{
			OperationID:   id,
			Method:        method,
			Path:          path,
			Summary:       summary,
			Tags:          []string{"distribuicao"},
			DefaultStatus: http.StatusOK,
			MaxBodyBytes:  4096,
			Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
		}
	}
	huma.Register(api, op("distribution-sector-get", http.MethodGet, "/api/panel/sectors/{sector_id}/distribution", "Mostra a fila e a flag de atribuição automática"), func(ctx context.Context, in *distributionGetInput) (*distributionOutput, error) {
		return distributionGet(ctx, deps, in)
	})
	huma.Register(api, op("distribution-sector-put", http.MethodPut, "/api/panel/sectors/{sector_id}/distribution", "Liga ou desliga a atribuição automática do setor"), func(ctx context.Context, in *distributionPutInput) (*distributionOutput, error) {
		return distributionPut(ctx, deps, in)
	})
}

func distributionPrincipal(ctx context.Context, deps Dependencies, session, csrf, origin string, write bool) (auth.Principal, error) {
	if deps.Auth == nil || deps.Store == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, allowedOrigin := range deps.TrustedOrigins {
			if allowedOrigin != "" && origin == allowedOrigin {
				allowed = true
				break
			}
		}
		if !allowed {
			return auth.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	principal, err := deps.Auth.Authenticate(ctx, session, csrf, write)
	if err != nil {
		return auth.Principal{}, authProblem(err)
	}
	if principal.IsSuperadmin || principal.EnrollmentOnly || principal.TenantID == uuid.Nil || principal.MembershipID == uuid.Nil {
		return auth.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return principal, nil
}

func distributionGet(ctx context.Context, deps Dependencies, in *distributionGetInput) (*distributionOutput, error) {
	principal, err := distributionPrincipal(ctx, deps, in.Session, in.CSRF, in.Origin, false)
	if err != nil {
		return nil, err
	}
	if principal.Role != "admin" && principal.Role != "supervisor" {
		return nil, huma.Error403Forbidden("acesso negado")
	}
	if in.SectorID == uuid.Nil || in.InboxID == uuid.Nil {
		return nil, huma.Error422UnprocessableEntity("setor ou caixa ausente")
	}
	now := time.Now().UTC()
	var view distribution.QueueView
	var auto distribution.SectorAuto
	err = deps.Store.WithTenantTx(ctx, principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(principal)); err != nil {
			return err
		}
		var err error
		view, err = distribution.Queue(ctx, tx, principal.TenantID, in.SectorID, in.InboxID, now)
		if err != nil {
			return err
		}
		auto, err = readCompositionAuto(ctx, tx, principal.TenantID, in.SectorID)
		return err
	})
	if err != nil {
		return nil, distributionProblem(err)
	}
	return distributionResponse(auto, view), nil
}

func distributionPut(ctx context.Context, deps Dependencies, in *distributionPutInput) (*distributionOutput, error) {
	principal, err := distributionPrincipal(ctx, deps, in.Session, in.CSRF, in.Origin, true)
	if err != nil {
		return nil, err
	}
	if principal.Role != "admin" {
		return nil, huma.Error403Forbidden("acesso negado")
	}
	if in.SectorID == uuid.Nil || in.Body.ExpectedVersion < 0 {
		return nil, huma.Error422UnprocessableEntity("comando inválido")
	}
	var auto distribution.SectorAuto
	err = deps.Store.WithTenantTx(ctx, principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := deps.Store.LockTenant(ctx); err != nil {
			return err
		}
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(principal)); err != nil {
			return err
		}
		if principal.Role != "admin" {
			return sectors.ErrForbidden
		}
		var err error
		auto, err = distribution.PutSectorAuto(ctx, tx, principal.TenantID, in.SectorID, in.Body.ExpectedVersion, in.Body.Enabled)
		return err
	})
	if err != nil {
		return nil, distributionProblem(err)
	}
	return &distributionOutput{CacheControl: "no-store", Body: distributionStateBody{Enabled: auto.Enabled, Version: auto.Version, Configured: auto.Configured}}, nil
}

func distributionResponse(auto distribution.SectorAuto, view distribution.QueueView) *distributionOutput {
	items := make([]distributionItemBody, 0, len(view.Items))
	for _, item := range view.Items {
		items = append(items, distributionItemBody{
			ConversationID: item.ConversationID,
			Priority:       item.Priority,
			QueuedAt:       item.QueuedAt,
			Alert:          item.Alert,
		})
	}
	return &distributionOutput{
		CacheControl: "no-store",
		Body: distributionStateBody{
			Enabled:          auto.Enabled,
			Version:          auto.Version,
			Configured:       auto.Configured,
			Reason:           view.Reason,
			WaitAlertSeconds: view.Settings.WaitAlertSeconds,
			Items:            items,
		},
	}
}

func distributionProblem(err error) error {
	switch {
	case errors.Is(err, distribution.ErrForbidden), errors.Is(err, sectors.ErrForbidden), errors.Is(err, auth.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, distribution.ErrConflict):
		return huma.Error409Conflict("versão conflitante")
	case errors.Is(err, distribution.ErrInvalid):
		return huma.Error422UnprocessableEntity("comando inválido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}

// readCompositionAuto reports the sector flag. No row means automatic
// assignment is off for this process, regardless of the library default.
func readCompositionAuto(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID) (distribution.SectorAuto, error) {
	row, err := dbgen.New(tx).GetDistributionSectorAuto(ctx, dbgen.GetDistributionSectorAutoParams{
		TenantID: tenant,
		SectorID: sector,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return distribution.SectorAuto{}, nil
	}
	if err != nil {
		return distribution.SectorAuto{}, err
	}
	return distribution.SectorAuto{Enabled: row.Enabled, Version: row.Version, Configured: true}, nil
}

// AssignArrived runs Pick only when the sector flag is explicitly enabled.
// A missing or disabled row leaves the queue untouched.
func AssignArrived(ctx context.Context, store *db.Store, tenant, sector, inbox uuid.UUID, now time.Time) (distribution.Result, error) {
	var result distribution.Result
	if store == nil || tenant == uuid.Nil || sector == uuid.Nil || inbox == uuid.Nil || now.IsZero() {
		return result, distribution.ErrInvalid
	}
	err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = assignArrivedTx(ctx, tx, tenant, sector, inbox, now)
		return err
	})
	return result, err
}

func assignArrivedTx(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (distribution.Result, error) {
	auto, err := readCompositionAuto(ctx, tx, tenant, sector)
	if err != nil {
		return distribution.Result{}, err
	}
	if !auto.Configured || !auto.Enabled {
		return distribution.Result{Status: distribution.StatusQueued, Reason: distribution.ReasonAutoDisabled}, nil
	}
	return distribution.Pick(ctx, tx, tenant, sector, inbox, now)
}

// Arrive runs the existing inbound handler and then one distribution attempt.
// Inbound success on an item that was already processed still reaches
// PickInbound, so a crash between the two steps recovers on the retry.
func Arrive(ctx context.Context, next InboundApplier, store *db.Store, tenant, item uuid.UUID, now time.Time) error {
	if next == nil {
		return errors.New("chegada indisponível")
	}
	if err := next.ApplyInbound(ctx, tenant, item); err != nil {
		return err
	}
	if err := PickInbound(ctx, store, tenant, item, now); err != nil {
		return fmt.Errorf("%w: %w", ErrArrivalAssign, err)
	}
	return nil
}

// PickInbound assigns once for one processed message item.
// The claim and every Pick share one transaction. A replay, including one
// after the conversation was returned, finds the claim and does not take
// another conversation from the head of the queue. A pending, status or
// unknown item writes no claim.
func PickInbound(ctx context.Context, store *db.Store, tenant, item uuid.UUID, now time.Time) error {
	if store == nil || tenant == uuid.Nil || item == uuid.Nil || now.IsZero() {
		return distribution.ErrInvalid
	}
	return store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		// Same shared tenant advisory Pick takes first, before the claim row.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, tenant.String()); err != nil {
			return err
		}
		q := dbgen.New(tx)
		row, err := q.GetWebhookItemKindState(ctx, dbgen.GetWebhookItemKindStateParams{TenantID: tenant, ItemID: item})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Kind != "message" || row.State != "processed" {
			return nil
		}
		claimed, err := q.ClaimDistributionArrival(ctx, dbgen.ClaimDistributionArrivalParams{
			TenantID:  tenant,
			ItemID:    item,
			ClaimedAt: pgtype.Timestamptz{Time: now, Valid: true},
		})
		if err != nil {
			return err
		}
		if claimed == 0 {
			return nil
		}
		rows, err := tx.Query(ctx, `
			SELECT DISTINCT c.sector_id, c.inbox_id
			FROM webhook_items wi
			JOIN messages m
			  ON m.tenant_id = wi.tenant_id
			 AND m.connector_id = wi.connector_id
			 AND m.external_message_id = wi.payload->>'external_id'
			JOIN conversations c
			  ON c.tenant_id = m.tenant_id
			 AND c.id = m.conversation_id
			WHERE wi.tenant_id = $1
			  AND wi.id = $2
			  AND wi.kind = 'message'
			  AND c.assignee_id IS NULL
			  AND c.inbox_id IS NOT NULL
			  AND c.sector_id IS NOT NULL
			  AND c.status IN ('open', 'waiting_customer')
		`, tenant, item)
		if err != nil {
			return err
		}
		defer rows.Close()
		type target struct {
			sector uuid.UUID
			inbox  uuid.UUID
		}
		var targets []target
		for rows.Next() {
			var one target
			if err := rows.Scan(&one.sector, &one.inbox); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, one)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, one := range targets {
			if _, err := assignArrivedTx(ctx, tx, tenant, one.sector, one.inbox, now); err != nil {
				return err
			}
		}
		return nil
	})
}
