package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
)

type ingressHandler struct {
	pool          *pgxpool.Pool
	jobsClient    *jobs.Client
	logger        *slog.Logger
	repo          store.Repo
	routeResolver RouteResolver
}

// NewIngressHandler constructs the River IngressHandler for expanding raw webhooks.
func NewIngressHandler(pool *pgxpool.Pool, jobsClient *jobs.Client, opts ...any) jobs.IngressHandler {
	var l *slog.Logger
	var resolver RouteResolver
	for _, opt := range opts {
		switch v := opt.(type) {
		case *slog.Logger:
			l = v
		case RouteResolver:
			resolver = v
		}
	}
	return &ingressHandler{
		pool:          pool,
		jobsClient:    jobsClient,
		logger:        l,
		repo:          store.Repo{},
		routeResolver: resolver,
	}
}

// ExpandIngress implements jobs.IngressHandler.
// It reads the raw delivery, parses independent items with metacloud.Parse,
// routes each item to its tenant, stores tenant-segregated webhook_items,
// and atomically enqueues an inbound job for each new item.
func (h *ingressHandler) ExpandIngress(ctx context.Context, deliveryID uuid.UUID) error {
	if h == nil || h.pool == nil || h.jobsClient == nil {
		return ErrUnavailable
	}
	if deliveryID == uuid.Nil {
		return ErrInvalid
	}

	return db.WithIngressTx(ctx, h.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		var (
			appID string
			body  []byte
			state string
		)
		err := tx.QueryRow(ctx, `
			SELECT app_id, body, state
			FROM ingress_deliveries
			WHERE id = $1
			FOR UPDATE
		`, deliveryID).Scan(&appID, &body, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		if state == "routed" || state == "quarantined" {
			return nil
		}

		items, err := metacloud.Parse(appID, body, time.Now())
		if err != nil {
			return err
		}

		routedCount := 0
		quarantineCount := 0

		for _, item := range items {
			if item.Kind == metacloud.ItemQuarantine {
				if item.PhoneNumberID != "" {
					route, qReason, err := h.resolveRoute(ctx, tx, item.AppID, item.WABAID, item.PhoneNumberID)
					if err != nil && qReason == "" {
						return err
					}
					if err == nil && route.TenantID != uuid.Nil {
						quarantineCount++
						// Known phone number belonging to a valid tenant: store inspectable quarantined item in webhook_items.
						eventKey := stableEventKey(item.DedupKey)
						qPayload, _ := json.Marshal(map[string]any{
							"reason":          item.Reason,
							"app_id":          item.AppID,
							"waba_id":         item.WABAID,
							"phone_number_id": item.PhoneNumberID,
							"detail":          string(item.Preserved),
						})
						_, err := tx.Exec(ctx, `
							INSERT INTO webhook_items (
								tenant_id, id, delivery_id, inbox_id, connector_id,
								phone_number_id, external_key, kind, state, payload
							) VALUES (
								$1, $2, $3, $4, $5, $6, $7, 'other', 'quarantined', $8
							)
							ON CONFLICT (delivery_id, external_key) DO NOTHING
						`, route.TenantID, uuid.New(), deliveryID, route.InboxID, route.ConnectorID,
							item.PhoneNumberID, eventKey, qPayload)
						if err != nil {
							return err
						}
						if h.logger != nil {
							h.logger.Warn("alerta operacional: item em quarentena tenant",
								"tenant_id", route.TenantID,
								"delivery_id", deliveryID,
								"reason", item.Reason,
								"phone", item.PhoneNumberID,
							)
						}
						continue
					}
					// Phone not known or mismatched binding
					quarantineCount++
					reason := qReason
					if reason == "" {
						reason = "unknown_phone"
					}
					ref := clipRef(item.PhoneNumberID + ":" + item.Reason)
					if _, err := tx.Exec(ctx, `
						INSERT INTO ingress_quarantine (id, delivery_id, reason, remote_ref)
						VALUES ($1, $2, $3, $4)
					`, uuid.New(), deliveryID, reason, ref); err != nil {
						return err
					}
					if h.logger != nil {
						h.logger.Warn("alerta operacional: quarentena de ingresso",
							"delivery_id", deliveryID,
							"reason", reason,
							"remote_ref", ref,
							"item_reason", item.Reason,
						)
					}
				} else {
					quarantineCount++
					reason := "unknown_phone"
					if item.Reason == "incompatible_binding" {
						reason = "incompatible_binding"
					}
					ref := clipRef(item.Reason + ":" + item.DedupKey)
					if _, err := tx.Exec(ctx, `
						INSERT INTO ingress_quarantine (id, delivery_id, reason, remote_ref)
						VALUES ($1, $2, $3, $4)
					`, uuid.New(), deliveryID, reason, ref); err != nil {
						return err
					}
					if h.logger != nil {
						h.logger.Warn("alerta operacional: quarentena de ingresso sem telefone",
							"delivery_id", deliveryID,
							"reason", reason,
							"remote_ref", ref,
							"item_reason", item.Reason,
						)
					}
				}
				continue
			}

			if item.Kind == metacloud.ItemMessage || item.Kind == metacloud.ItemStatus {
				route, qReason, err := h.resolveRoute(ctx, tx, item.AppID, item.WABAID, item.PhoneNumberID)
				if err != nil && qReason == "" {
					return err
				}
				if err != nil || route.TenantID == uuid.Nil {
					quarantineCount++
					reason := qReason
					if reason == "" {
						reason = "unknown_phone"
					}
					ref := clipRef(item.PhoneNumberID)
					if ref == "" {
						ref = clipRef(item.DedupKey)
					}
					_, err := tx.Exec(ctx, `
						INSERT INTO ingress_quarantine (id, delivery_id, reason, remote_ref)
						VALUES ($1, $2, $3, $4)
					`, uuid.New(), deliveryID, reason, ref)
					if err != nil {
						return err
					}
					if h.logger != nil {
						h.logger.Warn("alerta operacional: quarentena de ingresso número inválido",
							"delivery_id", deliveryID,
							"reason", reason,
							"remote_ref", ref,
						)
					}
					continue
				}

				eventKey := stableEventKey(item.DedupKey)
				var payloadBytes []byte
				if item.Kind == metacloud.ItemMessage {
					p := MessageItemPayload{
						AppID:         item.AppID,
						WABAID:        item.WABAID,
						PhoneNumberID: item.PhoneNumberID,
						From:          item.From,
						Text:          item.Text,
						ExternalID:    item.ExternalID,
						DedupKey:      eventKey,
						OriginAt:      item.OriginAt,
						ProfileName:   item.ProfileName,
						Content:       item.Content,
					}
					payloadBytes, err = json.Marshal(p)
				} else {
					p := StatusItemPayload{
						AppID:             item.AppID,
						WABAID:            item.WABAID,
						PhoneNumberID:     item.PhoneNumberID,
						ExternalMessageID: item.ExternalID,
						ExternalEventID:   eventKey,
						Kind:              item.Status,
						OriginAt:          item.OriginAt,
						RecipientID:       item.RecipientID,
						CallbackData:      item.CallbackData,
						BillingCategory:   item.PricingCategory,
						Detail:            item.Reason,
					}
					if item.Billable != nil {
						if *item.Billable {
							p.Billable = "true"
						} else {
							p.Billable = "false"
						}
					}
					payloadBytes, err = json.Marshal(p)
				}
				if err != nil {
					return err
				}

				res, err := h.repo.PutWebhookItem(ctx, tx, store.WebhookInput{
					TenantID:      route.TenantID,
					DeliveryID:    deliveryID,
					InboxID:       route.InboxID,
					ConnectorID:   route.ConnectorID,
					PhoneNumberID: item.PhoneNumberID,
					ExternalKey:   eventKey,
					Kind:          item.Kind,
					Payload:       payloadBytes,
				})
				if err != nil {
					return err
				}

				routedCount++

				// Replay must not duplicate inbound work.
				if !res.Replayed {
					if _, err := h.jobsClient.EnqueueInbound(ctx, tx, route.TenantID, res.ID); err != nil {
						return err
					}
				}
			}
		}

		finalState := "routed"
		if routedCount == 0 && quarantineCount > 0 {
			finalState = "quarantined"
		}
		return h.repo.MarkDelivery(ctx, tx, deliveryID, finalState)
	})
}

