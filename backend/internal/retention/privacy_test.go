package retention

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyExportAndDeleteKeepsProtocol(t *testing.T) {
	s := seedTenant(t)
	svc := Service{DB: testStore, Objects: &memObjects{}}
	ctx := context.Background()
	opened := time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)
	conv := uuid.New()
	message := uuid.New()
	attachment := uuid.New()
	other := uuid.New()
	otherMessage := uuid.New()
	const (
		phone   = "+5511988887777"
		email   = "titular@example.com"
		note    = "observação privada do titular"
		ref     = "erp-ref-sintetica"
		body    = "segredo-do-titular"
		memo    = "nota-privada-do-atendimento"
		cipher  = "CIFRA-NAO-EXPORTAR"
		object  = "obj-privado-nao-exportar"
		otherTx = "outro-cliente-intacto"
		actor   = "wa-ator-privado"
	)
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE contacts SET name='Titular Sintético', phone=$3, email=$4, notes=$5, external_ref=$6
			WHERE tenant_id=$1 AND id=$2
		`, s.tenant, s.contact, phone, email, note, ref); err != nil {
			return err
		}
		raw := make([]byte, 28)
		copy(raw, cipher)
		mac := make([]byte, 32)
		mac[0] = 7
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_documents (tenant_id, contact_id, kind, key_version, ciphertext, document_hmac, version, updated_by_membership_id)
			VALUES ($1,$2,'cpf','v1',$3,$4,1,$5)
		`, s.tenant, s.contact, raw, mac, s.admin.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'closed','human',$4,'P-PRIV','202611',1,d.sector_id,$5,$6
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$7
			ORDER BY i.id LIMIT 1
		`, conv, s.tenant, s.inbox, s.agent.MembershipID, opened, opened.Add(time.Hour), s.contact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_cycles (tenant_id,id,conversation_id,cycle_number,opened_at,closed_at,close_reason,close_result,close_summary,close_author_id,close_membership_id,close_author_kind)
			VALUES ($1,$2,$3,1,$4,$5,'queda','resolved','restabelecido',$6,$7,'human')
		`, s.tenant, uuid.New(), conv, opened, opened.Add(time.Hour), s.agent.UserID, s.agent.MembershipID); err != nil {
			return err
		}
		var connector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, s.tenant, s.inbox).Scan(&connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at)
			VALUES ($1,$2,$3,$4,'inbound','customer','public',$5,'received',$6,$6)
		`, message, s.tenant, conv, connector, body, opened); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_notes (tenant_id,id,conversation_id,author_id,request_id,body)
			VALUES ($1,$2,$3,$4,'priv-1',$5)
		`, s.tenant, uuid.New(), conv, s.agent.MembershipID, memo); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachments (tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,object_version,state,media_type,sha256,expires_at)
			VALUES ($1,$2,$3,$4,'incoming','fachada.png',4,$5,'v1','validated','image/png','abcd',$6)
		`, s.tenant, attachment, conv, uuid.New(), object+"-"+attachment.String(), opened.Add(24*time.Hour)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_items (tenant_id,id,conversation_id,attachment_id,direction,kind,media_type,size_bytes)
			VALUES ($1,$2,$3,$4,'incoming','image','image/png',4)
		`, s.tenant, uuid.New(), conv, attachment); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO message_reactions (tenant_id,id,connector_id,conversation_id,target_message_id,external_reaction_id,target_external_id,actor_external_id)
			VALUES ($1,$2,$3,$4,$5,'reacao-1','alvo-1',$6)
		`, s.tenant, uuid.New(), connector, conv, message, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO erp_links (tenant_id,id,contact_id,conversation_id,external_customer_id,external_contract_id,external_protocol,selected)
			VALUES ($1,$2,$3,$4,'fake-customer-casa','fake-contract-casa','OS-PRIV',true)
		`, s.tenant, uuid.New(), s.contact, conv); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1,$2,'Outro')`, other, s.tenant); err != nil {
			return err
		}
		identity := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1,$2,$3,$4,$5)
		`, identity, s.tenant, other, connector, "wa-outro-"+identity.String()); err != nil {
			return err
		}
		otherConv := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,$4,$5,'open','menu','P-OUTRO','202611',1,d.sector_id,$6,$6
			FROM inbox_sector_defaults d WHERE d.tenant_id=$2 AND d.inbox_id=$3
		`, otherConv, s.tenant, s.inbox, other, identity, opened); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,ingested_at)
			VALUES ($1,$2,$3,$4,'inbound','customer','public',$5,'received',$6)
		`, otherMessage, s.tenant, otherConv, connector, otherTx, opened)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := svc.ExportPrivacy(ctx, s.admin, s.contact)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	text := string(raw)
	if doc.Label != PrivacyExportLabel || !strings.Contains(text, body) || !strings.Contains(text, phone) || !strings.Contains(text, memo) {
		t.Fatalf("exportação incompleta: %s", text)
	}
	if !strings.Contains(text, "P-PRIV") || !strings.Contains(text, `"document_kind":"cpf"`) {
		t.Fatalf("protocolo ou documento: %s", text)
	}
	if strings.Contains(text, cipher) || strings.Contains(text, object) || strings.Contains(text, "histórico completo do ERP") {
		t.Fatalf("vazou cifra, chave ou rótulo errado: %s", text)
	}
	if _, err := svc.ExportPrivacy(ctx, s.agent, s.contact); !errors.Is(err, ErrForbidden) {
		t.Fatalf("atendente: %v", err)
	}

	deleted, err := svc.DeletePrivacy(ctx, s.admin, s.contact)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Erased || deleted.AlreadyErased || deleted.AutomaticPurge || deleted.MessagesRedacted != 1 || deleted.NotesRedacted != 1 || deleted.AttachmentsRemoved != 1 || deleted.ObjectsRemoved != 1 {
		t.Fatalf("resultado: %+v", deleted)
	}
	if len(deleted.Protocols) != 1 || deleted.Protocols[0] != "P-PRIV" || !strings.Contains(deleted.Note, "desligado") {
		t.Fatalf("preservação: %+v", deleted)
	}
	if len(svc.Objects.(*memObjects).keys) != 1 || !strings.Contains(svc.Objects.(*memObjects).keys[0], object) {
		t.Fatalf("objeto: %+v", svc.Objects)
	}

	var (
		name, notes, identity, actorLeft, customer, linkProtocol, otherBody string
		phoneLeft, emailLeft, refLeft                                       *string
		erased                                                              *time.Time
		kind                                                                *string
		cycles, attachments, records, classRecords                          int
		purge                                                               bool
		bodyLeft                                                            string
	)
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT name, phone, email, notes, external_ref, privacy_erased_at FROM contacts WHERE tenant_id=$1 AND id=$2
		`, s.tenant, s.contact).Scan(&name, &phoneLeft, &emailLeft, &notes, &refLeft, &erased); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND id=$2`, s.tenant, message).Scan(&bodyLeft); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND id=$2`, s.tenant, otherMessage).Scan(&otherBody); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT external_id FROM contact_channel_identities WHERE tenant_id=$1 AND contact_id=$2 ORDER BY external_id LIMIT 1`, s.tenant, s.contact).Scan(&identity); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT actor_external_id FROM message_reactions WHERE tenant_id=$1 AND target_message_id=$2`, s.tenant, message).Scan(&actorLeft); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT external_customer_id, COALESCE(external_protocol,'') FROM erp_links WHERE tenant_id=$1 AND contact_id=$2`, s.tenant, s.contact).Scan(&customer, &linkProtocol); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT kind FROM contact_documents WHERE tenant_id=$1 AND contact_id=$2`, s.tenant, s.contact).Scan(&kind); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_cycles cy JOIN conversations c ON c.tenant_id=cy.tenant_id AND c.id=cy.conversation_id WHERE cy.tenant_id=$1 AND c.contact_id=$2`, s.tenant, s.contact).Scan(&cycles); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments a JOIN conversations c ON c.tenant_id=a.tenant_id AND c.id=a.conversation_id WHERE a.tenant_id=$1 AND c.contact_id=$2`, s.tenant, s.contact).Scan(&attachments); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM deletion_records WHERE tenant_id=$1 AND subject='attachment' AND subject_id=$2`, s.tenant, attachment).Scan(&records); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM retention_deletion_records WHERE tenant_id=$1 AND target_id=$2`, s.tenant, s.contact).Scan(&classRecords); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT automatic_purge FROM tenant_retention_profiles WHERE tenant_id=$1`, s.tenant).Scan(&purge)
		if errors.Is(err, pgx.ErrNoRows) {
			purge = false
			return nil
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if name != TombstoneName || phoneLeft != nil || emailLeft != nil || notes != "" || refLeft != nil || erased == nil {
		t.Fatalf("perfil: %s %v %v %q %v %v", name, phoneLeft, emailLeft, notes, refLeft, erased)
	}
	if bodyLeft != "" || otherBody != otherTx || !strings.HasPrefix(identity, "excluido-") || actorLeft != "" {
		t.Fatalf("conteúdo: %q %q %s %q", bodyLeft, otherBody, identity, actorLeft)
	}
	if customer != "excluido" || linkProtocol != "OS-PRIV" || kind != nil || cycles != 1 || attachments != 0 || records != 1 || classRecords != 2 || purge {
		t.Fatalf("trilha kind=%v ciclos=%d anexos=%d records=%d classes=%d purge=%v cliente=%s protocolo=%s", kind, cycles, attachments, records, classRecords, purge, customer, linkProtocol)
	}
	again, err := svc.DeletePrivacy(ctx, s.admin, s.contact)
	if err != nil || !again.AlreadyErased || again.Erased || again.AttachmentsRemoved != 0 || len(again.Protocols) != 1 {
		t.Fatalf("repetição: %v %+v", err, again)
	}
	if _, err := svc.RunPurge(ctx, s.admin); !errors.Is(err, ErrPurgeDisabled) {
		t.Fatalf("expurgo: %v", err)
	}
	after, err := svc.ExportPrivacy(ctx, s.admin, s.contact)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(after)
	text = string(raw)
	if strings.Contains(text, body) || strings.Contains(text, phone) || strings.Contains(text, cipher) || strings.Contains(text, memo) || !strings.Contains(text, "P-PRIV") {
		t.Fatalf("exportação depois da exclusão: %s", text)
	}
}

