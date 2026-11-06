package retention

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ExportPrivacy returns the attendance dossier of one contact.
// The caller is an administrator. Ciphertext and object keys stay out of the file.
func (s Service) ExportPrivacy(ctx context.Context, p Principal, contact uuid.UUID) (PrivacyExport, error) {
	if s.DB == nil {
		return PrivacyExport{}, ErrUnavailable
	}
	if contact == uuid.Nil {
		return PrivacyExport{}, ErrInvalid
	}
	var out PrivacyExport
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := authorize(ctx, tx, p, true, true); err != nil {
			return err
		}
		doc, err := loadPrivacy(ctx, tx, p, contact)
		if err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, p, "contact.privacy_export", "contact", contact, "success"); err != nil {
			return err
		}
		out = doc
		return nil
	})
	return out, mapDB(err)
}

// DeletePrivacy removes identifiers, message bodies and eligible attachments
// of one contact. Cycles, protocols and close reasons stay. Automatic purge
// is not enabled. A second call does not delete those preserved rows again.
func (s Service) DeletePrivacy(ctx context.Context, p Principal, contact uuid.UUID) (DeletionResult, error) {
	if s.DB == nil {
		return DeletionResult{}, ErrUnavailable
	}
	if contact == uuid.Nil {
		return DeletionResult{}, ErrInvalid
	}
	var out DeletionResult
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.DB.LockTenant(ctx); err != nil {
			return err
		}
		if err := authorize(ctx, tx, p, true, true); err != nil {
			return err
		}
		var erasedAt *time.Time
		err := tx.QueryRow(ctx, `
			SELECT privacy_erased_at FROM contacts WHERE tenant_id = $1 AND id = $2 FOR UPDATE
		`, p.TenantID, contact).Scan(&erasedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		protocols, err := loadProtocols(ctx, tx, p.TenantID, contact)
		if err != nil {
			return err
		}
		cyclesBefore, err := countCycles(ctx, tx, p.TenantID, contact)
		if err != nil {
			return err
		}
		if erasedAt != nil {
			purge, err := purgeStillOff(ctx, tx, p.TenantID)
			if err != nil {
				return err
			}
			if err := insertAudit(ctx, tx, p, "contact.privacy_delete", "contact", contact, "already_erased"); err != nil {
				return err
			}
			out = DeletionResult{
				ContactID: contact, AlreadyErased: true, Protocols: protocols,
				AutomaticPurge: purge, Note: PrivacyDeletionNote,
			}
			return nil
		}
		files, bytes, err := lockAttachments(ctx, tx, p.TenantID, contact)
		if err != nil {
			return err
		}
		for _, file := range files {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_records (id, tenant_id, subject, subject_id, object_key, object_version, object_bytes)
				VALUES ($1, $2, 'attachment', $3, $4, $5, $6)
				ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING
			`, uuid.New(), p.TenantID, file.id, file.key, file.version, file.bytes); err != nil {
				return err
			}
			// Existing legacy or conflicting tombstones cannot silently claim a
			// new association. Keep them unchanged and refuse the erasure.
			var matches bool
			if err := tx.QueryRow(ctx, `SELECT object_key=$3 AND object_version=$4 AND object_bytes=$5 FROM deletion_records WHERE tenant_id=$1 AND subject='attachment' AND subject_id=$2 AND object_key IS NOT NULL`, p.TenantID, file.id, file.key, file.version, file.bytes).Scan(&matches); err != nil || !matches {
				return ErrUnavailable
			}
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM media_items m
			USING conversations c
			WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id
			  AND c.tenant_id = $1 AND c.contact_id = $2
		`, p.TenantID, contact); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			DELETE FROM attachments a
			USING conversations c
			WHERE a.tenant_id = c.tenant_id AND a.conversation_id = c.id
			  AND c.tenant_id = $1 AND c.contact_id = $2
		`, p.TenantID, contact)
		if err != nil {
			return err
		}
		if int(tag.RowsAffected()) != len(files) {
			return ErrUnavailable
		}
		if bytes > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE attachment_budgets
				SET reserved_bytes = GREATEST(0, reserved_bytes - $2)
				WHERE tenant_id = $1
			`, p.TenantID, bytes); err != nil {
				return err
			}
		}
		messages, err := tx.Exec(ctx, `
			UPDATE messages m
			SET body = '', content_context = '{}'::jsonb
			FROM conversations c
			WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id
			  AND c.tenant_id = $1 AND c.contact_id = $2 AND (m.body <> '' OR m.content_context <> '{}'::jsonb)
		`, p.TenantID, contact)
		if err != nil {
			return err
		}
		// Replay keeps event identities and cursor continuity, but never the
		// historical content/citation that the subject asked to erase.
		if _, err := tx.Exec(ctx, `UPDATE realtime_events r
          SET payload=jsonb_build_object('conversation_id',c.id,'privacy_erased',true,'historical',true)
          FROM conversations c WHERE r.tenant_id=c.tenant_id AND r.aggregate_type='conversation'
          AND r.aggregate_id=c.id AND c.tenant_id=$1 AND c.contact_id=$2`, p.TenantID, contact); err != nil {
			return err
		}
		notes, err := tx.Exec(ctx, `
			UPDATE conversation_notes n
			SET body = $3
			FROM conversations c
			WHERE n.tenant_id = c.tenant_id AND n.conversation_id = c.id
			  AND c.tenant_id = $1 AND c.contact_id = $2 AND n.body <> $3
		`, p.TenantID, contact, NoteTombstone)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE message_reactions r
			SET actor_external_id = '', updated_at = now()
			FROM messages m
			JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
			WHERE r.tenant_id = m.tenant_id AND r.target_message_id = m.id
			  AND c.tenant_id = $1 AND c.contact_id = $2 AND r.actor_external_id <> ''
		`, p.TenantID, contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE contact_channel_identities
			SET external_id = 'excluido-' || replace(id::text, '-', '')
			WHERE tenant_id = $1 AND contact_id = $2 AND external_id NOT LIKE 'excluido-%'
		`, p.TenantID, contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE erp_links
			SET external_customer_id = 'excluido', external_contract_id = NULL
			WHERE tenant_id = $1 AND contact_id = $2 AND external_customer_id <> 'excluido'
		`, p.TenantID, contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE erp_actions a
			SET external_ref = NULL
			FROM conversations c
			WHERE a.tenant_id = c.tenant_id AND a.conversation_id = c.id
			  AND c.tenant_id = $1 AND c.contact_id = $2 AND a.external_ref IS NOT NULL
		`, p.TenantID, contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE contact_documents
			SET kind = NULL, key_version = NULL, ciphertext = NULL, document_hmac = NULL,
			    version = version + 1, updated_by_membership_id = $3, updated_at = now()
			WHERE tenant_id = $1 AND contact_id = $2 AND kind IS NOT NULL
		`, p.TenantID, contact, p.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE contacts
			SET name = $3, phone = NULL, email = NULL, notes = '', external_ref = NULL,
			    privacy_erased_at = now(), updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND privacy_erased_at IS NULL
		`, p.TenantID, contact, TombstoneName); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO retention_deletion_records (tenant_id, id, class, target_type, target_id, note)
			VALUES ($1, $2, 'content', 'contact', $3, $4)
		`, p.TenantID, uuid.New(), contact, "identificadores e corpos removidos; ciclos e protocolos preservados"); err != nil {
			return err
		}
		if len(files) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO retention_deletion_records (tenant_id, id, class, target_type, target_id, note)
				VALUES ($1, $2, 'media', 'contact', $3, $4)
			`, p.TenantID, uuid.New(), contact, "anexos do titular removidos da base; backup imutável não é apagado aqui"); err != nil {
				return err
			}
		}
		cyclesAfter, err := countCycles(ctx, tx, p.TenantID, contact)
		if err != nil {
			return err
		}
		if cyclesAfter != cyclesBefore {
			return ErrUnavailable
		}
		purge, err := purgeStillOff(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if purge {
			return ErrPurgeDisabled
		}
		removed := 0
		if s.Objects != nil {
			for _, file := range files {
				if err := s.Objects.Delete(ctx, file.key, file.version); err != nil {
					return err
				}
				removed++
			}
		}
		if err := insertAudit(ctx, tx, p, "contact.privacy_delete", "contact", contact, "success"); err != nil {
			return err
		}
		out = DeletionResult{
			ContactID: contact, Erased: true, Protocols: protocols,
			MessagesRedacted: int(messages.RowsAffected()), NotesRedacted: int(notes.RowsAffected()),
			AttachmentsRemoved: len(files), ObjectsRemoved: removed,
			AutomaticPurge: false, Note: PrivacyDeletionNote,
		}
		return nil
	})
	return out, mapDB(err)
}

