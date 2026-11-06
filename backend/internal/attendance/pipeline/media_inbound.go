package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/media"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/privacyguard"
)

func prepareContent(content metacloud.InboundContent) metacloud.InboundContent {
	if content.Type == "" || (content.Type == "text" && content.ContextID == "") {
		return metacloud.InboundContent{}
	}
	if needsDownload(content) {
		content.MediaStatus = "pending"
	}
	return content
}

func mustContent(content metacloud.InboundContent) json.RawMessage {
	if content.Type == "" {
		return nil
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func needsDownload(content metacloud.InboundContent) bool {
	if content.MediaID == "" {
		return false
	}
	switch content.Type {
	case "image", "document", "audio", "voice", "video", "sticker":
		return true
	default:
		return false
	}
}

func (h *inboundHandler) applyReaction(ctx context.Context, tenantID, itemID, connectorID uuid.UUID, p MessageItemPayload) error {
	return h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := automationTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		var state string
		var payload json.RawMessage
		var received time.Time
		if err := tx.QueryRow(ctx, `SELECT state, payload, ingress_received_at FROM webhook_items WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, itemID).Scan(&state, &payload, &received); err != nil {
			return err
		}
		if state == "processed" || state == "quarantined" {
			return nil
		}
		if err := json.Unmarshal(payload, &p); err != nil {
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
		var (
			targetID     uuid.UUID
			conversation uuid.UUID
			version      int64
			targetArg    any
			convArg      any
		)
		err = tx.QueryRow(ctx, `
			SELECT m.id, m.conversation_id, c.version
			  FROM messages m
			  JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
			 WHERE m.tenant_id = $1 AND m.connector_id = $2 AND m.external_message_id = $3
		`, tenantID, connectorID, p.Content.ReactionTarget).Scan(&targetID, &conversation, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return err
		}
		if targetID != uuid.Nil {
			targetArg = targetID
			convArg = conversation
		}
		origin := p.OriginAt.UTC()
		if p.OriginAt.IsZero() {
			origin = time.Now().UTC()
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO message_reactions (
				tenant_id, id, connector_id, conversation_id, target_message_id,
				external_reaction_id, target_external_id, emoji, actor_external_id, origin_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (tenant_id, connector_id, target_external_id, actor_external_id)
			DO UPDATE SET emoji = EXCLUDED.emoji,
			              external_reaction_id = EXCLUDED.external_reaction_id,
			              origin_at = EXCLUDED.origin_at,
			              updated_at = now(),
			              target_message_id = COALESCE(message_reactions.target_message_id, EXCLUDED.target_message_id),
			              conversation_id = COALESCE(message_reactions.conversation_id, EXCLUDED.conversation_id)
			WHERE message_reactions.origin_at <= EXCLUDED.origin_at
		`, tenantID, uuid.New(), connectorID, convArg, targetArg, p.ExternalID, p.Content.ReactionTarget, p.Content.Emoji, p.From, origin)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 && targetID != uuid.Nil && version >= 1 {
			if err := (store.Repo{}).PublishConversationEvent(ctx, tx, tenantID, conversation, version, "conversation.reaction", map[string]any{
				"conversation_id":      conversation.String(),
				"message_id":           targetID.String(),
				"external_reaction_id": p.ExternalID,
				"emoji":                p.Content.Emoji,
				"target_external_id":   p.Content.ReactionTarget,
				"content_kind":         "reaction",
			}); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE webhook_items SET state='processed' WHERE tenant_id=$1 AND id=$2`, tenantID, itemID)
		return err
	})
}

// resolveReply links a quote only when the target is already in the open
// attendance. The id and the quoted text are returned before the message event.
func resolveReply(ctx context.Context, tx pgx.Tx, tenant, connector, inbox, identity uuid.UUID, content metacloud.InboundContent) (uuid.UUID, string, metacloud.InboundContent, error) {
	if content.ContextID == "" {
		return uuid.Nil, "", content, nil
	}
	var live uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM conversations
		 WHERE tenant_id=$1 AND inbox_id=$2 AND contact_identity_id=$3 AND status<>'closed'
		 ORDER BY created_at DESC
		 LIMIT 1
	`, tenant, inbox, identity).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", content, nil
	}
	if err != nil {
		return uuid.Nil, "", content, err
	}
	var quoted, quotedConv uuid.UUID
	var body string
	err = tx.QueryRow(ctx, `
		SELECT id, conversation_id, body FROM messages
		 WHERE tenant_id=$1 AND connector_id=$2 AND external_message_id=$3
	`, tenant, connector, content.ContextID).Scan(&quoted, &quotedConv, &body)
	if errors.Is(err, pgx.ErrNoRows) || quotedConv != live {
		return uuid.Nil, "", content, nil
	}
	if err != nil {
		return uuid.Nil, "", content, err
	}
	content.ReplyToMessage = quoted.String()
	return quoted, body, content, nil
}

var errMediaBusy = errors.New("download de mídia já reservado")

// errMediaLimit is a definitive budget refusal. The message stays and the
// unpublished object is removed instead of retrying the download.
var errMediaLimit = errors.New("limite de anexos atingido")

type mediaHandler struct {
	db          *db.Store
	storage     attachments.Storage
	fetcher     whatsapp.MediaFetcher
	materials   MaterialResolver
	limits      attachments.ResolveLimits
	repo        store.Repo
	reserveFor  time.Duration
	beforeFetch func()
	afterCreate func() error
}

type mediaClaim struct {
	done         bool
	stale        bool
	intentExists bool
	token        string
	conversation uuid.UUID
	connector    uuid.UUID
	version      int64
	content      metacloud.InboundContent
	key          string
}

// NewMediaHandler stores inbound files after a durable download.
func NewMediaHandler(database *db.Store, storage attachments.Storage, fetcher whatsapp.MediaFetcher, materials MaterialResolver) jobs.MediaHandler {
	if fetcher == nil {
		fetcher = whatsapp.DisabledFetcher{}
	}
	return &mediaHandler{db: database, storage: storage, fetcher: fetcher, materials: materials, repo: store.Repo{}}
}

func (h *mediaHandler) holdDuration() time.Duration {
	if h.reserveFor < 0 {
		return 0
	}
	if h.reserveFor == 0 {
		return 45 * time.Second
	}
	return h.reserveFor
}

func reservationFresh(content metacloud.InboundContent, ttl time.Duration) bool {
	if content.MediaStatus != "reserved" || content.ReserveToken == "" || ttl <= 0 {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, content.ReservedAt)
	if err != nil {
		return false
	}
	return time.Since(at) < ttl
}

func (h *mediaHandler) DownloadInboundMedia(ctx context.Context, tenantID, messageID uuid.UUID) error {
	if h == nil || h.db == nil || tenantID == uuid.Nil || messageID == uuid.Nil {
		return ErrInvalid
	}
	claim, err := h.claimMedia(ctx, tenantID, messageID)
	if errors.Is(err, errMediaBusy) {
		return river.JobSnooze(time.Second)
	}
	if err != nil || claim.done {
		return err
	}
	if h.beforeFetch != nil {
		h.beforeFetch()
	}
	ref := whatsapp.MediaRef{MediaID: claim.content.MediaID, TenantID: tenantID.String(), ConnectorID: claim.connector.String()}
	if h.materials != nil {
		var versionText string
		_ = h.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT version FROM channel_connectors WHERE tenant_id=$1 AND id=$2`, tenantID, claim.connector).Scan(&versionText)
		})
		if n, err := parseVersion(versionText); err == nil {
			material, err := h.materials.ResolveMaterial(ctx, tenantID, claim.connector, n)
			if err == nil {
				ref.AccessToken = material.AccessToken
				ref.PhoneNumberID = material.PhoneNumberID
			}
		}
	}
	blob, fetchErr := h.fetcher.Fetch(ctx, ref)
	if fetchErr != nil {
		// A stale reservation may already have a durable upload intent and an
		// object of unknown version. Keep it retryable until cleanup succeeds.
		if claim.intentExists {
			return ErrUnavailable
		}
		return h.failOwned(ctx, tenantID, messageID, claim, "download_falhou")
	}
	sniffed, sum, code := media.InspectInbound(claim.content.Type, claim.content.MIME, blob.Body)
	if code != "" {
		if claim.intentExists {
			return ErrUnavailable
		}
		claim.content.SniffedMIME = sniffed
		return h.failOwned(ctx, tenantID, messageID, claim, code)
	}
	if h.storage == nil {
		return h.failOwned(ctx, tenantID, messageID, claim, "armazenamento_indisponivel")
	}
	name := safeFileName(claim.content.Filename, claim.content.Type)
	ready, err := h.prepareIntent(ctx, tenantID, messageID, &claim, name, int64(len(blob.Body)))
	if errors.Is(err, errMediaLimit) {
		return h.failOwned(ctx, tenantID, messageID, claim, "limite_excedido")
	}
	if err != nil || !ready {
		return err
	}
	var objectVersion string
	err = h.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := automationTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT (m.content_context->>'reserve_token'=$3 AND a.state='upload' AND a.object_key=$4 AND p.privacy_erased_at IS NULL) FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id JOIN contacts p ON p.tenant_id=c.tenant_id AND p.id=c.contact_id JOIN attachments a ON a.tenant_id=m.tenant_id AND a.conversation_id=m.conversation_id AND a.request_id=m.id WHERE m.tenant_id=$1 AND m.id=$2 FOR UPDATE OF m,a`, tenantID, messageID, claim.token, claim.key).Scan(&valid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if !valid {
			return nil
		}
		storageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if claim.stale {
			if err := h.storage.Delete(storageCtx, claim.key, ""); err != nil {
				return err
			}
		}
		var err error
		objectVersion, err = h.storage.Create(storageCtx, claim.key, bytes.NewReader(blob.Body), int64(len(blob.Body)))
		if err != nil {
			return err
		}
		if objectVersion == "" {
			return ErrUnavailable
		}
		if h.afterCreate != nil {
			if err := h.afterCreate(); err != nil {
				return err
			}
		}
		return h.commitStored(ctx, tx, tenantID, messageID, claim, objectVersion, sniffed, sum, blob.Body)
	})
	if errors.Is(err, errMediaLimit) {
		if h.storage != nil {
			if delErr := h.storage.Delete(ctx, claim.key, objectVersion); delErr != nil {
				return delErr
			}
		}
		return h.failOwned(ctx, tenantID, messageID, claim, "limite_excedido")
	}
	return err
}

