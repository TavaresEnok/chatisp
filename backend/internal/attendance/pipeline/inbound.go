package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/lifecycle"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/privacyguard"
)

type inboundHandler struct {
	dbStore    *db.Store
	attendance service.Service
	repo       store.Repo
	automation *Automation
	jobs       *jobs.Client
	keys       *crypto.Ring
}

// NewInboundHandler constructs the River InboundHandler for processing tenant webhook items.
func NewInboundHandler(database *db.Store, attendance service.Service, automation ...*Automation) jobs.InboundHandler {
	var composed *Automation
	if len(automation) > 0 {
		composed = automation[0]
	}
	return &inboundHandler{
		automation: composed,
		dbStore:    database,
		attendance: attendance,
		repo:       store.Repo{},
	}
}

// ApplyInbound implements jobs.InboundHandler.
// It opens/reuses conversation, creates contact/identity, deduplicates messages,
// and reconciles status receipts.
func (h *inboundHandler) ApplyInbound(ctx context.Context, tenantID, itemID uuid.UUID) error {
	if h == nil || h.dbStore == nil {
		return ErrUnavailable
	}
	if tenantID == uuid.Nil || itemID == uuid.Nil {
		return ErrInvalid
	}

	workerID := uuid.New()
	expiresAt := time.Now().Add(30 * time.Second)

	// Ensure PG tenant slot limit is compatible with execution capacity (4 workers total -> max 2 per tenant).
	_ = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_job_slots (tenant_id, queue_name, max_slots)
			VALUES ($1, 'inbound', 2)
			ON CONFLICT (tenant_id, queue_name) DO UPDATE
			SET max_slots = LEAST(tenant_job_slots.max_slots, 2)
		`, tenantID)
		return err
	})

	slot, err := h.attendance.ReserveWorker(ctx, tenantID, workerID, "inbound", expiresAt)
	if errors.Is(err, store.ErrCapacity) {
		return river.JobSnooze(2 * time.Second)
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = h.attendance.ReleaseWorker(context.Background(), tenantID, slot.ID, workerID, "inbound")
	}()

	var (
		kind        string
		state       string
		inboxID     uuid.UUID
		connectorID uuid.UUID
		rawPayload  json.RawMessage
	)

	// Step 1: Read the item under tenant transaction and check state.
	err = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT kind, state, inbox_id, connector_id, payload
			FROM webhook_items
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND id = $2
			FOR UPDATE
		`, tenantID, itemID).Scan(&kind, &state, &inboxID, &connectorID, &rawPayload)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if state == "processed" {
		if h.automation != nil {
			return h.automation.ApplyTriage(ctx, tenantID, itemID)
		}
		return nil
	}
	if state == "quarantined" {
		return nil
	}

	if kind == "message" {
		var p MessageItemPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return err
		}
		if p.Content.Type == "reaction" {
			return h.applyReaction(ctx, tenantID, itemID, connectorID, p)
		}

		err = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Privacy deletion holds the same tenant lock while removing identifiers.
			// Reload the item after taking it: the initial read was a different transaction.
			if err := automationTenant(ctx, tx, tenantID); err != nil {
				return err
			}
			var currentState string
			var currentPayload json.RawMessage
			var received time.Time
			if err := tx.QueryRow(ctx, `SELECT state, payload, ingress_received_at FROM webhook_items WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, itemID).Scan(&currentState, &currentPayload, &received); err != nil {
				return err
			}
			if currentState != "pending" {
				return nil
			}
			if err := json.Unmarshal(currentPayload, &p); err != nil {
				return err
			}
			legacy, err := privacyguard.LegacyBlocked(ctx, tx, tenantID, received)
			if err != nil {
				return err
			}
			if legacy {
				_, err = tx.Exec(ctx, `UPDATE webhook_items SET state='quarantined' WHERE tenant_id=$1 AND id=$2`, tenantID, itemID)
				return err
			}
			blocked, err := privacyguard.Blocked(ctx, tx, h.keys, tenantID, connectorID, p.From, received)
			if err != nil {
				return err
			}
			if blocked {
				_, err = tx.Exec(ctx, `UPDATE webhook_items SET state='quarantined',payload='{}'::jsonb WHERE tenant_id=$1 AND id=$2`, tenantID, itemID)
				return err
			}
			var duplicate bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE tenant_id=$1 AND connector_id=$2 AND external_message_id=$3)`, tenantID, connectorID, p.ExternalID).Scan(&duplicate); err != nil {
				return err
			}
			if duplicate {
				_, err = tx.Exec(ctx, `UPDATE webhook_items SET state='processed',payload='{}'::jsonb WHERE tenant_id=$1 AND id=$2`, tenantID, itemID)
				return err
			}

			// Resolve or create contact and contact_channel_identity.
			var contactID, identityID uuid.UUID
			err = tx.QueryRow(ctx, `
				SELECT id, contact_id
				FROM contact_channel_identities
				WHERE tenant_id = $1
				  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
				  AND connector_id = $2
				  AND external_id = $3
			`, tenantID, connectorID, p.From).Scan(&identityID, &contactID)

			if errors.Is(err, pgx.ErrNoRows) {
				contactID = uuid.New()
				identityID = uuid.New()
				phone := formatE164(p.From)
				name := strings.TrimSpace(p.ProfileName)
				if name == "" {
					name = p.From
				}

				if _, err := tx.Exec(ctx, `
					INSERT INTO contacts (id, tenant_id, name, phone)
					VALUES ($1, $2, $3, $4)
				`, contactID, tenantID, name, phone); err != nil {
					return err
				}

				if _, err := tx.Exec(ctx, `
					INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
					VALUES ($1, $2, $3, $4, $5)
					ON CONFLICT (tenant_id, connector_id, external_id) DO NOTHING
				`, identityID, tenantID, contactID, connectorID, p.From); err != nil {
					return err
				}

				// If conflict occurred on concurrent insert, re-read identity.
				_ = tx.QueryRow(ctx, `
					SELECT id, contact_id
					FROM contact_channel_identities
					WHERE tenant_id = $1
					  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
					  AND connector_id = $2
					  AND external_id = $3
				`, tenantID, connectorID, p.From).Scan(&identityID, &contactID)
			} else if err != nil {
				return err
			}

			// Lock the contact until the item is stored or quarantined, serializing
			// against a concurrent block operation.
			var lockedID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM contacts WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenantID, contactID).Scan(&lockedID); err != nil {
				return err
			}
			blocked, err = contactdirectory.IsBlocked(ctx, tx, tenantID, contactID)
			if err != nil {
				return err
			}
			if blocked {
				_, err = tx.Exec(ctx, `UPDATE webhook_items SET state='quarantined' WHERE tenant_id=$1 AND id=$2`, tenantID, itemID)
				return err
			}
			trustedAt := p.OriginAt
			if trustedAt.IsZero() {
				trustedAt = time.Now()
			}
			period := trustedAt.UTC().Format("200601")

			// Existing snoozes are released before AcceptInbound publishes its
			// cursor, preserving conversation -> capacity -> cursor lock order.
			var active uuid.UUID
			e := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND inbox_id=$2 AND contact_identity_id=$3 AND status<>'closed' FOR UPDATE`, tenantID, inboxID, identityID).Scan(&active)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			duplicate = false
			if e == nil {
				if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE tenant_id=$1 AND connector_id=$2 AND external_message_id=$3)`, tenantID, connectorID, p.ExternalID).Scan(&duplicate); e != nil {
					return e
				}
			}
			if e == nil && !duplicate {
				if _, e = lifecycle.OnCustomerMessage(ctx, tx, tenantID, active, trustedAt); e != nil {
					return e
				}
			}
			// No active attendance: a current message within the inbox reopen
			// window reopens the last closed one (same protocol, new cycle)
			// before AcceptInbound looks for an active attendance.
			if errors.Is(e, pgx.ErrNoRows) {
				if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE tenant_id=$1 AND connector_id=$2 AND external_message_id=$3)`, tenantID, connectorID, p.ExternalID).Scan(&duplicate); e != nil {
					return e
				}
				if !duplicate {
					if _, _, e = service.ReopenWithinInboxWindow(ctx, tx, tenantID, inboxID, identityID, trustedAt); e != nil {
						return e
					}
				}
			}
			content := prepareContent(p.Content)
			replyTo, replyText, content, err := resolveReply(ctx, tx, tenantID, connectorID, inboxID, identityID, content)
			if err != nil {
				return err
			}
			stored, err := h.repo.AcceptInbound(ctx, tx, store.InboundInput{
				TenantID:          tenantID,
				InboxID:           inboxID,
				ContactID:         contactID,
				IdentityID:        identityID,
				ConnectorID:       connectorID,
				ExternalMessageID: p.ExternalID,
				Body:              p.Text,
				Period:            period,
				TrustedAt:         trustedAt,
				OpenedAt:          trustedAt,
				ContentKind:       content.Type,
				Content:           mustContent(content),
				ReplyTo:           replyTo,
				ReplyToText:       replyText,
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE message_reactions
				   SET target_message_id = $4, conversation_id = $5, updated_at = now()
				 WHERE tenant_id = $1 AND connector_id = $2 AND target_external_id = $3 AND target_message_id IS NULL
			`, tenantID, connectorID, p.ExternalID, stored.MessageID, stored.Conversation.ID); err != nil {
				return err
			}
			if !stored.Duplicate && needsDownload(content) && h.jobs != nil {
				if _, err := h.jobs.EnqueueMedia(ctx, tx, tenantID, stored.MessageID); err != nil {
					return err
				}
			}

			_, err = tx.Exec(ctx, `
				UPDATE webhook_items
				SET state = 'processed'
				WHERE tenant_id = $1
				  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
				  AND id = $2
			`, tenantID, itemID)
			return err
		})
		if err != nil {
			return err
		}
		if h.automation != nil {
			return h.automation.ApplyTriage(ctx, tenantID, itemID)
		}
		return nil
	}

	if kind == "status" {
		var p StatusItemPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return err
		}

		statusKind := p.Kind
		switch statusKind {
		case "delivered", "read", "failed", "accepted":
		case "sent":
			statusKind = "accepted"
		default:
			statusKind = "unknown"
		}

		trustedAt := p.OriginAt
		if trustedAt.IsZero() {
			trustedAt = time.Now()
		}

		// Reconcile uncertain outbound attempts via official CallbackData correlation.
		// If Meta accepted the send but response was lost, the status receipt carries
		// CallbackData = attemptID.
		if rawCb := strings.TrimSpace(p.CallbackData); rawCb != "" {
			if attemptUUID, parseErr := uuid.Parse(rawCb); parseErr == nil && attemptUUID != uuid.Nil {
				var (
					attemptState       string
					attemptConnectorID uuid.UUID
					attemptInboxID     uuid.UUID
					recipientPhone     string
				)
				err := h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
					return tx.QueryRow(ctx, `
						SELECT o.state, m.connector_id, c.inbox_id, ci.external_id
						FROM outbound_attempts o
						JOIN conversations c
						  ON c.tenant_id = o.tenant_id AND c.id = o.conversation_id
						JOIN messages m
						  ON m.tenant_id = o.tenant_id AND m.id = o.message_id
						JOIN contact_channel_identities ci
						  ON ci.tenant_id = c.tenant_id AND ci.id = c.contact_identity_id
						WHERE o.tenant_id = $1
						  AND o.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
						  AND o.id = $2
					`, tenantID, attemptUUID).Scan(&attemptState, &attemptConnectorID, &attemptInboxID, &recipientPhone)
				})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}

				if err == nil {
					// Validate complete binding: connector and inbox must match the receipt's connector and inbox.
					// Also recipient must match if receipt carries recipient_id.
					if attemptConnectorID != connectorID || attemptInboxID != inboxID {
						// Quarantine or skip correlation because callback points to another connector/inbox.
						// Mark item quarantined with explanation to prevent miscorrelation.
						return h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
							_, qErr := tx.Exec(ctx, `
								UPDATE webhook_items
								SET state = 'quarantined'
								WHERE tenant_id = $1
								  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
								  AND id = $2
							`, tenantID, itemID)
							return qErr
						})
					}

					if p.RecipientID != "" && recipientPhone != "" && strings.TrimPrefix(recipientPhone, "+") != strings.TrimPrefix(p.RecipientID, "+") {
						return h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
							_, qErr := tx.Exec(ctx, `
								UPDATE webhook_items
								SET state = 'quarantined'
								WHERE tenant_id = $1
								  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
								  AND id = $2
							`, tenantID, itemID)
							return qErr
						})
					}

					if attemptState == "uncertain" || attemptState == "sending" {
						targetState := "accepted"
						if statusKind == "failed" {
							targetState = "failed"
						}
						if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
							TenantID:          tenantID,
							AttemptID:         attemptUUID,
							To:                targetState,
							ExternalMessageID: p.ExternalMessageID,
							AcceptedAt:        trustedAt,
							Documented:        true,
							Observation:       true,
						}); recErr != nil {
							return recErr
						}
					}
				}
			}
		}

		_, _, err := h.attendance.ReconcileReceipt(ctx, store.StatusInput{
			TenantID:          tenantID,
			ConnectorID:       connectorID,
			ExternalMessageID: p.ExternalMessageID,
			ExternalEventID:   p.ExternalEventID,
			Kind:              statusKind,
			TrustedAt:         trustedAt,
			BillingCategory:   p.BillingCategory,
			Billable:          p.Billable,
			Detail:            p.Detail,
		})
		if err != nil {
			return err
		}

		return h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				UPDATE webhook_items
				SET state = 'processed'
				WHERE tenant_id = $1
				  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
				  AND id = $2
			`, tenantID, itemID)
			return err
		})
	}

	// For other kinds, mark processed
	return h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE webhook_items
			SET state = 'processed'
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND id = $2
		`, tenantID, itemID)
		return err
	})
}

func formatE164(raw string) *string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)
	if len(digits) >= 8 && len(digits) <= 15 && digits[0] != '0' {
		s := "+" + digits
		return &s
	}
	return nil
}