func TestPrivacyDeleteRollsBackWhenObjectRemains(t *testing.T) {
	s := seedTenant(t)
	svc := Service{DB: testStore, Objects: &memObjects{fail: true}}
	ctx := context.Background()
	opened := time.Date(2026, 11, 4, 16, 0, 0, 0, time.UTC)
	conv := uuid.New()
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at)
			SELECT $1,$2,$3,c.id,i.id,'open','menu','P-ROLLBACK','202611',1,d.sector_id,$4,$4
			FROM contacts c
			JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id
			JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3
			WHERE c.tenant_id=$2 AND c.id=$5
			ORDER BY i.id LIMIT 1
		`, conv, s.tenant, s.inbox, opened, s.contact); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO attachments (tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,object_version,state,expires_at)
			VALUES ($1,$2,$3,$4,'incoming','a.png',1,$5,'v1','validated',$6)
		`, s.tenant, uuid.New(), conv, uuid.New(), "obj-falha-"+conv.String(), opened.Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeletePrivacy(ctx, s.admin, s.contact); err == nil {
		t.Fatal("exclusão confirmou com objeto preso")
	}
	var name string
	var left int
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM contacts WHERE tenant_id=$1 AND id=$2`, s.tenant, s.contact).Scan(&name); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1`, s.tenant).Scan(&left)
	}); err != nil {
		t.Fatal(err)
	}
	if name != "Cliente" || left != 1 {
		t.Fatalf("rollback: %s anexos=%d", name, left)
	}
}

type memObjects struct {
	keys []string
	fail bool
}

func (m *memObjects) Delete(_ context.Context, key, version string) error {
	if m.fail {
		return errors.New("objeto indisponível")
	}
	m.keys = append(m.keys, key+"@"+version)
	return nil
}