// prepareIntent makes an in-flight object visible to privacy deletion and its
// durable cleanup ledger before any write reaches storage.
func (h *mediaHandler) prepareIntent(ctx context.Context, tenant, message uuid.UUID, claim *mediaClaim, name string, size int64) (bool, error) {
	ready := false
	err := h.db.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := automationTenant(ctx, tx, tenant); err != nil {
			return err
		}
		var token string
		err := tx.QueryRow(ctx, `SELECT COALESCE(m.content_context->>'reserve_token','') FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id JOIN contacts p ON p.tenant_id=c.tenant_id AND p.id=c.contact_id WHERE m.tenant_id=$1 AND m.id=$2 AND p.privacy_erased_at IS NULL FOR UPDATE OF m`, tenant, message).Scan(&token)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if token != claim.token {
			return nil
		}
		var key, state string
		var expected int64
		err = tx.QueryRow(ctx, `SELECT object_key,state,expected_bytes FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND request_id=$3`, tenant, claim.conversation, message).Scan(&key, &state, &expected)
		if err == nil {
			if expected != size {
				return ErrUnavailable
			}
			claim.key = key
			ready = state == "upload"
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err = h.reserveInbound(ctx, tx, tenant, size); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO attachment_budgets(tenant_id,namespace) VALUES($1,$2) ON CONFLICT DO NOTHING`, tenant, uuid.New()); err != nil {
			return err
		}
		var namespace uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT namespace FROM attachment_budgets WHERE tenant_id=$1`, tenant).Scan(&namespace); err != nil {
			return err
		}
		claim.key = namespace.String() + "/" + message.String()
		_, err = tx.Exec(ctx, `INSERT INTO attachments(tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,state,expires_at) VALUES($1,$2,$3,$4,'incoming',$5,$6,$7,'upload',$8)`, tenant, uuid.New(), claim.conversation, message, name, size, claim.key, time.Now().Add(24*time.Hour))
		if planLimitViolation(err) {
			return errMediaLimit
		}
		if err == nil {
			ready = true
		}
		return err
	})
	return ready, err
}

