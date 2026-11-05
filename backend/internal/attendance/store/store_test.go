package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
)

func TestSchemaForcesTenantRLSAndCompositeFK(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)

	var bypass bool
	if err := admin.QueryRow(ctx, `SELECT rolbypassrls FROM pg_roles WHERE rolname = 'chatisp_app'`).Scan(&bypass); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("chatisp_app tem BYPASSRLS")
	}
	var owned int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_class c
		JOIN pg_roles r ON r.oid = c.relowner
		WHERE r.rolname = 'chatisp_app' AND c.relkind = 'r'
	`).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if owned != 0 {
		t.Fatalf("tabelas da aplicação: %d", owned)
	}
	for _, table := range []string{"messages", "conversations", "outbound_attempts", "credential_secrets", "webhook_items"} {
		var owner string
		var enabled, forced bool
		if err := admin.QueryRow(ctx, `
			SELECT r.rolname, c.relrowsecurity, c.relforcerowsecurity
			FROM pg_class c
			JOIN pg_roles r ON r.oid = c.relowner
			WHERE c.relname = $1 AND c.relkind = 'r'
		`, table).Scan(&owner, &enabled, &forced); err != nil {
			t.Fatal(table, err)
		}
		if owner != "chatisp_owner" || !enabled || !forced {
			t.Fatalf("%s owner=%s rls=%v force=%v", table, owner, enabled, forced)
		}
	}
	var deliveryRLS bool
	if err := admin.QueryRow(ctx, `SELECT relrowsecurity FROM pg_class WHERE relname = 'ingress_deliveries'`).Scan(&deliveryRLS); err != nil {
		t.Fatal(err)
	}
	if deliveryRLS {
		t.Fatal("ingresso bruto não deve ter RLS de tenant")
	}

	left := seedWorld(t)
	right := seedWorld(t)
	var opened OpenResult
	now := time.Now().UTC()
	if err := testStore.WithTenantTx(ctx, left.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		opened, err = (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: left.tenant, InboxID: left.inbox, ContactID: left.contact, IdentityID: left.identity,
			Period: "202610", OpenedAt: now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	err = testStore.WithTenantTx(ctx, right.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (tenant_id, id, conversation_id, connector_id, direction, origin, visibility)
			VALUES ($1, $2, $3, $4, 'inbound', 'customer', 'public')
		`, right.tenant, uuid.New(), opened.Conversation.ID, right.connector)
		return err
	})
	if pgCode(err) != "23503" {
		t.Fatalf("fk cruzada: %v", err)
	}
	err = testStore.WithTenantTx(ctx, right.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (tenant_id, id, conversation_id, connector_id, direction, origin, visibility)
			VALUES ($1, $2, $3, $4, 'inbound', 'customer', 'public')
		`, left.tenant, uuid.New(), opened.Conversation.ID, left.connector)
		return err
	})
	if pgCode(err) != "42501" {
		t.Fatalf("rls de escrita: %v", err)
	}

	var visible int
	if err := testStore.WithTenantTx(ctx, right.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id = $1`, opened.Conversation.ID).Scan(&visible)
	}); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("mensagens visíveis: %d", visible)
	}

	tx, err := testPools.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := (Repo{}).Open(ctx, tx, OpenInput{TenantID: left.tenant, InboxID: left.inbox, ContactID: left.contact, IdentityID: left.identity, Period: "202610", OpenedAt: now}); !errors.Is(err, ErrTenantContextRequired) {
		t.Fatalf("sem tenant: %v", err)
	}
	var bare int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversations`).Scan(&bare); err != nil {
		t.Fatal(err)
	}
	if bare != 0 {
		t.Fatalf("leitura sem tenant: %d", bare)
	}
}

func TestPlaceholderInsertStillWorks(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	id := uuid.New()
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		return testStore.InsertConversation(ctx, id, "fila antiga")
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var cycle int32
		var inbox *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT status, cycle_number, inbox_id FROM conversations WHERE id = $1
		`, id).Scan(&status, &cycle, &inbox); err != nil {
			return err
		}
		if status != StatusOpen || cycle != 0 || inbox != nil {
			t.Fatalf("placeholder status=%s cycle=%d inbox=%v", status, cycle, inbox)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		rows, err := testStore.ListConversationSummaries(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row == "fila antiga" {
				return nil
			}
		}
		t.Fatalf("resumo ausente: %v", rows)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOpenKeepsOneProtocol(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	in := OpenInput{
		TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
		Period: "202610", OpenedAt: time.Now().UTC(),
	}
	start := make(chan struct{})
	type got struct {
		result OpenResult
		err    error
	}
	out := make(chan got, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			ready.Done()
			<-start
			var result OpenResult
			err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				result, err = (Repo{}).Open(ctx, tx, in)
				return err
			})
			out <- got{result, err}
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-out, <-out
	if first.err != nil || second.err != nil {
		t.Fatalf("abertura: %v %v", first.err, second.err)
	}
	if first.result.Conversation.ID != second.result.Conversation.ID || first.result.Conversation.Protocol != second.result.Conversation.Protocol {
		t.Fatalf("protocolos %s e %s", first.result.Conversation.Protocol, second.result.Conversation.Protocol)
	}
	if first.result.Created == second.result.Created {
		t.Fatal("as duas aberturas gravaram ou nenhuma gravou")
	}
	if first.result.Conversation.Protocol != "202610-000001" {
		t.Fatalf("protocolo: %s", first.result.Conversation.Protocol)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET protocol = '202610-999999' WHERE id = $1`, first.result.Conversation.ID)
		return err
	}); pgCode(err) != "23514" {
		t.Fatalf("protocolo mutável: %v", err)
	}
}

func TestIdempotentSendAndUncertainAttempt(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var conversationID uuid.UUID
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now,
		})
		conversationID = opened.Conversation.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"text":"ola"}`)
	in := OutboundInput{
		TenantID: w.tenant, ConversationID: conversationID, ConnectorID: w.connector,
		PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "req-1",
		Payload: payload, Body: "ola", At: now,
	}
	var first OutboundResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		first, err = (Repo{}).EnqueueText(ctx, tx, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var replay OutboundResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		replay, err = (Repo{}).EnqueueText(ctx, tx, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.MessageID != first.MessageID || replay.AttemptID != first.AttemptID {
		t.Fatalf("replay: %+v", replay)
	}
	in.Payload = []byte(`{"text":"outra"}`)
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).EnqueueText(ctx, tx, in)
		return err
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflito: %v", err)
	}

	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		state, err := (Repo{}).TransitionAttempt(ctx, tx, AttemptTransition{TenantID: w.tenant, AttemptID: first.AttemptID, To: "sending"})
		if err != nil {
			return err
		}
		if state != "sending" {
			t.Fatalf("estado: %s", state)
		}
		_, err = (Repo{}).TransitionAttempt(ctx, tx, AttemptTransition{TenantID: w.tenant, AttemptID: first.AttemptID, To: "uncertain"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).TransitionAttempt(ctx, tx, AttemptTransition{TenantID: w.tenant, AttemptID: first.AttemptID, To: "sending"})
		return err
	})
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("reenvio: %v", err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		state, err := (Repo{}).OutboundState(ctx, tx, w.tenant, first.AttemptID)
		if err != nil {
			return err
		}
		if state != "uncertain" {
			t.Fatalf("permaneceu: %s", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAssumeClosePreservesCycle(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	trusted := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	var opened InboundResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		opened, err = (Repo{}).AcceptInbound(ctx, tx, InboundInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			ConnectorID: w.connector, ExternalMessageID: "wamid.1", Body: "oi",
			Period: "202610", TrustedAt: trusted, OpenedAt: trusted,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			ready.Done()
			<-start
			errs <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, err := (Repo{}).Assume(ctx, tx, AssumeInput{
					TenantID: w.tenant, ConversationID: opened.Conversation.ID, MembershipID: w.membership,
					ExpectedVersion: opened.Conversation.Version, At: trusted.Add(time.Minute),
				})
				return err
			})
		}()
	}
	ready.Wait()
	close(start)
	a, b := <-errs, <-errs
	stale := 0
	for _, err := range []error{a, b} {
		if err == nil {
			continue
		}
		if errors.Is(err, ErrStaleVersion) {
			stale++
			continue
		}
		t.Fatal(err)
	}
	if stale != 1 {
		t.Fatalf("assume: %v %v", a, b)
	}

	var current Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		row, err := lock(ctx, queries(tx), w.tenant, opened.Conversation.ID)
		current = row
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if current.Controller != ControllerHuman || current.AssigneeID != w.membership {
		t.Fatalf("assunção: %+v", current)
	}
	waiting, err := func() (Conversation, error) {
		var row Conversation
		err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			row, err = (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, current.ID, current.Version, trusted.Add(2*time.Minute))
			return err
		})
		return row, err
	}()
	if err != nil || waiting.Status != StatusWaitingCustomer {
		t.Fatalf("espera: %v %+v", err, waiting)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		again, err := (Repo{}).AcceptInbound(ctx, tx, InboundInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			ConnectorID: w.connector, ExternalMessageID: "wamid.2", Body: "ainda aqui",
			Period: "202610", TrustedAt: trusted.Add(3 * time.Minute), OpenedAt: trusted,
		})
		if err != nil {
			return err
		}
		if again.Conversation.Status != StatusOpen || !again.Conversation.LastCustomerMessageAt.Equal(trusted.Add(3*time.Minute)) {
			t.Fatalf("retorno: %+v", again.Conversation)
		}
		var waited string
		if err := tx.QueryRow(ctx, `
			SELECT controller FROM conversation_events
			WHERE conversation_id = $1 AND kind = 'wait_ended'
		`, again.Conversation.ID).Scan(&waited); err != nil {
			return err
		}
		if waited != ControllerHuman {
			t.Fatalf("wait_ended controlador: %s", waited)
		}
		current = again.Conversation
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var closed Conversation
	var closure Closure
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		closed, closure, err = (Repo{}).Close(ctx, tx, CloseInput{
			TenantID: w.tenant, ConversationID: current.ID, MembershipID: w.membership,
			ExpectedVersion: current.Version, Reason: "resolvido na caixa", Result: ResultNoResponse,
			Summary: "cliente não confirmou", AuthorKind: ControllerHuman, At: trusted.Add(4 * time.Minute),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if closed.Status != StatusClosed || !closed.LastCustomerMessageAt.Equal(trusted.Add(3*time.Minute)) || !closed.WaitingSince.IsZero() {
		t.Fatalf("encerramento: %+v", closed)
	}
	if closure.Result != ResultNoResponse || closure.AuthorID != w.user || closure.Summary != "cliente não confirmou" || closure.CycleNumber != 1 {
		t.Fatalf("ciclo: %+v", closure)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversation_cycles SET close_author_id = $1 WHERE conversation_id = $2`, uuid.New(), closed.ID)
		return err
	})
	if pgCode(err) != "23514" {
		t.Fatalf("autoria: %v", err)
	}
}

func TestSnapshotCursorRollbackAndResync(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var conversationID uuid.UUID
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).AcceptInbound(ctx, tx, InboundInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			ConnectorID: w.connector, ExternalMessageID: "wamid.snap", Body: "oi",
			Period: "202610", TrustedAt: now, OpenedAt: now,
		})
		conversationID = opened.Conversation.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var before Snapshot
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		before, err = (Repo{}).Snapshot(ctx, tx, SnapshotInput{TenantID: w.tenant, InboxID: w.inbox, ConversationID: conversationID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if before.NextCursor < 1 || len(before.Conversations) != 1 || len(before.Messages) != 1 {
		t.Fatalf("snapshot: %+v", before)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	holdErr := make(chan error, 1)
	go func() {
		holdErr <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := (Repo{}).Snapshot(ctx, tx, SnapshotInput{TenantID: w.tenant, InboxID: w.inbox}); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := (Repo{}).AcceptInbound(ctx, tx, InboundInput{
				TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
				ConnectorID: w.connector, ExternalMessageID: "wamid.later", Body: "depois",
				Period: "202610", TrustedAt: now.Add(time.Minute), OpenedAt: now,
			})
			return err
		})
	}()
	select {
	case err := <-writeErr:
		t.Fatalf("escritor avançou com o cursor compartilhado: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	if err := <-holdErr; err != nil {
		t.Fatal(err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	var page EventPage
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		page, err = (Repo{}).ReadEvents(ctx, tx, w.tenant, before.NextCursor)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != before.NextCursor+1 || len(page.Events) != 1 {
		t.Fatalf("página: %+v", page)
	}

	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).AcceptInbound(ctx, tx, InboundInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			ConnectorID: w.connector, ExternalMessageID: "wamid.rollback", Body: "nao",
			Period: "202610", TrustedAt: now.Add(2 * time.Minute), OpenedAt: now,
		})
		if err != nil {
			return err
		}
		return errors.New("desfaz")
	}); err == nil {
		t.Fatal("rollback ausente")
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		again, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, before.NextCursor)
		if err != nil {
			return err
		}
		if again.NextCursor != page.NextCursor {
			t.Fatalf("cursor após rollback: %d", again.NextCursor)
		}
		return (Repo{}).Compact(ctx, tx, w.tenant, again.NextCursor)
	}); err != nil {
		t.Fatal(err)
	}
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, 0)
		return err
	})
	if !errors.Is(err, ErrResync) {
		t.Fatalf("resync: %v", err)
	}
}

func TestJobCapacityAndSendLease(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	until := time.Now().UTC().Add(time.Hour)
	var held uuid.UUID
	for i := 0; i < 4; i++ {
		if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			row, err := (Repo{}).AcquireJob(ctx, tx, w.tenant, uuid.New(), QueueOutbound, until)
			if i == 0 {
				held = row.ID
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).AcquireJob(ctx, tx, w.tenant, uuid.New(), QueueOutbound, until)
		return err
	})
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacidade: %v", err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenant_job_reservations SET expires_at = now() - interval '1 minute' WHERE id = $1`, held)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).AcquireJob(ctx, tx, w.tenant, uuid.New(), QueueOutbound, until)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	ownerA, ownerB := uuid.New(), uuid.New()
	var lease Lease
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		lease, err = (Repo{}).AcquireLease(ctx, tx, w.tenant, w.inbox, ownerA, until)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).AcquireLease(ctx, tx, w.tenant, w.inbox, ownerB, until)
		return err
	})
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("lease: %v", err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE inbox_send_leases SET expires_at = now() - interval '1 second' WHERE inbox_id = $1`, w.inbox)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var taken Lease
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		taken, err = (Repo{}).AcquireLease(ctx, tx, w.tenant, w.inbox, ownerB, until)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if taken.LeaseVersion != lease.LeaseVersion+1 || taken.OwnerID != ownerB {
		t.Fatalf("retomada: %+v", taken)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).RenewLease(ctx, tx, w.tenant, w.inbox, ownerA, lease.LeaseVersion, until)
		return err
	})
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("renovação antiga: %v", err)
	}
}

func TestIngressRouteSecretsAndPrivileges(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	secret := Secret{KeyVersion: "v1", Ciphertext: []byte{1, 2, 3, 4}, AAD: "meta-app"}
	binding := MetaBinding{
		TenantID: w.tenant, AppRowID: uuid.New(), AppID: "app-" + w.tenant.String()[:8],
		Secret: secret, AADScope: "ingress_registry", AADEntity: "meta_app_registry", AADField: "app_secret", WabaRowID: uuid.New(), WabaID: "waba-" + w.inbox.String()[:8],
		PhoneRowID: uuid.New(), PhoneNumberID: "phone-" + w.inbox.String()[:8],
		ConnectorID: w.connector, InboxID: w.inbox,
	}
	binding.AADEntityID = binding.AppRowID
	binding.Secret.AAD = "ingress_registry\x1fmeta_app_registry\x1f" + binding.AppRowID.String() + "\x1fapp_secret"
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := (Repo{}).RegisterMeta(ctx, tx, binding); err != nil {
			return err
		}
		stored, err := (Repo{}).PutCredential(ctx, tx, w.tenant, w.connector, "access_token", secret)
		if err != nil {
			return err
		}
		got, err := (Repo{}).GetCredential(ctx, tx, w.tenant, w.connector, "access_token")
		if err != nil {
			return err
		}
		if stored.KeyVersion != got.KeyVersion || string(got.Ciphertext) != string(secret.Ciphertext) || got.AAD != secret.AAD {
			t.Fatalf("credencial: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var route Route
	var read Secret
	if err := dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		route, err = (Repo{}).ResolvePhone(ctx, tx, binding.AppID, binding.PhoneNumberID)
		if err != nil {
			return err
		}
		read, err = (Repo{}).ReadAppSecret(ctx, tx, binding.AppID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if route.TenantID != w.tenant || route.InboxID != w.inbox || string(read.Ciphertext) != string(secret.Ciphertext) {
		t.Fatalf("rota %+v segredo %+v", route, read)
	}

	body := []byte(`{"entry":[]}`)
	var first Delivery
	if err := dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		first, err = (Repo{}).StoreDelivery(ctx, tx, binding.AppID, body, "unknown_phone", "5511")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first.State != "quarantined" || first.Replayed {
		t.Fatalf("quarentena: %+v", first)
	}
	var replay Delivery
	if err := dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		replay, err = (Repo{}).StoreDelivery(ctx, tx, binding.AppID, body, "unknown_phone", "5511")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.ID != first.ID {
		t.Fatalf("replay de entrega: %+v", replay)
	}

	routedBody := []byte(`{"entry":[{"id":"1"}]}`)
	var routed Delivery
	var item WebhookResult
	if err := dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		routed, err = (Repo{}).StoreDelivery(ctx, tx, binding.AppID, routedBody, "", "")
		if err != nil {
			return err
		}
		item, err = (Repo{}).PutWebhookItem(ctx, tx, WebhookInput{
			TenantID: w.tenant, DeliveryID: routed.ID, InboxID: w.inbox, ConnectorID: w.connector,
			PhoneNumberID: binding.PhoneNumberID, ExternalKey: "item-1", Kind: "message",
			Payload: []byte(`{"id":"item-1"}`),
		})
		if err != nil {
			return err
		}
		return (Repo{}).MarkDelivery(ctx, tx, routed.ID, "routed")
	}); err != nil {
		t.Fatal(err)
	}
	err := dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).PutWebhookItem(ctx, tx, WebhookInput{
			TenantID: w.tenant, DeliveryID: routed.ID, InboxID: uuid.New(), ConnectorID: w.connector,
			PhoneNumberID: binding.PhoneNumberID, ExternalKey: "item-2", Kind: "message",
			Payload: []byte(`{}`),
		})
		return err
	})
	if pgCode(err) != "42501" {
		t.Fatalf("item sem vínculo: %v", err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		ids, err := (Repo{}).ListWebhookItems(ctx, tx, w.tenant, w.inbox)
		if err != nil {
			return err
		}
		if len(ids) != 1 || ids[0] != item.ID {
			t.Fatalf("itens: %v", ids)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	denied := []struct {
		name string
		run  func() error
	}{
		{"app deliveries", func() error {
			return testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT id FROM ingress_deliveries`)
				return err
			})
		}},
		{"platform messages", func() error {
			tx, err := testPools.Platform.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, `SELECT id FROM messages`)
			return err
		}},
		{"platform secrets", func() error {
			tx, err := testPools.Platform.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, `SELECT ciphertext FROM meta_app_secrets`)
			return err
		}},
		{"ingress messages", func() error {
			return dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT id FROM messages`)
				return err
			})
		}},
		{"ingress users", func() error {
			return dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT id FROM users`)
				return err
			})
		}},
		{"ingress conversations", func() error {
			return dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT id FROM conversations`)
				return err
			})
		}},
		{"ingress credentials", func() error {
			return dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT ciphertext FROM credential_secrets`)
				return err
			})
		}},
		{"ingress payload", func() error {
			return dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT payload FROM webhook_items`)
				return err
			})
		}},
	}
	for _, item := range denied {
		err := item.run()
		if pgCode(err) != "42501" {
			t.Fatalf("%s: %v", item.name, err)
		}
	}
	if err := dbWithIngress(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		return tx.QueryRow(ctx, `SELECT id FROM webhook_items WHERE delivery_id = $1`, routed.ID).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusDedupLinksWhenMessageArrives(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var first StatusResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		first, err = (Repo{}).RecordStatus(ctx, tx, StatusInput{
			TenantID: w.tenant, ConnectorID: w.connector, ExternalMessageID: "wamid.status",
			ExternalEventID: "evt-1", Kind: "delivered", TrustedAt: now, Billable: "unknown",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !first.Pending || first.MessageID != uuid.Nil {
		t.Fatalf("pendente: %+v", first)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		again, err := (Repo{}).RecordStatus(ctx, tx, StatusInput{
			TenantID: w.tenant, ConnectorID: w.connector, ExternalMessageID: "wamid.status",
			ExternalEventID: "evt-1", Kind: "delivered", TrustedAt: now,
		})
		if err != nil {
			return err
		}
		if !again.Duplicate || again.ID != first.ID {
			t.Fatalf("dedup: %+v", again)
		}
		_, err = (Repo{}).AcceptInbound(ctx, tx, InboundInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			ConnectorID: w.connector, ExternalMessageID: "wamid.status", Body: "oi",
			Period: "202610", TrustedAt: now, OpenedAt: now,
		})
		if err != nil {
			return err
		}
		var pending bool
		var messageID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT pending, message_id FROM message_status_events WHERE id = $1`, first.ID).Scan(&pending, &messageID); err != nil {
			return err
		}
		if pending || messageID == nil {
			t.Fatal("status não vinculado")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if StatusDedupKey("evt-1", "wamid.status", "delivered", now, "", "") != "event:evt-1" {
		t.Fatal("chave por evento")
	}
}

func TestReleaseJobFreesCapacity(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	until := time.Now().UTC().Add(time.Hour)
	owners := make([]uuid.UUID, 4)
	ids := make([]uuid.UUID, 4)
	for i := 0; i < 4; i++ {
		owners[i] = uuid.New()
		if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			row, err := (Repo{}).AcquireJob(ctx, tx, w.tenant, owners[i], QueueOutbound, until)
			ids[i] = row.ID
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).AcquireJob(ctx, tx, w.tenant, uuid.New(), QueueOutbound, until)
		return err
	})
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacidade: %v", err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return (Repo{}).ReleaseJob(ctx, tx, w.tenant, ids[0], owners[0], QueueOutbound)
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).AcquireJob(ctx, tx, w.tenant, uuid.New(), QueueOutbound, until)
		return err
	}); err != nil {
		t.Fatalf("releitura: %v", err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return (Repo{}).ReleaseJob(ctx, tx, w.tenant, ids[0], owners[0], QueueOutbound)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("segunda liberação: %v", err)
	}
}

