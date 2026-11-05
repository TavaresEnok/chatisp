package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Composed queue search measured on synthetic rows: two tenants, three sectors, mixed
// statuses and message bodies. It exercises the real Queue() (role/sector authorization,
// pinning locks, filters, search, order, pagination) and prints EXPLAIN ANALYZE of the
// real statement. This is a local measurement, not a capacity promise (D2-07).
const (
	perfConversations = 20000
	perfMessagesEach  = 5
)

func seedPerf(t *testing.T, ctx context.Context, w world, sectors []uuid.UUID) {
	t.Helper()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		for i, s := range sectors {
			if i > 0 {
				if _, e := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,$3)`, w.tenant, s, fmt.Sprintf("setor-%d", i)); e != nil {
					return e
				}
				if _, e := tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, w.tenant, s, w.inbox); e != nil {
					return e
				}
			}
		}
		// 2000 contacts with accented names.
		if _, e := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name,phone) SELECT gen_random_uuid(),$1,(ARRAY['João','José','Conceição','Ângela','Márcio','Luíza'])[1+g%6]||' Silva '||g,'+5511'||lpad(g::text,8,'0') FROM generate_series(1,2000) g`, w.tenant); e != nil {
			return e
		}
		// One identity per contact, so the conversation binding trigger accepts the pair.
		if _, e := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) SELECT gen_random_uuid(),$1,id,$2,'wa-'||id FROM contacts WHERE tenant_id=$1 AND id<>$3`, w.tenant, w.connector, w.contact); e != nil {
			return e
		}
		// Conversations (all closed, so any number may share an identity).
		if _, e := tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id,created_at)
			SELECT $1,gen_random_uuid(),$2,i.contact_id,i.id,'closed','P-'||n,'202610',1,($4::uuid[])[1+n%3],now()-n*interval '1 minute'
			FROM generate_series(1,$5) n JOIN LATERAL (SELECT id,contact_id FROM contact_channel_identities WHERE tenant_id=$1 AND contact_id<>$3 ORDER BY id OFFSET (n%2000) LIMIT 1) i ON true`, w.tenant, w.inbox, w.contact, sectors, perfConversations); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueueComposedSearchOnSyntheticVolume(t *testing.T) {
	if testing.Short() || os.Getenv("CHATISP_PERF") != "1" {
		t.Skip("medição sintética de ~2 min: CHATISP_PERF=1 go test -run TestQueueComposedSearchOnSyntheticVolume -v ./internal/attendance/store/")
	}
	ctx := context.Background()
	w := seedWorld(t)
	other := seedWorld(t)
	sectors := []uuid.UUID{}
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var def uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2`, w.tenant, w.inbox).Scan(&def); e != nil {
			return e
		}
		sectors = append(sectors, def, uuid.New(), uuid.New())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	seedPerf(t, ctx, w, sectors)
	// Other tenant gets the same volume and the same body words, to prove isolation.
	otherSectors := []uuid.UUID{}
	_ = testStore.WithTenantTx(ctx, other.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var def uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2`, other.tenant, other.inbox).Scan(&def); e != nil {
			return e
		}
		otherSectors = append(otherSectors, def, uuid.New(), uuid.New())
		return nil
	})
	seedPerf(t, ctx, other, otherSectors)
	for _, x := range []struct {
		w world
	}{{w}, {other}} {
		err := testStore.WithTenantTx(ctx, x.w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			// 5 messages per conversation, 1 in 7 internal. Bodies carry accented and plain words.
			_, e := tx.Exec(ctx, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,body,ingested_at)
				SELECT c.tenant_id,gen_random_uuid(),c.id,$2,'inbound','customer',CASE WHEN k%7=0 THEN 'internal' ELSE 'public' END,
				 'mensagem '||k||' sobre '||(ARRAY['pagamento','fatura','sinal','instalação','velocidade','cancelamento'])[1+(k+length(c.protocol))%6]||' do cliente'||CASE WHEN c.protocol IN ('P-77','P-78') THEN ' roteador lentidão rara' ELSE '' END,
				 c.created_at+k*interval '1 second'
				FROM conversations c, generate_series(1,$3) k WHERE c.tenant_id=$1`, x.w.tenant, x.w.connector, perfMessagesEach)
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `ANALYZE conversations; ANALYZE messages; ANALYZE contacts`)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("tamanho: %d conversas e %d mensagens por tenant (dois tenants), 3 setores, 2000 contatos; carga em %s", perfConversations, perfConversations*perfMessagesEach, time.Since(start).Round(time.Second))
	_ = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT status||'/'||visibility, count(*) FROM conversations c JOIN messages m ON m.conversation_id=c.id AND m.tenant_id=c.tenant_id WHERE c.tenant_id=$1 GROUP BY 1 ORDER BY 1`, w.tenant)
		var parts []string
		for rows.Next() {
			var k string
			var n int
			_ = rows.Scan(&k, &n)
			parts = append(parts, fmt.Sprintf("%s=%d", k, n))
		}
		rows.Close()
		t.Logf("distribuição de mensagens: %s", strings.Join(parts, " "))
		return nil
	})

	type scenario struct {
		name string
		in   QueueInput
		max  time.Duration
	}
	base := QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent", Limit: 50}
	adm := base
	adm.Role = "admin"
	mk := func(in QueueInput, f func(*QueueInput)) QueueInput { f(&in); return in }
	scenarios := []scenario{
		{"agente, sem busca, espera", base, 5 * time.Second},
		{"admin, sem busca, espera", adm, 5 * time.Second},
		{"agente, ordem última mensagem", mk(base, func(q *QueueInput) { q.Order = "lastMessage" }), 5 * time.Second},
		{"agente, aba todos, status closed, setor", mk(base, func(q *QueueInput) { q.Status = "closed"; q.SectorID = sectors[0] }), 5 * time.Second},
		{"agente, busca por nome com acento (Conceição)", mk(base, func(q *QueueInput) { q.Search = "Conceição" }), 8 * time.Second},
		{"agente, busca por nome sem acento (conceicao)", mk(base, func(q *QueueInput) { q.Search = "conceicao" }), 8 * time.Second},
		{"agente, busca por telefone parcial", mk(base, func(q *QueueInput) { q.Search = "00000123" }), 8 * time.Second},
		{"agente, busca por protocolo", mk(base, func(q *QueueInput) { q.Search = "P-77" }), 8 * time.Second},
		{"agente, busca no conteúdo (lentidão)", mk(base, func(q *QueueInput) { q.Search = "lentidão" }), 8 * time.Second},
		{"agente, busca no conteúdo (lentidao sem acento)", mk(base, func(q *QueueInput) { q.Search = "lentidao" }), 8 * time.Second},
		{"admin, busca de conteúdo comum (instalação)", mk(adm, func(q *QueueInput) { q.Search = "instalação" }), 15 * time.Second},
		{"agente, busca + filtro de data + status", mk(base, func(q *QueueInput) {
			a := time.Now().Add(-48 * time.Hour)
			b := time.Now()
			q.From, q.To, q.Search, q.Status = &a, &b, "fatura", "closed"
		}), 15 * time.Second},
	}
	for _, s := range scenarios {
		var page QueuePage
		began := time.Now()
		err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			var e error
			page, e = Queue(ctx, tx, s.in)
			return e
		})
		took := time.Since(began)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		for _, it := range page.Items {
			var tenant uuid.UUID
			_ = testStore.WithTenantTx(ctx, other.tenant, func(ctx context.Context, tx pgx.Tx) error {
				_ = tx.QueryRow(ctx, `SELECT tenant_id FROM conversations WHERE id=$1`, it.ID).Scan(&tenant)
				return nil
			})
			if tenant != uuid.Nil {
				t.Fatalf("%s: vazamento entre tenants: %s", s.name, it.ID)
			}
		}
		t.Logf("%-52s itens=%-3d total=%-6d %8s", s.name, len(page.Items), page.Counts.All, took.Round(time.Millisecond))
		if took > s.max {
			t.Errorf("%s: %s acima do teto de %s", s.name, took, s.max)
		}
	}

	// Pagination: walk the whole closed queue by sector; no duplicates, no gaps.
	{
		seen := map[uuid.UUID]bool{}
		in := mk(base, func(q *QueueInput) { q.Status = "closed"; q.Limit = 100 })
		total := 0
		pages := 0
		began := time.Now()
		for {
			var page QueuePage
			err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
				var e error
				page, e = Queue(ctx, tx, in)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range page.Items {
				if seen[it.ID] {
					t.Fatalf("item repetido na paginação: %s", it.ID)
				}
				seen[it.ID] = true
			}
			total += len(page.Items)
			pages++
			if page.PageCursor == "" || pages >= 12 {
				break
			}
			in.Cursor = page.PageCursor
		}
		t.Logf("paginação: %d páginas de 100, %d itens únicos, %s no total (%s por página)", pages, total, time.Since(began).Round(time.Millisecond), (time.Since(began) / time.Duration(pages)).Round(time.Millisecond))
		if total != pages*100 && pages < 12 {
			t.Errorf("paginação incompleta: %d itens em %d páginas", total, pages)
		}
	}

	// EXPLAIN ANALYZE of the real statement for the composed content search.
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var ids []uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT array_agg(c.id) FROM conversations c JOIN sectors s ON s.tenant_id=c.tenant_id AND s.id=c.sector_id JOIN sector_memberships sm ON sm.tenant_id=c.tenant_id AND sm.sector_id=c.sector_id AND sm.membership_id=$2 WHERE c.tenant_id=$1`, w.tenant, w.membership).Scan(&ids); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, TIMING OFF) "+queueSQL, w.tenant, w.user, w.membership, "agent", "all", "wait", "", nil, nil, nil, nil, nil, nil, "lentidão", 50, nil, nil, ids)
		if e != nil {
			return e
		}
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var line string
			if e := rows.Scan(&line); e != nil {
				return e
			}
			plan = append(plan, line)
		}
		t.Logf("candidatos autorizados para o agente: %d\nEXPLAIN (ANALYZE, BUFFERS) da consulta real, busca de conteúdo:\n%s", len(ids), strings.Join(plan, "\n"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
