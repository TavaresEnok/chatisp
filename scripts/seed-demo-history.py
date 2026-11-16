#!/usr/bin/env python3
"""Append an explicitly authorized synthetic history to an existing demo dataset."""
import argparse
import json
import subprocess
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--dataset', required=True, type=Path)
parser.add_argument('--project', required=True)
parser.add_argument('--apply', action='store_true', help='Without this flag, only describe the proposed dataset.')
args = parser.parse_args()
data = json.loads(args.dataset.read_text())
tenant, inbox, connector, owner = (str(uuid.UUID(data[k])) for k in ('tenant', 'inbox', 'connector', 'owner'))
assert args.project == 'chatisp-pronto-hoje', 'This fixture targets only the authorized demo installation.'
namespace = uuid.UUID('124419b1-a542-49f2-a613-43d8475cf45d')
def uid(value):
    return str(uuid.uuid5(namespace, value))
def lit(value):
    if value is None: return 'NULL'
    if isinstance(value, bool): return 'true' if value else 'false'
    if isinstance(value, int): return str(value)
    return "'" + str(value).replace("'", "''") + "'"
def psql(sql):
    result = subprocess.run(['docker', 'exec', '-i', args.project + '-postgres-1', 'psql', '-U', 'chatisp', '-d', 'chatisp', '-v', 'ON_ERROR_STOP=1', '-Atq'], input=sql, text=True, capture_output=True)
    if result.returncode: raise RuntimeError(result.stderr)
    return result.stdout.strip()

print('APPEND DEMO-HIST-20261116: 24 contacts; 72 closed + 24 current conversations; 768 historical text messages; 96 notes. No external sends, credentials or modifications to existing records.')
if not args.apply:
    raise SystemExit(0)
agents = {a['email']: str(uuid.UUID(a['membership'])) for a in data['agents']}
owner_user = psql(f"SELECT user_id FROM memberships WHERE tenant_id={lit(tenant)} AND id={lit(owner)} AND role='admin' AND status='active';")
owner_user = str(uuid.UUID(owner_user))
users = json.loads(psql(f"SELECT json_object_agg(id,user_id) FROM memberships WHERE tenant_id={lit(tenant)};"))
sectors = json.loads(psql(f"SELECT json_object_agg(name,id) FROM sectors WHERE tenant_id={lit(tenant)};"))
demo_tag = psql(f"SELECT id FROM annotation_tags WHERE tenant_id={lit(tenant)} AND name='DEMONSTRAÇÃO';")
demo_tag = str(uuid.UUID(demo_tag))
tags = json.loads(psql(f"SELECT json_object_agg(name,id) FROM annotation_tags WHERE tenant_id={lit(tenant)};"))
q = ['BEGIN;', "SET LOCAL lock_timeout='5s';", "SELECT pg_advisory_xact_lock(20261116, 24);"]
q.append(f"""DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM inboxes i JOIN channel_connectors c ON c.tenant_id=i.tenant_id AND c.id=i.connector_id WHERE i.tenant_id={lit(tenant)} AND i.id={lit(inbox)} AND c.id={lit(connector)} AND NOT c.enabled AND c.status='disconnected' AND c.remote_origin LIKE 'demonstracao-%') THEN RAISE EXCEPTION 'Not the disabled demonstration inbox'; END IF;
 IF EXISTS(SELECT 1 FROM channel_connectors WHERE tenant_id={lit(tenant)} AND enabled) THEN RAISE EXCEPTION 'Tenant has an enabled external connector'; END IF;
END $$;""")
def insert(table, **values):
    q.append('INSERT INTO ' + table + '(' + ','.join(values) + ') VALUES(' + ','.join(lit(v) for v in values.values()) + ') ON CONFLICT DO NOTHING;')