func TestSendReservationConsumeAndRelease(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	owner := uuid.New()
	var attemptA, attemptB uuid.UUID
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now,
		})
		if err != nil {
			return err
		}
		first, err := (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: opened.Conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "hold-a",
			Payload: []byte(`{"text":"a"}`), Body: "a", At: now,
		})
		if err != nil {
			return err
		}
		second, err := (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: opened.Conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "hold-b",
			Payload: []byte(`{"text":"b"}`), Body: "b", At: now,
		})
		if err != nil {
			return err
		}
		attemptA, attemptB = first.AttemptID, second.AttemptID
		if _, err := (Repo{}).AcquireLease(ctx, tx, w.tenant, w.inbox, owner, until); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var heldA, heldB uuid.UUID
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		heldA, err = (Repo{}).ReserveSend(ctx, tx, w.tenant, w.inbox, attemptA, owner, 1, until)
		if err != nil {
			return err
		}
		heldB, err = (Repo{}).ReserveSend(ctx, tx, w.tenant, w.inbox, attemptB, owner, 1, until)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := (Repo{}).ConsumeSend(ctx, tx, w.tenant, heldA, owner); err != nil {
			return err
		}
		if err := (Repo{}).ConsumeSend(ctx, tx, w.tenant, heldA, owner); err != nil {
			return err
		}
		return (Repo{}).ReleaseSend(ctx, tx, w.tenant, heldB, owner)
	}); err != nil {
		t.Fatal(err)
	}
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return (Repo{}).ConsumeSend(ctx, tx, w.tenant, heldB, owner)
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("consumo após liberação: %v", err)
	}
	var state string
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state FROM send_reservations WHERE id = $1`, heldA).Scan(&state)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "consumed" {
		t.Fatalf("estado: %s", state)
	}
}

func TestAttemptStatusAndProtocolAdvanceCursor(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var conversationID uuid.UUID
	var attemptID uuid.UUID
	var cursor int64
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now,
		})
		if err != nil {
			return err
		}
		conversationID = opened.Conversation.ID
		sent, err := (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversationID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "cursor-1",
			Payload: []byte(`{"text":"ola"}`), Body: "ola", At: now,
		})
		if err != nil {
			return err
		}
		attemptID = sent.AttemptID
		page, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, 0)
		cursor = page.NextCursor
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		state, err := (Repo{}).TransitionAttempt(ctx, tx, AttemptTransition{
			TenantID: w.tenant, AttemptID: attemptID, To: "sending",
		})
		if err != nil {
			return err
		}
		if state != "sending" {
			t.Fatalf("estado: %s", state)
		}
		_, err = (Repo{}).TransitionAttempt(ctx, tx, AttemptTransition{
			TenantID: w.tenant, AttemptID: attemptID, To: "accepted", ExternalMessageID: "wamid.out",
		})
		if err != nil {
			return err
		}
		page, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, cursor)
		if err != nil {
			return err
		}
		if len(page.Events) != 2 || page.Events[0].Type != "message.attempt" || page.Events[1].Type != "message.attempt" {
			t.Fatalf("tentativas: %+v", page.Events)
		}
		cursor = page.NextCursor
		status, err := (Repo{}).RecordStatus(ctx, tx, StatusInput{
			TenantID: w.tenant, ConnectorID: w.connector, ExternalMessageID: "wamid.out",
			ExternalEventID: "evt-read", Kind: "read", TrustedAt: now.Add(time.Minute),
		})
		if err != nil {
			return err
		}
		if status.Pending || status.Duplicate {
			t.Fatalf("recibo: %+v", status)
		}
		page, err = (Repo{}).ReadEvents(ctx, tx, w.tenant, cursor)
		if err != nil {
			return err
		}
		if len(page.Events) != 1 || page.Events[0].Type != "message.status" || page.Events[0].AggregateID != conversationID {
			t.Fatalf("status: %+v", page.Events)
		}
		cursor = page.NextCursor
		again, err := (Repo{}).RecordStatus(ctx, tx, StatusInput{
			TenantID: w.tenant, ConnectorID: w.connector, ExternalMessageID: "wamid.out",
			ExternalEventID: "evt-read", Kind: "read", TrustedAt: now.Add(time.Minute),
		})
		if err != nil {
			return err
		}
		if !again.Duplicate {
			t.Fatal("dedup ausente")
		}
		page, err = (Repo{}).ReadEvents(ctx, tx, w.tenant, cursor)
		if err != nil {
			return err
		}
		if len(page.Events) != 0 {
			t.Fatalf("dedup publicou: %+v", page.Events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var version int64
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		row, err := lock(ctx, queries(tx), w.tenant, conversationID)
		if err != nil {
			return err
		}
		version = row.Version
		page, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, 0)
		cursor = page.NextCursor
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := (Repo{}).AttachExternalProtocol(ctx, tx, w.tenant, conversationID, version, "ERP-1"); err != nil {
			return err
		}
		page, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, cursor)
		if err != nil {
			return err
		}
		if len(page.Events) != 1 || page.Events[0].Type != "conversation.external_protocol" {
			t.Fatalf("protocolo: %+v", page.Events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClosedReplayAndConnectorBinding(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var conversation Conversation
	payload := []byte(`{"text":"ola"}`)
	in := OutboundInput{
		TenantID: w.tenant, ConnectorID: w.connector, PrincipalID: w.membership,
		Operation: "send_text", ClientRequestID: "req-close", Payload: payload, Body: "ola", At: now,
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now,
		})
		if err != nil {
			return err
		}
		conversation = opened.Conversation
		in.ConversationID = conversation.ID
		if _, err := (Repo{}).EnqueueText(ctx, tx, in); err != nil {
			return err
		}
		closed, _, err := (Repo{}).Close(ctx, tx, CloseInput{
			TenantID: w.tenant, ConversationID: conversation.ID, MembershipID: w.membership,
			ExpectedVersion: conversation.Version, Reason: "fim", Result: ResultResolved,
			Summary: "encerrado", AuthorKind: ControllerHuman, At: now.Add(time.Minute),
		})
		conversation = closed
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		replay, err := (Repo{}).EnqueueText(ctx, tx, in)
		if err != nil {
			return err
		}
		if !replay.Replayed {
			t.Fatal("replay após fechar")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fresh := in
	fresh.ClientRequestID = "req-new"
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).EnqueueText(ctx, tx, fresh)
		return err
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("nova chave fechada: %v", err)
	}
	other := uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true)
		`, other, w.tenant, "origin-"+other.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		reopened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", Status: StatusOpen, OpenedAt: now,
		})
		if err != nil {
			return err
		}
		_, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: reopened.Conversation.ID, ConnectorID: other,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "req-other",
			Payload: payload, Body: "ola", At: now,
		})
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("conector: %v", err)
	}
}