func loadPrivacy(ctx context.Context, tx pgx.Tx, p Principal, contact uuid.UUID) (PrivacyExport, error) {
	var doc PrivacyExport
	var erased *time.Time
	err := tx.QueryRow(ctx, `
		SELECT name, phone, email, notes, blocked, external_ref, privacy_erased_at
		FROM contacts WHERE tenant_id = $1 AND id = $2
	`, p.TenantID, contact).Scan(
		&doc.Profile.Name, &doc.Profile.Phone, &doc.Profile.Email, &doc.Profile.Notes,
		&doc.Profile.Blocked, &doc.Profile.ExternalRef, &erased,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PrivacyExport{}, ErrNotFound
		}
		return PrivacyExport{}, err
	}
	if erased != nil {
		stamp := erased.UTC().Format(time.RFC3339)
		doc.Profile.ErasedAt = &stamp
	}
	var kind *string
	err = tx.QueryRow(ctx, `
		SELECT kind FROM contact_documents WHERE tenant_id = $1 AND contact_id = $2
	`, p.TenantID, contact).Scan(&kind)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PrivacyExport{}, err
	}
	if kind != nil && *kind != "" {
		doc.Profile.DocumentStored = true
		doc.Profile.DocumentKind = *kind
	}
	rows, err := tx.Query(ctx, `
		SELECT external_id FROM contact_channel_identities
		WHERE tenant_id = $1 AND contact_id = $2
		ORDER BY external_id
	`, p.TenantID, contact)
	if err != nil {
		return PrivacyExport{}, err
	}
	defer rows.Close()
	doc.Identities = []PrivacyIdentity{}
	for rows.Next() {
		var item PrivacyIdentity
		if err := rows.Scan(&item.ExternalID); err != nil {
			return PrivacyExport{}, err
		}
		doc.Identities = append(doc.Identities, item)
	}
	if err := rows.Err(); err != nil {
		return PrivacyExport{}, err
	}
	messages, err := tx.Query(ctx, `
		SELECT c.id, COALESCE(c.protocol, ''), m.direction, m.origin, m.visibility, m.body, m.ingested_at
		FROM messages m
		JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
		WHERE m.tenant_id = $1 AND c.contact_id = $2
		ORDER BY m.ingested_at, m.id
	`, p.TenantID, contact)
	if err != nil {
		return PrivacyExport{}, err
	}
	defer messages.Close()
	doc.Messages = []PrivacyMessage{}
	for messages.Next() {
		var item PrivacyMessage
		var at time.Time
		if err := messages.Scan(&item.ConversationID, &item.Protocol, &item.Direction, &item.Origin, &item.Visibility, &item.Body, &at); err != nil {
			return PrivacyExport{}, err
		}
		item.IngestedAt = at.UTC().Format(time.RFC3339)
		doc.Messages = append(doc.Messages, item)
	}
	if err := messages.Err(); err != nil {
		return PrivacyExport{}, err
	}
	notes, err := tx.Query(ctx, `
		SELECT n.conversation_id, n.body
		FROM conversation_notes n
		JOIN conversations c ON c.tenant_id = n.tenant_id AND c.id = n.conversation_id
		WHERE n.tenant_id = $1 AND c.contact_id = $2
		ORDER BY n.created_at, n.id
	`, p.TenantID, contact)
	if err != nil {
		return PrivacyExport{}, err
	}
	defer notes.Close()
	doc.Notes = []PrivacyNote{}
	for notes.Next() {
		var item PrivacyNote
		if err := notes.Scan(&item.ConversationID, &item.Body); err != nil {
			return PrivacyExport{}, err
		}
		doc.Notes = append(doc.Notes, item)
	}
	if err := notes.Err(); err != nil {
		return PrivacyExport{}, err
	}
	media, err := tx.Query(ctx, `
		SELECT a.id, a.name, a.media_type, a.expected_bytes, a.sha256
		FROM attachments a
		JOIN conversations c ON c.tenant_id = a.tenant_id AND c.id = a.conversation_id
		WHERE a.tenant_id = $1 AND c.contact_id = $2
		ORDER BY a.created_at, a.id
	`, p.TenantID, contact)
	if err != nil {
		return PrivacyExport{}, err
	}
	defer media.Close()
	doc.Media = []PrivacyMedia{}
	for media.Next() {
		var item PrivacyMedia
		if err := media.Scan(&item.AttachmentID, &item.Name, &item.MediaType, &item.SizeBytes, &item.SHA256); err != nil {
			return PrivacyExport{}, err
		}
		doc.Media = append(doc.Media, item)
	}
	if err := media.Err(); err != nil {
		return PrivacyExport{}, err
	}
	demands, err := loadDemands(ctx, tx, p, contact, nil)
	if err != nil {
		return PrivacyExport{}, err
	}
	doc.Label = PrivacyExportLabel
	doc.ContactID = contact
	doc.Demands = demands
	return doc, nil
}

