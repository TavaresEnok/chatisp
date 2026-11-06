package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReapplyPrivacyRedactsStructuredContentAndPreservesOtherTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	type fixture struct{ tenant, contact, conv, attachment, document, namespace uuid.UUID }
	seed := func() fixture {
		f := fixture{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
		connector, inbox, sector, identity, user, member := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		statements := []struct {
			q string
			a []any
		}{
			{`INSERT INTO tenants(id,timezone,status) VALUES($1,'UTC','active')`, []any{f.tenant}},
			{`INSERT INTO users(id,email,status) VALUES($1,$2,'active')`, []any{user, user.String() + "@example.com"}},
			{`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,'admin','active')`, []any{member, f.tenant, user}},
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{connector, f.tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Box')`, []any{inbox, f.tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Support')`, []any{f.tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{f.tenant, sector, inbox}},
			{`INSERT INTO contacts(id,tenant_id,name,phone,email,notes) VALUES($1,$2,'private name','+551188887777','person@example.com','private notes')`, []any{f.contact, f.tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,'private-channel-id')`, []any{identity, f.tenant, f.contact, connector}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,$7,'202611',1)`, []any{f.conv, f.tenant, inbox, sector, f.contact, identity, "P-" + f.conv.String()}},
			{`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,now())`, []any{f.tenant, uuid.New(), f.conv}},
			{`INSERT INTO messages(id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at,content_context) VALUES($1,$2,$3,$4,'inbound','customer','public','private body','received',now(),now(),'{"location":{"name":"private address"}}')`, []any{uuid.New(), f.tenant, f.conv, connector}},
			{`INSERT INTO contact_documents(tenant_id,contact_id,kind,key_version,ciphertext,document_hmac,updated_by_membership_id,version) VALUES($1,$2,'cpf','1',$3,$4,$5,1)`, []any{f.tenant, f.contact, make([]byte, 28), make([]byte, 32), member}},
			{`INSERT INTO attachment_budgets(tenant_id,namespace,reserved_bytes) VALUES($1,$2,10)`, []any{f.tenant, f.namespace}},
			{`INSERT INTO attachments(tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,object_version,state,expires_at) VALUES($1,$2,$3,$4,'incoming','private.txt',10,$5,'etag:synthetic','validated',now()+interval '1 day')`, []any{f.tenant, f.attachment, f.conv, uuid.New(), f.namespace.String() + "/" + f.attachment.String()}},
			{`INSERT INTO realtime_tenant_cursors(tenant_id,seq) VALUES($1,3)`, []any{f.tenant}},
			{`INSERT INTO realtime_events(tenant_id,seq,id,event_type,aggregate_type,aggregate_id,version,payload) VALUES($1,3,$2,'conversation.message','conversation',$3,1,'{"content":{"name":"private address"},"reply_to_text":"private quote"}')`, []any{f.tenant, uuid.New(), f.conv}},
		}
		for _, stmt := range statements {
			if _, err := conn.Exec(ctx, stmt.q, stmt.a...); err != nil {
				t.Fatal(err)
			}
		}
		return f
	}
	a, b := seed(), seed()
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE ledger_external(tenant_id uuid,subject text,subject_id uuid)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO ledger_external VALUES($1,'contact_privacy',$2),($1,'contact_privacy',$2),($1,'contact_privacy',$3)`, a.tenant, a.contact, b.contact); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "backup", "reapply-deletions.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := conn.Exec(ctx, string(script)); err != nil {
			t.Fatal(err)
		}
		var body, content, name, payload, identity string
		var keyVersion *string
		var reserved, events, cursor, cycles int64
		var objects int
		if err := conn.QueryRow(ctx, `SELECT m.body,m.content_context::text,c.name,d.key_version FROM messages m JOIN conversations v ON v.tenant_id=m.tenant_id AND v.id=m.conversation_id JOIN contacts c ON c.tenant_id=v.tenant_id AND c.id=v.contact_id JOIN contact_documents d ON d.tenant_id=c.tenant_id AND d.contact_id=c.id WHERE m.tenant_id=$1`, a.tenant).Scan(&body, &content, &name, &keyVersion); err != nil {
			t.Fatal(err)
		}
		if body != "" || content != "{}" || name != "titular excluído" || keyVersion != nil {
			t.Fatalf("private content survived body=%s context=%s name=%s key=%v", body, content, name, keyVersion)
		}
		if err := conn.QueryRow(ctx, `SELECT payload::text,seq FROM realtime_events WHERE tenant_id=$1`, a.tenant).Scan(&payload, &events); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT seq FROM realtime_tenant_cursors WHERE tenant_id=$1`, a.tenant).Scan(&cursor); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "private") || events != 3 || cursor != 3 {
			t.Fatalf("unsafe replay payload=%s seq=%d cursor=%d", payload, events, cursor)
		}
		if err := conn.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, a.tenant).Scan(&reserved); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM conversation_cycles WHERE tenant_id=$1`, a.tenant).Scan(&cycles); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM deletion_records WHERE tenant_id=$1 AND subject='attachment' AND subject_id=$2 AND object_key=$3 AND object_version='etag:synthetic' AND object_bytes=10`, a.tenant, a.attachment, a.namespace.String()+"/"+a.attachment.String()).Scan(&objects); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT external_id FROM contact_channel_identities WHERE tenant_id=$1`, a.tenant).Scan(&identity); err != nil {
			t.Fatal(err)
		}
		if reserved != 0 || cycles != 1 || objects != 1 || !strings.HasPrefix(identity, "excluido-") {
			t.Fatalf("restore not idempotent reserved=%d cycles=%d objects=%d identity=%s", reserved, cycles, objects, identity)
		}
	}
	var body string
	var count int
	if err := conn.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1`, b.tenant).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1`, b.tenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if body != "private body" || count != 1 {
		t.Fatalf("other tenant changed body=%s attachments=%d", body, count)
	}
}