names = ['Joana Almeida', 'Lucas Mendes', 'Mariana Torres', 'Pedro Ribeiro', 'Beatriz Campos', 'Rafael Gomes', 'Juliana Castro', 'André Fernandes', 'Patrícia Santos', 'Marcos Vieira', 'Fernanda Dias', 'Ricardo Alves', 'Cláudia Nunes', 'Fábio Martins', 'Adriana Lima', 'Leandro Costa', 'Simone Rodrigues', 'Alexandre Rocha', 'Cristiane Melo', 'Roberto Barros', 'Márcia Pinto', 'Sérgio Teixeira', 'Helena Freitas', 'Tiago Cardoso']
topics = [
 ('Suporte técnico', 'Wi-Fi', 'Cobertura Wi-Fi', 'O sinal no quarto está fraco. Na sala a conexão funciona.', 'Vamos comparar o sinal perto do roteador e um teste por cabo.', 'Por cabo está normal. O quarto fica depois de duas paredes.', 'Registramos a análise de cobertura e a orientação de posicionamento. Nenhuma visita real foi agendada.'),
 ('Financeiro', 'Financeiro', 'Conferência de pagamento', 'Meu pagamento demonstrativo ainda aparece em aberto.', 'Vamos registrar a conferência para o financeiro, sem consultar um ERP real.', 'O exemplo de pagamento foi feito ontem. Não há comprovante verdadeiro.', 'Registramos a conferência fictícia. Não foi emitido boleto nem alterada cobrança real.'),
 ('Comercial', 'Comercial', 'Consulta de plano', 'Gostaria de comparar planos para trabalhar em casa.', 'Qual é o uso principal e a quantidade de pessoas na residência fictícia?', 'São três pessoas, reuniões e vídeos. Quero apenas uma simulação.', 'Proposta ilustrativa registrada. Valores, cobertura e contratação não foram confirmados.'),
 ('Suporte técnico', 'Fibra', 'Quedas de conexão', 'A conexão fictícia apresentou quedas durante uma reunião.', 'Você consegue informar as luzes do equipamento e testar por cabo?', 'As luzes estão normais. Vou acompanhar por cabo e informar os horários.', 'Registramos os testes para acompanhamento técnico. Nenhum equipamento real foi acessado.'),
]
now = datetime.now(timezone.utc)
conv_ids = []
current = []
for i, name in enumerate(names):
    contact, identity = uid(f'contact:{i}'), uid(f'identity:{i}')
    insert('contacts', tenant_id=tenant, id=contact, name='DEMO · ' + name, email=f'historico-{i+1}@exemplo.invalid', notes='DADOS FICTÍCIOS. Cliente ilustrativo com três atendimentos anteriores. Sem documento, cobrança ou contrato real.', created_by_membership_id=owner, created_at=(now-timedelta(days=95)).isoformat())
    insert('contact_channel_identities', tenant_id=tenant, id=identity, contact_id=contact, connector_id=connector, external_id=f'demo-historico-20261116-{i+1}')
    for episode in range(4):
        conv = uid(f'conversation:{i}:{episode}'); conv_ids.append(conv)
        sector, tag, subject, a, b, c, d = topics[(i+episode) % len(topics)]
        sector_id = sectors[sector]
        closed = episode < 3
        if closed:
            start = now-timedelta(days=[85, 25, 7][episode], hours=i % 12)
        else:
            start = now-timedelta(minutes=24+i*6)
        stamp = lambda minutes: (start+timedelta(minutes=minutes)).isoformat()
        # Keep current occupancy below the existing capacities; six cases remain queued.
        assignee = None if not closed and i >= 18 else owner if i < 4 else agents['ana.suporte.demo@chatisp.local'] if i < 10 else agents[['bruno.suporte.demo@chatisp.local','carla.financeiro.demo@chatisp.local','daniel.comercial.demo@chatisp.local'][(i-10) % 3]]
        author = assignee or agents['ana.suporte.demo@chatisp.local']
        controller = 'human' if assignee else 'menu'
        status = 'closed' if closed else 'waiting_customer' if assignee and i % 3 == 0 else 'open'
        protocol = f'DEMO-HIST-20261116-{i+1:03}-{episode+1}'
        if not closed: current.append({'id':conv,'contact':contact,'protocol':protocol,'assignee':assignee})
        insert('conversations', tenant_id=tenant, id=conv, inbox_id=inbox, sector_id=sector_id, contact_id=contact, contact_identity_id=identity, protocol=protocol, protocol_period=start.strftime('%Y%m'), cycle_number=1, controller=controller, assignee_id=assignee, status=status, summary='DEMONSTRAÇÃO — '+subject, last_customer_message_at=stamp(14), last_eligible_human_reply_at=stamp(16), waiting_since=stamp(16) if status=='waiting_customer' else None, created_at=stamp(0), updated_at=stamp(32 if closed else 16))
        cycle=dict(tenant_id=tenant,id=uid(f'cycle:{i}:{episode}'),conversation_id=conv,cycle_number=1,opened_at=stamp(0))
        if closed: cycle.update(closed_at=stamp(32),close_reason='Demonstração: '+subject,close_result='resolved',close_summary='Exemplo fictício concluído com orientação e registro. Sem operação externa.',close_author_id=users[author],close_membership_id=author,close_author_kind='human')
        insert('conversation_cycles',**cycle)
        bodies=[a,b,c,d,'Você pode deixar essa orientação registrada no histórico?', 'Sim. Este registro é uma demonstração, sem envio para terceiros.', 'Obrigado! Vou conferir as orientações combinadas.', 'Histórico registrado. '+('Atendimento demonstrativo encerrado.' if closed else 'Seguimos acompanhando este atendimento fictício.')]
        for j,body in enumerate(bodies):
            outbound=j % 2 == 1
            insert('messages',tenant_id=tenant,id=uid(f'message:{i}:{episode}:{j}'),conversation_id=conv,connector_id=connector,direction='outbound' if outbound else 'inbound',origin='human' if outbound else 'customer',visibility='public',body=body,local_state=None if outbound else 'received',author_membership_id=author if outbound else None,trusted_at=stamp((j+1)*2),ingested_at=stamp((j+1)*2),historical=True)
        for kind, minute in [('assumed' if assignee else 'customer_message',1)]+([('closed',32)] if closed else [('wait_started',16)] if status=='waiting_customer' else []):
            insert('conversation_events',tenant_id=tenant,id=uid(f'event:{i}:{episode}:{kind}'),conversation_id=conv,cycle_number=1,kind=kind,actor_id=author if assignee else None,at=stamp(minute),version=1,controller_epoch=1,controller=controller,assignee_id=assignee)
        insert('conversation_annotations',tenant_id=tenant,conversation_id=conv,priority='urgent' if not closed and i % 5 == 0 else 'normal')
        insert('conversation_annotation_tags',tenant_id=tenant,conversation_id=conv,tag_id=demo_tag)
        insert('conversation_annotation_tags',tenant_id=tenant,conversation_id=conv,tag_id=tags[tag])
        insert('conversation_notes',tenant_id=tenant,id=uid(f'note:{i}:{episode}'),conversation_id=conv,author_id=author,request_id=f'demo-historico-{i}-{episode}',body=f'DEMONSTRAÇÃO · {subject}. Atendimento {episode+1} deste contato fictício. Consultar os outros protocolos em Histórico do contato. Sem ordens técnicas, cobrança ou integração real.',created_at=stamp(15))
        insert('assignment_queue_intervals',tenant_id=tenant,id=uid(f'queue:{i}:{episode}'),conversation_id=conv,sector_id=sector_id,started_at=stamp(0),ended_at=stamp(1) if assignee else None,historical_estimate=True)
        if not closed:
            for user in {owner_user,users[author]}:
                insert('conversation_read_markers',tenant_id=tenant,user_id=user,conversation_id=conv,unread=True)
insert('audit_logs',tenant_id=tenant,id=uid('audit'),membership_id=owner,actor_user_id=owner_user,action='demo.history.seed',target_type='inbox',target_id=inbox,result='authorized_synthetic_data',correlation_id='proprietario-20261116-historicos')
ids=','.join(lit(v) for v in conv_ids)
q.append(f"DO $$ BEGIN IF (SELECT count(*) FROM conversations WHERE tenant_id={lit(tenant)} AND id IN ({ids})) <> 96 THEN RAISE EXCEPTION 'Incomplete demo dataset'; END IF; END $$;")
q.append('COMMIT;')
psql('\n'.join(q))
manifest=args.dataset.parent/'historicos-20261116.json'
manifest.write_text(json.dumps({'tenant':tenant,'inbox':inbox,'namespace':str(namespace),'conversations':conv_ids,'current':current,'verified_at':now.isoformat()},ensure_ascii=False,indent=2)+'\n')
manifest.chmod(0o600)
print('Committed synthetic dataset. Manifest:', manifest)