// resolveRoute strictly validates the binding (App, WABA, PhoneNumber) in ingress scope.
// It never falls back to unauthenticated phone lookups.
// Returns (route, quarantineReason, err).
// Domain errors (unknown phone, incompatible binding) set quarantineReason so they can be quarantined.
// Operational errors (resolver unavailable, connection lost, timeout) return an empty quarantineReason and non-nil err,
// causing ExpandIngress to abort and rollback so River can retry later.
func (h *ingressHandler) resolveRoute(ctx context.Context, tx pgx.Tx, appID, wabaID, phoneNumberID string) (store.Route, string, error) {
	if strings.TrimSpace(phoneNumberID) == "" || len(strings.TrimSpace(phoneNumberID)) > 64 {
		return store.Route{}, "unknown_phone", store.ErrNotFound
	}

	if h.routeResolver != nil {
		route, err := h.routeResolver.ResolveRoute(ctx, appID, phoneNumberID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, configuration.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
				return store.Route{}, "unknown_phone", err
			}
			// Operational error: do not quarantine, abort for River retry
			return store.Route{}, "", err
		}
		if route.TenantID == uuid.Nil {
			return store.Route{}, "unknown_phone", store.ErrNotFound
		}

		// R2: Enforce WABA validation across all paths including when RouteResolver is provided.
		if wabaID != "" && tx != nil {
			var (
				regWABAID  string
				wabaStatus string
			)
			err := tx.QueryRow(ctx, `
				SELECT waba.waba_id, waba.status
				FROM meta_phone_numbers AS phone
				JOIN meta_wabas AS waba
				  ON waba.tenant_id = phone.tenant_id AND waba.id = phone.meta_waba_id
				WHERE phone.tenant_id = $1
				  AND phone.phone_number_id = $2
				  AND current_setting('app.access_scope', true) = 'ingress'
			`, route.TenantID, phoneNumberID).Scan(&regWABAID, &wabaStatus)
			if errors.Is(err, pgx.ErrNoRows) {
				return store.Route{}, "incompatible_binding", ErrForbidden
			}
			if err != nil {
				return store.Route{}, "", err
			}
			if wabaStatus != "active" || regWABAID != wabaID {
				return store.Route{}, "incompatible_binding", ErrForbidden
			}
		}

		return route, "", nil
	}

	var (
		r           store.Route
		regWABAID   *string
		regAppID    *string
		phoneStatus string
		wabaStatus  *string
		appStatus   *string
	)

	err := tx.QueryRow(ctx, `
		SELECT phone.tenant_id, phone.inbox_id, phone.connector_id, phone.phone_number_id,
		       waba.waba_id, app.app_id,
		       phone.status, waba.status, app.status
		FROM meta_phone_numbers AS phone
		LEFT JOIN meta_wabas AS waba
		  ON waba.tenant_id = phone.tenant_id AND waba.id = phone.meta_waba_id
		LEFT JOIN meta_apps AS app
		  ON app.tenant_id = waba.tenant_id AND app.id = waba.meta_app_id
		WHERE phone.phone_number_id = $1
		  AND current_setting('app.access_scope', true) = 'ingress'
	`, phoneNumberID).Scan(
		&r.TenantID, &r.InboxID, &r.ConnectorID, &r.PhoneNumberID,
		&regWABAID, &regAppID,
		&phoneStatus, &wabaStatus, &appStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Route{}, "unknown_phone", store.ErrNotFound
	}
	if err != nil {
		return store.Route{}, "", err
	}

	// Active status verification
	if phoneStatus != "active" || wabaStatus == nil || *wabaStatus != "active" || appStatus == nil || *appStatus != "active" {
		return store.Route{}, "incompatible_binding", ErrForbidden
	}

	// App ID match verification
	if regAppID == nil || *regAppID != appID {
		return store.Route{}, "incompatible_binding", ErrForbidden
	}

	// WABA ID match verification (if provided in payload)
	if wabaID != "" && (regWABAID == nil || *regWABAID != wabaID) {
		return store.Route{}, "incompatible_binding", ErrForbidden
	}

	return r, "", nil
}

// stableEventKey keeps event deduplication keys strictly bounded within 256 bytes
// without losing uniqueness or identity.
func stableEventKey(rawKey string) string {
	rawKey = strings.TrimSpace(rawKey)
	if len(rawKey) <= 256 && !strings.HasPrefix(rawKey, "status:{") {
		return rawKey
	}
	prefix := ""
	rest := rawKey
	if idx := strings.Index(rawKey, ":"); idx != -1 && idx < 32 {
		prefix = rawKey[:idx+1]
		rest = rawKey[idx+1:]
	}
	sum := sha256.Sum256([]byte(rest))
	return prefix + hex.EncodeToString(sum[:])
}

func clipRef(val string) string {
	val = strings.TrimSpace(val)
	if len(val) > 256 {
		return val[:256]
	}
	return val
}