func (h *mediaHandler) claimMedia(ctx context.Context, tenantID, messageID uuid.UUID) (mediaClaim, error) {
	claim := mediaClaim{}
	err := h.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := automationTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(ctx, `
			SELECT m.conversation_id, m.connector_id, m.content_context, c.version
			  FROM messages m
			  JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
			 WHERE m.tenant_id = $1 AND m.id = $2
			 FOR UPDATE OF m
		`, tenantID, messageID).Scan(&claim.conversation, &claim.connector, &raw, &claim.version)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &claim.content); err != nil {
				return err
			}
		}
		if claim.content.MediaStatus == "stored" || claim.content.MediaStatus == "failed" || !needsDownload(claim.content) {
			claim.done = true
			return nil
		}
		if reservationFresh(claim.content, h.holdDuration()) {
			return errMediaBusy
		}
		claim.stale = claim.content.MediaStatus == "reserved"
		if claim.stale {
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND request_id=$3 AND state='upload')`, tenantID, claim.conversation, messageID).Scan(&claim.intentExists); err != nil {
				return err
			}
			if h.storage == nil {
				return ErrUnavailable
			}
			storageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := h.storage.Delete(storageCtx, "in/"+tenantID.String()+"/"+messageID.String(), ""); err != nil {
				return err
			}
		}
		claim.token = uuid.NewString()
		claim.content.MediaStatus = "reserved"
		claim.content.MediaError = ""
		claim.content.ReserveToken = claim.token
		claim.content.ReservedAt = time.Now().UTC().Format(time.RFC3339Nano)
		encoded, err := json.Marshal(claim.content)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE messages SET content_context=$3::jsonb WHERE tenant_id=$1 AND id=$2`, tenantID, messageID, encoded)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return mediaClaim{done: true}, nil
	}
	return claim, err
}