func TestDatabaseRejectsIncompatibleBinding(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var conversationID uuid.UUID
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now,
		})
		conversationID = opened.Conversation.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	otherContact := uuid.New()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO conversations (
				id, tenant_id, summary, inbox_id, contact_id, contact_identity_id,
				status, controller, protocol, protocol_period, cycle_number
			) VALUES ($1, $2, '', $3, $4, $5, 'open', 'menu', '202610-000099', '202610', 1)
		`, uuid.New(), w.tenant, w.inbox, otherContact, w.identity)
		return err
	})
	if pgMessage(err) != "amarração incompatível" {
		t.Fatalf("contato: %v", err)
	}
	other := uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true)
		`, other, w.tenant, "origin-"+other.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (tenant_id, id, conversation_id, connector_id, direction, origin, visibility)
			VALUES ($1, $2, $3, $4, 'inbound', 'customer', 'public')
		`, w.tenant, uuid.New(), conversationID, other)
		return err
	})
	if pgMessage(err) != "conector não pertence à caixa" {
		t.Fatalf("mensagem: %v", err)
	}
	appID := uuid.New()
	wabaID := uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`,
			"app-"+appID.String()[:8], appID, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f"+appID.String()+"\x1fapp_secret"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO meta_wabas (tenant_id, id, meta_app_id, waba_id, status)
			VALUES ($1, $2, $3, $4, 'active')
		`, w.tenant, wabaID, appID, "waba-"+wabaID.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO meta_phone_numbers (
				tenant_id, id, meta_waba_id, phone_number_id, status, connector_id, inbox_id
			) VALUES ($1, $2, $3, $4, 'active', $5, $6)
		`, w.tenant, uuid.New(), wabaID, "phone-"+wabaID.String()[:8], other, w.inbox)
		return err
	})
	if pgCode(err) != "23503" {
		t.Fatalf("telefone: %v", err)
	}
}