type storedObject struct {
	id      uuid.UUID
	key     string
	version string
	bytes   int64
}

func lockAttachments(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID) ([]storedObject, int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.object_key, a.object_version, a.expected_bytes
		FROM attachments a
		JOIN conversations c ON c.tenant_id = a.tenant_id AND c.id = a.conversation_id
		WHERE a.tenant_id = $1 AND c.contact_id = $2
		ORDER BY a.id
		FOR UPDATE OF a
	`, tenant, contact)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []storedObject
	var bytes int64
	for rows.Next() {
		var item storedObject
		if err := rows.Scan(&item.id, &item.key, &item.version, &item.bytes); err != nil {
			return nil, 0, err
		}
		out = append(out, item)
		bytes += item.bytes
	}
	return out, bytes, rows.Err()
}

func loadProtocols(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT protocol FROM conversations
		WHERE tenant_id = $1 AND contact_id = $2 AND protocol IS NOT NULL
		ORDER BY protocol
	`, tenant, contact)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var protocol string
		if err := rows.Scan(&protocol); err != nil {
			return nil, err
		}
		out = append(out, protocol)
	}
	return out, rows.Err()
}

func countCycles(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM conversation_cycles cy
		JOIN conversations c ON c.tenant_id = cy.tenant_id AND c.id = cy.conversation_id
		WHERE cy.tenant_id = $1 AND c.contact_id = $2
	`, tenant, contact).Scan(&n)
	return n, err
}

func purgeStillOff(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `
		SELECT automatic_purge FROM tenant_retention_profiles WHERE tenant_id = $1
	`, tenant).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return on, nil
}