func (h *mediaHandler) commitStored(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID, claim mediaClaim, objectVersion, sniffed, sum string, body []byte) error {
	var attachmentID uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE attachments SET state='validated',object_version=$4,media_type=$5,sha256=$6 WHERE tenant_id=$1 AND conversation_id=$2 AND request_id=$3 AND state='upload' RETURNING id`, tenantID, claim.conversation, messageID, objectVersion, sniffed, sum).Scan(&attachmentID)
	if err != nil {
		return err
	}
	content := claim.content
	content.MediaStatus = "stored"
	content.MediaError = ""
	content.ReserveToken = ""
	content.ReservedAt = ""
	content.SniffedMIME = sniffed
	content.AttachmentID = attachmentID.String()
	content.Bytes = int64(len(body))
	if _, err = tx.Exec(ctx, `INSERT INTO media_items (tenant_id,id,conversation_id,message_id,attachment_id,direction,kind,media_type,size_bytes,duration_source,conversion_state) VALUES ($1,$2,$3,$4,$5,'incoming',$6,$7,$8,'absent','original')`, tenantID, uuid.New(), claim.conversation, messageID, attachmentID, mediaKind(content.Type), sniffed, len(body)); err != nil {
		return err
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE messages SET content_context=$3::jsonb WHERE tenant_id=$1 AND id=$2 AND content_context->>'reserve_token'=$4 AND content_context->>'media_status'='reserved'`, tenantID, messageID, encoded, claim.token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrUnavailable
	}
	return h.repo.PublishConversationEvent(ctx, tx, tenantID, claim.conversation, claim.version, "conversation.inbound_media", map[string]any{"conversation_id": claim.conversation.String(), "message_id": messageID.String(), "content_kind": content.Type, "content": json.RawMessage(encoded), "attachment_id": attachmentID.String()})
}