func dbWithIngress(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithIngressTx(ctx, testPools.Ingress, fn)
}

func TestAssumeCloseCancelsQueuedBotAndMenu(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	var conversation Conversation
	var sending, menuQueued, botQueued, humanQueued OutboundResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now,
		})
		if err != nil {
			return err
		}
		conversation = opened.Conversation
		sending, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "menu-sending",
			Payload: []byte(`{"text":"enviando"}`), Body: "enviando", At: now,
		})
		if err != nil {
			return err
		}
		if _, err := (Repo{}).TransitionAttempt(ctx, tx, AttemptTransition{
			TenantID: w.tenant, AttemptID: sending.AttemptID, To: "sending",
		}); err != nil {
			return err
		}
		menuQueued, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "menu-fila",
			Payload: []byte(`{"text":"cardapio"}`), Body: "cardapio", At: now,
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE conversations SET controller = 'bot'
			WHERE tenant_id = $1 AND id = $2
		`, w.tenant, conversation.ID); err != nil {
			return err
		}
		botQueued, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "bot-fila",
			Payload: []byte(`{"text":"fluxo"}`), Body: "fluxo", At: now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var cursor int64
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		page, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, 0)
		cursor = page.NextCursor
		if err != nil {
			return err
		}
		conversation, err = (Repo{}).Assume(ctx, tx, AssumeInput{
			TenantID: w.tenant, ConversationID: conversation.ID, MembershipID: w.membership,
			ExpectedVersion: conversation.Version, At: now.Add(time.Minute),
		})
		if err != nil {
			return err
		}
		page, err = (Repo{}).ReadEvents(ctx, tx, w.tenant, cursor)
		if err != nil {
			return err
		}
		if err := expectCancelledAttempts(t, page.Events, menuQueued.AttemptID, botQueued.AttemptID); err != nil {
			return err
		}
		cursor = page.NextCursor
		snap, err := (Repo{}).Snapshot(ctx, tx, SnapshotInput{
			TenantID: w.tenant, InboxID: w.inbox, ConversationID: conversation.ID,
		})
		if err != nil {
			return err
		}
		if state := localState(snap.Messages, "cardapio"); state != "cancelled" {
			t.Fatalf("menu após assumir: %s", state)
		}
		if state := localState(snap.Messages, "fluxo"); state != "cancelled" {
			t.Fatalf("bot após assumir: %s", state)
		}
		if state := localState(snap.Messages, "enviando"); state != "sending" {
			t.Fatalf("envio em curso: %s", state)
		}
		for _, id := range []uuid.UUID{menuQueued.AttemptID, botQueued.AttemptID} {
			got, err := (Repo{}).OutboundState(ctx, tx, w.tenant, id)
			if err != nil {
				return err
			}
			if got != "cancelled" {
				t.Fatalf("tentativa %s: %s", id, got)
			}
		}
		humanQueued, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "humano",
			Payload: []byte(`{"text":"eu assumo"}`), Body: "eu assumo", At: now.Add(time.Minute),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		closed, _, err := (Repo{}).Close(ctx, tx, CloseInput{
			TenantID: w.tenant, ConversationID: conversation.ID, MembershipID: w.membership,
			ExpectedVersion: conversation.Version, Reason: "fim", Result: ResultResolved,
			Summary: "assumido", AuthorKind: ControllerHuman, At: now.Add(2 * time.Minute),
		})
		if err != nil {
			return err
		}
		snap, err := (Repo{}).Snapshot(ctx, tx, SnapshotInput{
			TenantID: w.tenant, InboxID: w.inbox, ConversationID: closed.ID,
		})
		if err != nil {
			return err
		}
		if state := localState(snap.Messages, "eu assumo"); state != "queued" {
			t.Fatalf("humano após encerrar: %s", state)
		}
		if state := localState(snap.Messages, "enviando"); state != "sending" {
			t.Fatalf("envio em curso após encerrar: %s", state)
		}
		got, err := (Repo{}).OutboundState(ctx, tx, w.tenant, humanQueued.AttemptID)
		if err != nil {
			return err
		}
		if got != "queued" {
			t.Fatalf("tentativa humana: %s", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var closedMenu, closedBot OutboundResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{
			TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity,
			Period: "202610", OpenedAt: now.Add(3 * time.Minute),
		})
		if err != nil {
			return err
		}
		conversation = opened.Conversation
		closedMenu, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "menu-encerra",
			Payload: []byte(`{"text":"cardapio 2"}`), Body: "cardapio 2", At: now.Add(3 * time.Minute),
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE conversations SET controller = 'bot'
			WHERE tenant_id = $1 AND id = $2
		`, w.tenant, conversation.ID); err != nil {
			return err
		}
		closedBot, err = (Repo{}).EnqueueText(ctx, tx, OutboundInput{
			TenantID: w.tenant, ConversationID: conversation.ID, ConnectorID: w.connector,
			PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "bot-encerra",
			Payload: []byte(`{"text":"fluxo 2"}`), Body: "fluxo 2", At: now.Add(3 * time.Minute),
		})
		if err != nil {
			return err
		}
		page, err := (Repo{}).ReadEvents(ctx, tx, w.tenant, 0)
		if err != nil {
			return err
		}
		cursor = page.NextCursor
		if _, _, err := (Repo{}).Close(ctx, tx, CloseInput{
			TenantID: w.tenant, ConversationID: conversation.ID, MembershipID: w.membership,
			ExpectedVersion: conversation.Version, Reason: "bot encerra", Result: ResultNoResponse,
			Summary: "fila automática", AuthorKind: ControllerBot, At: now.Add(4 * time.Minute),
		}); err != nil {
			return err
		}
		page, err = (Repo{}).ReadEvents(ctx, tx, w.tenant, cursor)
		if err != nil {
			return err
		}
		if err := expectCancelledAttempts(t, page.Events, closedMenu.AttemptID, closedBot.AttemptID); err != nil {
			return err
		}
		snap, err := (Repo{}).Snapshot(ctx, tx, SnapshotInput{
			TenantID: w.tenant, InboxID: w.inbox, ConversationID: conversation.ID,
		})
		if err != nil {
			return err
		}
		if state := localState(snap.Messages, "cardapio 2"); state != "cancelled" {
			t.Fatalf("menu após encerrar: %s", state)
		}
		if state := localState(snap.Messages, "fluxo 2"); state != "cancelled" {
			t.Fatalf("bot após encerrar: %s", state)
		}
		for _, id := range []uuid.UUID{closedMenu.AttemptID, closedBot.AttemptID} {
			got, err := (Repo{}).OutboundState(ctx, tx, w.tenant, id)
			if err != nil {
				return err
			}
			if got != "cancelled" {
				t.Fatalf("tentativa %s: %s", id, got)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func localState(messages []Message, body string) string {
	for _, message := range messages {
		if message.Body == body {
			return message.LocalState
		}
	}
	return ""
}

func expectCancelledAttempts(t *testing.T, events []Event, attemptIDs ...uuid.UUID) error {
	t.Helper()
	seen := map[uuid.UUID]bool{}
	for _, event := range events {
		if event.Type != "message.attempt" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		if _, ok := payload["body"]; ok {
			t.Fatalf("evento com corpo: %s", event.Payload)
		}
		if payload["state"] != "cancelled" {
			continue
		}
		attempt, _ := payload["attempt_id"].(string)
		seen[uuid.MustParse(attempt)] = true
	}
	for _, id := range attemptIDs {
		if !seen[id] {
			t.Fatalf("sem message.attempt cancelled para %s em %+v", id, events)
		}
	}
	return nil
}
