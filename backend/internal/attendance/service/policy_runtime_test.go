package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/channelpolicy"
)

func TestWindowClosedCancelsQueuedAttempt(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-window", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "resposta", "req-window")
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours' WHERE tenant_id = $1 AND direction = 'inbound'`, w.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reserved, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if !errors.Is(err, ErrPolicyDenied) || !errors.Is(err, ErrForbidden) || reserved.State != "cancelled" {
		t.Fatalf("janela vencida: %v %+v", err, reserved)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='cancelled' AND error_code='window_closed'`, w.tenant, sent.AttemptID) != 1 {
		t.Fatal("tentativa vencida não foi cancelada")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM dispatch_policy_decisions WHERE tenant_id=$1 AND attempt_id=$2 AND phase='dispatch' AND allowed=false AND reason='window_closed'`, w.tenant, sent.AttemptID) != 1 {
		t.Fatal("decisão de janela não ficou durável")
	}
}

func TestHumanSignatureOnceAndTemplateOptOut(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	p.Email = "Ana"
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO attendant_signature_settings (tenant_id, enabled, prefix, version) VALUES ($1, true, '{{nome_atendente}}: ', 1)`, w.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got := mustReceive(t, svc, w, "ext-sign", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "resposta", "req-sign")
	var body string
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND id=$2`, w.tenant, sent.MessageID).Scan(&body)
	}); err != nil {
		t.Fatal(err)
	}
	if body != "Ana: resposta" {
		t.Fatalf("assinatura: %q", body)
	}
	again, err := svc.Send(ctx, p, SendInput{
		InboxID: assumed.InboxID, ConversationID: assumed.ID, ExpectedVersion: assumed.Version + 1,
		ClientRequestID: "req-sign", Body: "resposta", At: at(3),
	})
	if err != nil || !again.Replayed || again.MessageID != sent.MessageID {
		t.Fatalf("replay da assinatura: %v %+v", err, again)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attendant_signature_receipts WHERE tenant_id=$1`, w.tenant) != 1 {
		t.Fatal("assinatura reaplicada")
	}

	policy := channelpolicy.Service{DB: testStore}
	scope := channelpolicy.Scope{InboxID: w.inbox, ContactID: w.contact, Purpose: "cobranca"}
	if _, err := policy.RecordConsent(ctx, sectorPrincipal(p), channelpolicy.ConsentInput{Scope: scope, Source: "painel", Evidence: "aceite do contato"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO wa_templates (tenant_id, waba_id, external_id, name, language, category, state, components, sync_source, synced_at)
			SELECT $1, id, 'tpl-cobranca', 'cobranca', 'pt_BR', 'utility', 'APPROVED', '[]'::jsonb, 'driver', clock_timestamp()
			  FROM meta_wabas WHERE tenant_id=$1`, w.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	current := mustHead(t, svc, p, w.inbox, assumed.ID)
	templateIn := TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(4),
	}
	first, err := svc.SendTemplate(ctx, p, templateIn)
	if err != nil || first.Replayed {
		t.Fatalf("template: %v %+v", err, first)
	}
	replayed, err := svc.SendTemplate(ctx, p, templateIn)
	if err != nil || !replayed.Replayed || replayed.AttemptID != first.AttemptID {
		t.Fatalf("replay do template: %v %+v", err, replayed)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM idempotency_records WHERE tenant_id=$1 AND operation='send_template' AND client_request_id='req-tpl'`, w.tenant) != 1 {
		t.Fatal("template compartilhou a operação de texto")
	}
	otherLanguage := templateIn
	otherLanguage.Language = "en_US"
	if _, err := svc.SendTemplate(ctx, p, otherLanguage); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("idioma diferente não conflitou: %v", err)
	}
	emptyReplay := templateIn
	emptyReplay.Language = ""
	if _, err := svc.SendTemplate(ctx, p, emptyReplay); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("idioma vazio na mesma chave não conflitou: %v", err)
	}
	beforeTemplates := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND content_kind='template'`, w.tenant)
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	if _, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl-empty", TemplateID: "tpl-cobranca", Language: "", Purpose: "cobranca", At: at(4),
	}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("idioma vazio: %v", err)
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	if _, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl-bad", TemplateID: "tpl-cobranca", Language: "pt BR", Purpose: "cobranca", At: at(4),
	}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("idioma inválido: %v", err)
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	if _, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl-divergente", TemplateID: "tpl-cobranca", Language: "en_US", Purpose: "cobranca", At: at(4),
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("idioma divergente: %v", err)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND content_kind='template'`, w.tenant) != beforeTemplates {
		t.Fatal("idioma recusado gravou template")
	}
	otherPurpose := templateIn
	otherPurpose.Purpose = "suporte"
	if _, err := svc.SendTemplate(ctx, p, otherPurpose); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("finalidade diferente não conflitou: %v", err)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND content_kind='template'`, w.tenant) != 1 {
		t.Fatal("conflito de template gravou outra mensagem")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND id=$2 AND content_kind='template'`, w.tenant, first.MessageID) != 1 {
		t.Fatal("template gravado como texto")
	}
	owner := uuid.New()
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	inflight, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl-flight", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: inflight.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(6),
	})
	if err != nil || reserved.State != "sending" {
		t.Fatalf("template em voo: %v %+v", err, reserved)
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	uncertainSend, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl-uncertain", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	uncertainLease, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: uncertainSend.AttemptID, OwnerID: owner,
		LeaseVersion: reserved.LeaseVersion, ExpiresAt: time.Now().Add(time.Minute), At: at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: uncertainSend.AttemptID, OwnerID: owner, LeaseVersion: uncertainLease.LeaseVersion, To: "uncertain",
	}); err != nil {
		t.Fatal(err)
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	reply, err := svc.Send(ctx, p, SendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-customer", Body: "seguimos", At: at(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	var cursorBefore int64
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(seq, 0) FROM realtime_tenant_cursors WHERE tenant_id=$1`, w.tenant).Scan(&cursorBefore)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.RecordConsent(ctx, sectorPrincipal(p), channelpolicy.ConsentInput{Scope: scope, Source: "painel", Evidence: "revogação do contato", Revoke: true}); err != nil {
		t.Fatal(err)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='cancelled' AND error_code='consent_revoked'`, w.tenant, first.AttemptID) != 1 {
		t.Fatal("proativa pendente não foi cancelada")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND id=$2 AND local_state='cancelled'`, w.tenant, first.MessageID) != 1 {
		t.Fatal("mensagem pendente não acompanhou o cancelamento")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM realtime_events WHERE tenant_id=$1 AND event_type='message.attempt' AND aggregate_type='conversation' AND aggregate_id=$2 AND seq>$3 AND payload->>'attempt_id'=$4 AND payload->>'state'='cancelled' AND payload->>'message_id'=$5 AND payload->>'conversation_id'=$6`, w.tenant, assumed.ID, cursorBefore, first.AttemptID.String(), first.MessageID.String(), assumed.ID.String()) != 1 {
		t.Fatal("revogação não publicou message.attempt")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM realtime_tenant_cursors WHERE tenant_id=$1 AND seq>$2`, w.tenant, cursorBefore) != 1 {
		t.Fatal("cursor não avançou com a revogação")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='sending'`, w.tenant, inflight.AttemptID) != 1 {
		t.Fatal("transmissão em voo foi cancelada")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='uncertain'`, w.tenant, uncertainSend.AttemptID) != 1 {
		t.Fatal("resultado incerto foi reclassificado")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='queued'`, w.tenant, reply.AttemptID) != 1 {
		t.Fatal("resposta pedida pelo cliente foi cancelada")
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	if _, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-tpl-after", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(8),
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("nova proativa após revogação: %v", err)
	}
	if strings.Contains(body, "tpl-cobranca") {
		t.Fatal("texto livre virou template")
	}
}

func TestRevokeOverlapsReserveWithoutDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	adminUser, adminMember := seedMember(t, w.tenant, "admin")
	admin := Principal{UserID: adminUser, MembershipID: adminMember, TenantID: w.tenant, Role: "admin"}
	policy := channelpolicy.Service{DB: testStore}
	scope := channelpolicy.Scope{InboxID: w.inbox, ContactID: w.contact, Purpose: "cobranca"}
	if _, err := policy.RecordConsent(ctx, sectorPrincipal(admin), channelpolicy.ConsentInput{Scope: scope, Source: "painel", Evidence: "aceite do contato"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO wa_templates (tenant_id, waba_id, external_id, name, language, category, state, components, sync_source, synced_at)
			SELECT $1, id, 'tpl-cobranca', 'cobranca', 'pt_BR', 'utility', 'APPROVED', '[]'::jsonb, 'driver', clock_timestamp()
			  FROM meta_wabas WHERE tenant_id=$1`, w.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got := mustReceive(t, svc, w, "ext-race", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	current := mustHead(t, svc, p, w.inbox, assumed.ID)
	first, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-race-a", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	second, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-race-b", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	third, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-race-c", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE outbound_attempts SET state='sending' WHERE tenant_id=$1 AND id=$2 AND state='queued'`, w.tenant, third.AttemptID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE outbound_attempts SET state='retrying' WHERE tenant_id=$1 AND id=$2 AND state='sending'`, w.tenant, third.AttemptID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	db, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT id FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, w.tenant, first.AttemptID); err != nil {
		t.Fatal(err)
	}
	var holderPID int
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	type reserved struct {
		out Reservation
		err error
	}
	reserveDone := make(chan reserved, 1)
	go func() {
		out, err := svc.ReserveDispatch(ctx, ReserveInput{
			TenantID: w.tenant, InboxID: w.inbox, AttemptID: first.AttemptID, OwnerID: uuid.New(),
			ExpiresAt: time.Now().Add(time.Minute), At: at(5),
		})
		reserveDone <- reserved{out, err}
	}()
	reservePID := waitBlockedBy(t, ctx, observer, reserveDone, holderPID)
	consentDone := make(chan error, 1)
	go func() {
		_, err := policy.RecordConsent(ctx, sectorPrincipal(admin), channelpolicy.ConsentInput{
			Scope: scope, Source: "painel", Evidence: "revogação do contato", Revoke: true,
		})
		consentDone <- err
	}()
	waitBlockedBy(t, ctx, observer, consentDone, reservePID)
	select {
	case result := <-reserveDone:
		t.Fatalf("reserva terminou durante a barreira: %v %+v", result.err, result.out)
	case err := <-consentDone:
		t.Fatalf("revogação terminou durante a barreira: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var reserveResult reserved
	var consentErr error
	select {
	case reserveResult = <-reserveDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case consentErr = <-consentDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if reserveResult.err != nil || reserveResult.out.State != "sending" {
		t.Fatalf("reserva: %v %+v", reserveResult.err, reserveResult.out)
	}
	if consentErr != nil {
		t.Fatalf("revogação: %v", consentErr)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='sending'`, w.tenant, first.AttemptID) != 1 {
		t.Fatal("tentativa em reserva não permaneceu em envio")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='cancelled' AND error_code='consent_revoked'`, w.tenant, second.AttemptID) != 1 {
		t.Fatal("segunda pendência não foi cancelada")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND id=$2 AND state='failed' AND error_code='consent_revoked'`, w.tenant, third.AttemptID) != 1 {
		t.Fatal("pendência em retry não falhou")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND id=$2 AND local_state='cancelled'`, w.tenant, second.MessageID) != 1 {
		t.Fatal("mensagem da segunda pendência não foi cancelada")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM realtime_events WHERE tenant_id=$1 AND event_type='message.attempt' AND aggregate_id=$2 AND payload->>'attempt_id'=$3 AND payload->>'state'='cancelled'`, w.tenant, assumed.ID, second.AttemptID.String()) != 1 {
		t.Fatal("cancelamento concorrente não publicou o evento")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM realtime_events WHERE tenant_id=$1 AND event_type='message.attempt' AND aggregate_id=$2 AND payload->>'attempt_id'=$3 AND payload->>'state'='failed'`, w.tenant, assumed.ID, third.AttemptID.String()) != 1 {
		t.Fatal("falha concorrente não publicou o evento")
	}
	current = mustHead(t, svc, p, w.inbox, assumed.ID)
	if _, err := svc.SendTemplate(ctx, p, TemplateSendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: current.Version,
		ClientRequestID: "req-race-after", TemplateID: "tpl-cobranca", Language: "pt_BR", Purpose: "cobranca", At: at(6),
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("proativa depois da revogação concorrente: %v", err)
	}
}

func activityDump() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		return err.Error()
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `
		SELECT pid, COALESCE(wait_event_type, ''), COALESCE(wait_event, ''), pg_blocking_pids(pid), left(regexp_replace(query, '\s+', ' ', 'g'), 280)
		  FROM pg_stat_activity
		 WHERE datname = current_database()
		   AND backend_type = 'client backend'
		 ORDER BY pid`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var pid int
		var kind, event, query string
		var blocking []int32
		if err := rows.Scan(&pid, &kind, &event, &blocking, &query); err != nil {
			return err.Error()
		}
		b.WriteString(fmt.Sprintf("pid=%d wait=%s/%s blocking=%v query=%s\n", pid, kind, event, blocking, query))
	}
	if err := rows.Err(); err != nil {
		return err.Error()
	}
	return b.String()
}

func waitBlockedBy[T any](t *testing.T, ctx context.Context, db *pgx.Conn, done <-chan T, blocker int) int {
	t.Helper()
	for {
		var pid int
		err := db.QueryRow(ctx, `
			SELECT pid
			  FROM pg_stat_activity
			 WHERE datname = current_database()
			   AND pid <> $1
			   AND $1 = ANY(pg_blocking_pids(pid))
			 ORDER BY pid
			 LIMIT 1`, blocker).Scan(&pid)
		if err == nil && pid > 0 {
			return pid
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case result := <-done:
			t.Fatalf("operação terminou antes de esperar o backend %d: %+v\n%s", blocker, result, activityDump())
		case <-ctx.Done():
			t.Fatalf("sem espera pelo backend %d: %v\n%s", blocker, ctx.Err(), activityDump())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestLegacyReplayKeepsOriginalBodyAfterSignature(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	p.Email = "Ana"
	got := mustReceive(t, svc, w, "ext-legacy", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	const body = "resposta anterior"
	const requestID = "req-legacy"
	legacyEnqueue(t, ctx, svc, p, w.connector, assumed, body, requestID)
	enableSignature(t, ctx, w.tenant, "{{nome_atendente}}: ")
	again, err := svc.Send(ctx, p, SendInput{
		InboxID: assumed.InboxID, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: requestID, Body: body, At: at(3),
	})
	if err != nil || !again.Replayed {
		t.Fatalf("replay legado: %v %+v", err, again)
	}
	if messageBody(t, ctx, w.tenant, again.MessageID) != body {
		t.Fatalf("corpo reassinado: %q", messageBody(t, ctx, w.tenant, again.MessageID))
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attendant_signature_receipts WHERE tenant_id=$1`, w.tenant) != 0 {
		t.Fatal("replay legado gravou recibo")
	}

	const farewell = "até logo"
	const farewellID = "bye-legacy"
	legacyEnqueue(t, ctx, svc, p, w.connector, assumed, farewell, farewellID)
	closed, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-legacy", Reason: "resolvido", Result: "resolved", Summary: "ok",
		At: at(8), FarewellBody: farewell, FarewellRequestID: farewellID,
	})
	if err != nil || closed.FarewellErr != nil || closed.Farewell == nil || !closed.Farewell.Replayed || closed.Conversation.Status != store.StatusClosed {
		t.Fatalf("despedida legada: %v %+v", err, closed)
	}
	if messageBody(t, ctx, w.tenant, closed.Farewell.MessageID) != farewell {
		t.Fatal("despedida legada foi reassinalada")
	}
	replayed, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-legacy", Reason: "resolvido", Result: "resolved", Summary: "ok",
		At: at(8), FarewellBody: farewell, FarewellRequestID: farewellID,
	})
	if err != nil || replayed.FarewellErr != nil || replayed.Farewell == nil || !replayed.Farewell.Replayed {
		t.Fatalf("replay do encerramento legado: %v %+v", err, replayed)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attendant_signature_receipts WHERE tenant_id=$1`, w.tenant) != 0 {
		t.Fatal("despedida legada gravou recibo")
	}
}

func TestFarewellSignatureOverflowDoesNotAbortClose(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	p.Email = "Ana"
	got := mustReceive(t, svc, w, "ext-farewell-limit", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	enableSignature(t, ctx, w.tenant, "{{nome_atendente}}: ")
	overflow := strings.Repeat("a", 15996)
	closed, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-overflow", Reason: "resolvido", Result: "resolved", Summary: "ok",
		At: at(8), FarewellBody: overflow, FarewellRequestID: "bye-overflow",
	})
	if err != nil || !errors.Is(closed.FarewellErr, store.ErrInvalid) || closed.Conversation.Status != store.StatusClosed {
		t.Fatalf("expansão da assinatura abortou o encerramento: %v %+v", err, closed)
	}
	if closed.Farewell != nil {
		t.Fatal("despedida inválida entrou na resposta")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1`, w.tenant) != 0 {
		t.Fatal("despedida inválida deixou outbox")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attendant_signature_receipts WHERE tenant_id=$1`, w.tenant) != 0 {
		t.Fatal("despedida inválida gravou recibo")
	}
	again, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-overflow", Reason: "resolvido", Result: "resolved", Summary: "ok",
		At: at(8), FarewellBody: overflow, FarewellRequestID: "bye-overflow",
	})
	if err != nil || again.Conversation.Status != store.StatusClosed {
		t.Fatalf("replay do encerramento com despedida inválida: %v %+v", err, again)
	}

	w2 := seedWorld(t)
	p2 := principal(w2)
	opened := mustReceive(t, svc, w2, "ext-farewell-raw", "oi", at(1))
	owned := mustAssume(t, svc, p2, opened.Conversation)
	raw, err := svc.Close(ctx, p2, CloseInput{
		InboxID: w2.inbox, ConversationID: owned.ID, ExpectedVersion: owned.Version,
		ClientRequestID: "close-raw", Reason: "resolvido", Result: "resolved", Summary: "ok",
		At: at(8), FarewellBody: strings.Repeat("b", 16001), FarewellRequestID: "bye-raw",
	})
	if err != nil || !errors.Is(raw.FarewellErr, store.ErrInvalid) || raw.Conversation.Status != store.StatusClosed {
		t.Fatalf("texto acima do limite abortou o encerramento: %v %+v", err, raw)
	}
	if countQuery(t, ctx, w2.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1`, w2.tenant) != 0 {
		t.Fatal("texto acima do limite deixou outbox")
	}
}

func legacyEnqueue(t *testing.T, ctx context.Context, svc Service, p Principal, connector uuid.UUID, conv store.Conversation, body, requestID string) {
	t.Helper()
	payload, err := canonicalSend(conv.ID.String(), connector.String(), body)
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Repo.EnqueueText(ctx, tx, store.OutboundInput{
			TenantID: p.TenantID, ConversationID: conv.ID, ConnectorID: connector, PrincipalID: p.MembershipID,
			Operation: OpSendText, ClientRequestID: requestID, Payload: payload, Body: body, At: at(3),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func enableSignature(t *testing.T, ctx context.Context, tenant uuid.UUID, prefix string) {
	t.Helper()
	err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO attendant_signature_settings (tenant_id, enabled, prefix, version) VALUES ($1, true, $2, 1)`, tenant, prefix)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func messageBody(t *testing.T, ctx context.Context, tenant, message uuid.UUID) string {
	t.Helper()
	var body string
	err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND id=$2`, tenant, message).Scan(&body)
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