func (h *mediaHandler) failOwned(ctx context.Context, tenantID, messageID uuid.UUID, claim mediaClaim, code string) error {
	content := claim.content
	content.MediaStatus = "failed"
	content.MediaError = code
	content.ReserveToken = ""
	content.ReservedAt = ""
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}
	var applied bool
	err = h.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := automationTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE messages SET content_context=$3::jsonb
			 WHERE tenant_id=$1 AND id=$2
			   AND content_context->>'reserve_token'=$4
			   AND content_context->>'media_status'='reserved'
		`, tenantID, messageID, raw, claim.token)
		if err != nil {
			return err
		}
		applied = tag.RowsAffected() > 0
		if !applied || claim.conversation == uuid.Nil || claim.version < 1 {
			return nil
		}
		return h.repo.PublishConversationEvent(ctx, tx, tenantID, claim.conversation, claim.version, "conversation.inbound_media", map[string]any{
			"conversation_id": claim.conversation.String(),
			"message_id":      messageID.String(),
			"content_kind":    content.Type,
			"content":         json.RawMessage(raw),
			"media_error":     code,
		})
	})
	if err != nil || applied {
		return err
	}
	var status string
	readErr := h.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(content_context->>'media_status','') FROM messages WHERE tenant_id=$1 AND id=$2`, tenantID, messageID).Scan(&status)
	})
	if readErr != nil || status == "stored" || status == "failed" || status == "" {
		return readErr
	}
	return river.JobSnooze(time.Second)
}

// reserveInbound locks the shared attachment budget and counts this file
// before the attachment row exists. Incoming capacity is the plan plus the
// incoming reserve. The same reserved_bytes counter is what outgoing
// registration reads. A nil resolver keeps callers that have no budget wired.
func (h *mediaHandler) reserveInbound(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, size int64) error {
	if h == nil || h.limits == nil {
		return nil
	}
	l, err := h.limits(ctx, tx, tenantID)
	if errors.Is(err, attachments.ErrLimit) || errors.Is(err, attachments.ErrStorage) {
		return errMediaLimit
	}
	if err != nil {
		return err
	}
	if size <= 0 || l.PlanBytes <= 0 || l.IncomingReserveBytes < 0 || l.FileBytes <= 0 || l.FileBytes > attachments.MaxFileBytes || size > l.FileBytes || l.PlanBytes >= 1<<62 || l.IncomingReserveBytes >= 1<<62 {
		return errMediaLimit
	}
	budget := l.PlanBytes + l.IncomingReserveBytes
	if size > budget {
		return errMediaLimit
	}
	if _, err = tx.Exec(ctx, `INSERT INTO attachment_budgets(tenant_id,namespace) VALUES($1,$2) ON CONFLICT DO NOTHING`, tenantID, uuid.New()); err != nil {
		return err
	}
	var used int64
	if err = tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1 FOR UPDATE`, tenantID).Scan(&used); err != nil {
		return err
	}
	if used > budget-size {
		return errMediaLimit
	}
	_, err = tx.Exec(ctx, `UPDATE attachment_budgets SET reserved_bytes=reserved_bytes+$2 WHERE tenant_id=$1`, tenantID, size)
	return err
}

func planLimitViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func parseVersion(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ErrInvalid
	}
	var n int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, ErrInvalid
		}
		n = n*10 + int64(r-'0')
	}
	if n < 1 {
		return 0, ErrInvalid
	}
	return n, nil
}

func mediaKind(kind string) string {
	switch kind {
	case "image", "audio", "voice", "video", "document", "sticker":
		return kind
	default:
		return "document"
	}
}

func safeFileName(name, kind string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		switch kind {
		case "image", "sticker":
			return "imagem"
		case "audio", "voice":
			return "audio"
		case "video":
			return "video"
		default:
			return "documento"
		}
	}
	if len(name) > 180 {
		return name[:180]
	}
	return name
}
