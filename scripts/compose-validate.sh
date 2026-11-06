#!/usr/bin/env bash
# Valida o pacote instalado pelo Compose em ambiente isolado: projeto próprio, volumes próprios,
# imagens com etiqueta própria, portas de loopback livres e segredos descartáveis gerados na hora.
#
# Prova, nesta ordem: build; subida com healthchecks; migrações; bootstrap do primeiro
# administrador; jornada no navegador (primeiro acesso e MFA, convite com e-mail, aceite pelo
# link, recuperação de senha, falha de entrega do SMTP, canal e conversa pelo worker, tempo real
# por WebSocket, anexo no Garage por TLS com URL assinada e isolamento entre provedores, /metrics
# fora do acesso público); despacho Meta desabilitado de forma explícita; storage parado e de
# volta; reinício de todos os serviços com persistência; encerramento sem sobras do projeto.
#
# Doubles identificados: SMTP descartável (backend/cmd/smtpsink) e o remetente do webhook da Meta
# (a própria jornada). Os demais componentes são os serviços reais do Compose. Nenhuma chamada
# externa. Não toca em outros projetos e não faz limpeza global.
#
# Requer Docker >= 20.10.10 com rede de build funcional, Compose >= 2.24.4, Node 24 com
# Playwright (web/node_modules) e Chromium já provisionado. No Docker 20.10.9 sem docker0, use
# CHATISP_VALIDATE_ALPINE=1 (bases musl, build na rede do host); isso não prova as imagens Debian.
set -euo pipefail
backup=${CHATISP_VALIDATE_BACKUP:-0}
root=$(cd "$(dirname "$0")/.." && pwd)
project="chatisp-validate-$$"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/chatisp-compose-XXXXXX")
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
port=$(free_port)
sink_port=$(free_port)
r() { openssl rand -hex 12; }
admin_email="admin@validacao.example"
admin_password="$(r)$(r)"
cat >"$tmp/env" <<ENV
POSTGRES_PASSWORD=$(r)
CHATISP_OWNER_PASSWORD=$(r)
CHATISP_APP_PASSWORD=$(r)
CHATISP_PLATFORM_PASSWORD=$(r)
CHATISP_INGRESS_PASSWORD=$(r)
CHATISP_JOBS_PASSWORD=$(r)
CHATISP_DATA_KEYS=1:$(openssl rand -base64 32)
CHATISP_DATA_KEY_VERSION=1
CHATISP_META_VERIFY_TOKEN=$(r)
CHATISP_PUBLIC_PORT=${port}
CHATISP_PANEL_ORIGINS=https://localhost:${port}
CHATISP_ATTACHMENTS_S3_PUBLIC_ENDPOINT=https://storage.localhost:${port}
CHATISP_ATTACHMENTS_S3_ACCESS_KEY=GK$(r)
CHATISP_ATTACHMENTS_S3_SECRET_KEY=$(r)$(r)$(openssl rand -hex 8)
CHATISP_SMTP_ADDR=smtp-sink:2525
CHATISP_SMTP_FROM=chat@validacao.example
# Perfil sintético limitado da prova, sem definir política comercial.
CHATISP_SUSPENDED_INGRESS_ENABLED=true
CHATISP_SUSPENDED_INGRESS_MAX_DELIVERIES=1
CHATISP_SUSPENDED_INGRESS_MAX_BYTES=1048576
ENV
if [ "$backup" = 1 ]; then
	# Valores só do laboratório: a senha do repositório é aleatória; a retenção 2 não é política.
	cat >>"$tmp/env" <<ENV
BACKUP_REPO_CIPHER_PASS=$(r)$(r)
BACKUP_RETENTION_FULL=2
ENV
	openssl rand -hex 32 >"$tmp/keys.pass"
fi
chmod 600 "$tmp/env"
runtime_image="${project}:runtime"
edge_image="${project}:edge"
sink_image="${project}:smtp-sink"
pg_image="${project}:postgres-backup"
build_extra=""
sink_network=""
if [ "${CHATISP_VALIDATE_ALPINE:-0}" = 1 ]; then
	# Docker 20.10.9: sem docker0 e com seccomp que nega clone3. Build na rede do host e
	# com base musl (deploy/validacao/Dockerfile.alpine). Não prova a imagem Debian.
	build_extra="
    build:
      context: ${root}
      dockerfile: deploy/validacao/Dockerfile.alpine
      network: host"
	sink_network="
      network: host"
fi
postgres_extra=""
pulled_images=()
if [ -n "$build_extra" ]; then
	docker image inspect postgres:18.6-alpine >/dev/null 2>&1 || pulled_images+=(postgres:18.6-alpine)
	postgres_extra="
  postgres:
    image: postgres:18.6-alpine"
fi
backup_files=()
if [ "$backup" = 1 ]; then
	backup_files=(-f "$root/deploy/backup/compose.backup.yaml")
	if [ -n "$build_extra" ]; then
		# Docker 20.10.9: PostgreSQL 18.6 Alpine com pgBackRest, build na rede do host.
		postgres_extra="
  postgres:
    image: ${pg_image}
    build:
      context: ${root}/deploy/backup
      dockerfile: Dockerfile.postgres.alpine
      network: host"
	else
		postgres_extra="
  postgres:
    image: ${pg_image}"
	fi
fi
cat >"$tmp/override.yaml" <<YAML
services:${postgres_extra}
  chatisp-migrate:
    image: ${runtime_image}${build_extra:+${build_extra}
      target: runtime}
  chatisp-api:
    image: ${runtime_image}${build_extra:+${build_extra}
      target: runtime}
  chatisp-worker:
    image: ${runtime_image}${build_extra:+${build_extra}
      target: runtime}
  storage-setup:
    image: ${runtime_image}
  caddy:
    image: ${edge_image}${build_extra:+${build_extra}
      target: edge}
  trust-export:
    image: ${edge_image}
  smtp-sink:
    image: ${sink_image}
    build:
      context: ${root}
      dockerfile: deploy/validacao/Dockerfile.smtpsink${sink_network}
    ports:
      - "127.0.0.1:${sink_port}:8025"
    restart: "no"
YAML
# Sub-rede própria: hosts cujo default-address-pools já está esgotado não criam a rede padrão.
# CHATISP_VALIDATE_SUBNET escolhe uma; sem ela, a primeira 172.31.N.0/24 sem rota nem rede Docker.
subnet=${CHATISP_VALIDATE_SUBNET:-$(python3 - <<'PY'
import ipaddress, json, subprocess
used = []
for line in subprocess.run(["ip", "-4", "route"], capture_output=True, text=True).stdout.splitlines():
    first = line.split()[0] if line.split() else ""
    try:
        used.append(ipaddress.ip_network(first, strict=False))
    except ValueError:
        pass
ids = subprocess.run(["docker", "network", "ls", "-q"], capture_output=True, text=True).stdout.split()
if ids:
    out = subprocess.run(["docker", "network", "inspect", *ids], capture_output=True, text=True).stdout
    for net in json.loads(out or "[]"):
        for cfg in (net.get("IPAM") or {}).get("Config") or []:
            if cfg.get("Subnet"):
                used.append(ipaddress.ip_network(cfg["Subnet"], strict=False))
for n in range(240, 255):
    cand = ipaddress.ip_network(f"172.31.{n}.0/24")
    if not any(cand.overlaps(u) for u in used):
        print(cand)
        break
PY
)}
if [ -z "$subnet" ]; then
	echo "nenhuma sub-rede livre; defina CHATISP_VALIDATE_SUBNET" >&2
	exit 1
fi
cat >>"$tmp/override.yaml" <<YAML
networks:
  default:
    ipam:
      config:
        - subnet: ${subnet}
YAML
compose=(docker compose -p "$project" -f "$root/deploy/compose.yaml" "${backup_files[@]}" -f "$tmp/override.yaml" --env-file "$tmp/env")
base="https://localhost:${port}"
storage_base="https://storage.localhost:${port}"
sink="http://127.0.0.1:${sink_port}"

cleanup() {
	local status=$?
	if [ "$status" -ne 0 ]; then
		echo "falha ($status); logs do projeto ${project}:" >&2
		"${compose[@]}" ps -a >&2 || true
		# Erros de jobs River (o log os omite): texto gravado na própria linha do job.
		"${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -At -c "select kind, state, left(errors::text, 700) from river.river_job where state <> 'completed' order by id limit 6" >&2 2>/dev/null || true
		"${compose[@]}" logs --no-color --tail 60 >&2 || true
	fi
	if [ "$status" -ne 0 ] && [ "${CHATISP_VALIDATE_KEEP_ON_FAILURE:-0}" = 1 ]; then
		echo "diagnóstico preservado por opção explícita: projeto=$project diretório=$tmp; cleanup obrigatório após investigar" >&2
		return
	fi
	"${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
	docker image rm "$runtime_image" "$edge_image" "$sink_image" "$pg_image" >/dev/null 2>&1 || true
	# Só as imagens que este script baixou (ausentes antes da execução).
	if [ "${#pulled_images[@]}" -gt 0 ]; then
		docker image rm "${pulled_images[@]}" >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT

curl_ca() { curl -fsS --cacert "$tmp/ca.crt" --resolve "localhost:${port}:127.0.0.1" --resolve "storage.localhost:${port}:127.0.0.1" "$@"; }

wait_ready() {
	for _ in $(seq 1 60); do
		if curl_ca "$base/readyz" >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	echo "readyz não respondeu em $base" >&2
	return 1
}

fetch_ca() {
	"${compose[@]}" cp caddy:/data/caddy/pki/authorities/local/root.crt "$tmp/ca.crt" >/dev/null
}

# Fixa o navegador no certificado deste laboratório (SPKI do certificado servido), sem desligar a
# verificação de TLS: qualquer outro certificado continua recusado.
spki_pins() {
	local host pins=()
	for host in localhost storage.localhost; do
		pins+=("$(openssl s_client -connect "127.0.0.1:${port}" -servername "$host" </dev/null 2>/dev/null \
			| openssl x509 -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | base64)")
	done
	(IFS=,; echo "${pins[*]}")
}

journey() {
	local phase=$1
	(
		cd "$root/web"
		# shellcheck disable=SC1091
		. "$root/scripts/lib.sh"
		use_npm11
		# TMPDIR curto: o Chromium cria um socket Unix no perfil temporário (limite de 108 caracteres).
		JOURNEY_PHASE="$phase" JOURNEY_BASE="$base" JOURNEY_STORAGE_BASE="$storage_base" JOURNEY_SINK="$sink" \
			JOURNEY_SPKI="$(spki_pins)" JOURNEY_CA_FILE="$tmp/ca.crt" JOURNEY_STATE="$tmp/journey.json" \
			JOURNEY_ADMIN_EMAIL="$admin_email" JOURNEY_ADMIN_PASSWORD="$admin_password" \
			TMPDIR=/tmp UV_THREADPOOL_SIZE=1 taskset -c 0,1 node scripts/compose-journey.mjs
	)
}

"${compose[@]}" config -q
# Uma construção por etiqueta: api, worker e storage-setup reutilizam a imagem do migrate.
build_services=(chatisp-migrate caddy smtp-sink)
[ "$backup" = 1 ] && build_services+=(postgres)
"${compose[@]}" build "${build_services[@]}"
"${compose[@]}" up -d --no-build --wait --wait-timeout 300
fetch_ca
wait_ready
curl_ca "$base/healthz" >/dev/null
curl_ca "$base/entrar" | grep -qi '<div id="root"'
echo "subida ok: postgres, garage (bucket e chave provisionados), migrações, api, worker, caddy e SMTP descartável ($base)"

# Provisionamento idempotente do storage: uma segunda execução não muda nada.
"${compose[@]}" run --rm -T --no-deps storage-setup | tee "$tmp/storage-setup.log"
grep -q "bucket já existe" "$tmp/storage-setup.log"
grep -q "chave já existe" "$tmp/storage-setup.log"
echo "storage-setup idempotente: segunda execução só conferiu o que já existia"

printf '%s\n' "$admin_password" | "${compose[@]}" run --rm -T --no-deps chatisp-migrate \
	bootstrap -email "$admin_email" -superadmin -storage-bytes 10485760

journey before

# Despacho Meta desabilitado: a tentativa de envio com anexo falha antes da rede, com código claro.
code=""
for _ in $(seq 1 60); do
	code=$("${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -Atc "select error_code from outbound_attempts where error_code is not null order by created_at desc limit 1" 2>/dev/null || true)
	[ -n "$code" ] && break
	sleep 1
done
if [ "$code" != "driver_indisponivel" ]; then
	echo "tentativa de envio terminou com '${code}', esperado driver_indisponivel" >&2
	exit 1
fi
echo "envio Meta desabilitado: tentativa falhou com driver_indisponivel, sem chamada externa"

journey suspension
tenant_b=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tenantB"])' "$tmp/journey.json")
for _ in $(seq 1 30); do
	held=$("${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -Atc "select count(*) from webhook_items where tenant_id='${tenant_b}' and state='quarantined' and payload->>'reason'='tenant_suspended'")
	[ "$held" = 1 ] && break
	sleep 1
done
[ "$held" = 1 ]
[ "$("${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -Atc "select count(*) from suspended_ingress_holds where tenant_id='${tenant_b}'")" = 1 ]
[ "$("${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -Atc "select rejected_count || ':' || admission_blocked from suspended_ingress_alerts where tenant_id='${tenant_b}'")" = '1:true' ]
[ "$("${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -Atc "select count(*) from river.river_job where queue='inbound' and args->>'tenant_id'='${tenant_b}' and args->>'item_id' in (select id::text from webhook_items where tenant_id='${tenant_b}' and payload->>'reason'='tenant_suspended')")" = 0 ]
echo "suspensão: um envelope retido, segundo recusado e alerta persistido; reativação mantém quarentena sem job de atendimento"

# Métricas: coletáveis na rede interna do Compose, ausentes do acesso público e das portas publicadas.
"${compose[@]}" exec -T chatisp-worker curl -fsS http://127.0.0.1:9090/metrics | grep '^# HELP' >/dev/null
"${compose[@]}" exec -T chatisp-api curl -fsS http://127.0.0.1:9090/metrics | grep '^# HELP' >/dev/null
api_container=$("${compose[@]}" ps -q chatisp-api)
if docker inspect "$api_container" --format '{{json .HostConfig.PortBindings}}' | python3 -c 'import json,sys; p=json.load(sys.stdin) or {}; sys.exit(0 if p.get("9090/tcp") else 1)'; then
	echo "porta de métricas publicada" >&2
	exit 1
fi
published=$(docker ps --filter "label=com.docker.compose.project=${project}" --format '{{.Ports}}' | tr ',' '\n' | grep -- '->' | sed 's/^ *//' | sort -u)
if echo "$published" | grep -v -E "^127\.0\.0\.1:(${port}->443|${sink_port}->8025)/tcp$" | grep -q .; then
	echo "portas publicadas além das esperadas:" >&2
	echo "$published" >&2
	exit 1
fi
echo "métricas ok: coletáveis dentro da rede do Compose, fora do acesso público; portas publicadas: loopback ${port} (HTTPS) e ${sink_port} (SMTP descartável)"

# Storage parado e de volta.
"${compose[@]}" stop garage >/dev/null
journey storage-down
"${compose[@]}" up -d --wait --wait-timeout 120 garage >/dev/null
journey storage-up

# Reinício de todos os serviços com persistência.
"${compose[@]}" restart >/dev/null
"${compose[@]}" up -d --no-build --wait --wait-timeout 300
wait_ready
journey after
printf '%s\n' "$admin_password" | "${compose[@]}" run --rm -T --no-deps chatisp-migrate \
	bootstrap -email "$admin_email" | grep -q 'nada foi alterado'
echo "reinício ok: dados, anexo, sessões de login e fluxos voltaram; o bootstrap não recria nada"

if [ "$backup" = 1 ]; then
	export COMPOSE_CMD="${compose[*]}" BACKUP_KEYS_PASSPHRASE_FILE="$tmp/keys.pass" BACKUP_STATUS_FILE="$tmp/backup-status.prom" BACKUP_INSTALLATION="$project"
	bk="$root/deploy/backup/chatisp-backup.sh"
	state_get() { python3 -c "import json,sys;d=json.load(open('$tmp/journey.json'));print(eval(sys.argv[1]))" "$1"; }
	excluded_id=$(state_get "d['operation']['attachment']")
	kept_id=$(state_get "d['operation']['keep']['attachment']")
	sqlq() { "${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -v ON_ERROR_STOP=1 -At -c "$1"; }

	"$bk" init
	if BACKUP_MAX_AGE_SECONDS=3600 "$bk" check 2>/dev/null; then
		echo "o monitor de atraso aceitou a ausência de backup" >&2
		exit 1
	fi
	echo "backup atrasado é sinalizado: sem nenhum sucesso registrado, 'check' falha"
	"$bk" backup full
	"$bk" info | tee "$tmp/pgbackrest-info.txt"
	grep -q 'full backup' "$tmp/pgbackrest-info.txt"
	grep -q 'wal archive min/max' "$tmp/pgbackrest-info.txt"
	BACKUP_MAX_AGE_SECONDS=3600 "$bk" check
	# Cada componente é obrigatório independentemente dos demais e da instalação vizinha.
	cp "$BACKUP_STATUS_FILE" "$tmp/status.complete"
	for component in postgres attachments keys ledger; do
		awk -v kind="kind=\"$component\"" 'index($0,kind)==0' "$tmp/status.complete" >"$BACKUP_STATUS_FILE"
		if BACKUP_MAX_AGE_SECONDS=3600 "$bk" check >"$tmp/check-missing.txt" 2>&1; then
			echo "monitor aceitou ausência de $component" >&2; exit 1
		fi
		grep -q "$component" "$tmp/check-missing.txt"
	done
	cp "$tmp/status.complete" "$BACKUP_STATUS_FILE"
	echo "monitor: ausência de cada um dos quatro componentes é detectada separadamente"
	"${compose[@]}" run --rm -T --no-deps -u 0 --entrypoint tar chatisp-migrate -cf - -C /mirror ledgers >"$tmp/ledgers.tar"
	mkdir "$tmp/artifact-proof"
	python3 "$root/deploy/backup/validate-artifacts.py" extract-ledgers "$tmp/ledgers.tar" "$tmp/artifact-proof"
	python3 "$root/deploy/backup/validate-artifacts.py" verify-ledgers "$tmp/artifact-proof/ledgers"
	ledger_copy=$(find "$tmp/artifact-proof/ledgers" -name 'deletions-*.tsv' -print -quit)
	printf 'corrupcao sintetica\n' >>"$ledger_copy"
	if python3 "$root/deploy/backup/validate-artifacts.py" verify-ledgers "$tmp/artifact-proof/ledgers" >"$tmp/corrupt-ledger.txt" 2>&1; then
		echo "ledger corrompido aceito" >&2; exit 1
	fi
	grep -q corrompido "$tmp/corrupt-ledger.txt"
	rm "$ledger_copy.sha256"
	if python3 "$root/deploy/backup/validate-artifacts.py" verify-ledgers "$tmp/artifact-proof/ledgers" >"$tmp/incomplete-ledger.txt" 2>&1; then
		echo "ledger incompleto aceito" >&2; exit 1
	fi
	grep -q ausente "$tmp/incomplete-ledger.txt"
	echo "artefatos reais: ledger íntegro aceito; corrupção e checksum ausente recusados pelo validador usado no restore"
	excluded_key=$(sqlq "select object_key from attachments where id = '${excluded_id}'")
	kept_key=$(sqlq "select object_key from attachments where id = '${kept_id}'")
	test -n "$excluded_key" && test -n "$kept_key"
	echo "backup completo: banco (pgBackRest com WAL), anexos (espelho), livro de exclusões, chaves cifradas e sinal de sucesso"

	# Exclusão de privacidade DEPOIS do backup; o livro externo é exportado em seguida.
	journey exclude
	"$bk" ledger
	[ "$(sqlq "select count(*) from deletion_records where subject = 'attachment' and subject_id = '${excluded_id}'")" = 1 ]
	sleep 2
	if BACKUP_MAX_AGE_SECONDS=1 "$bk" check 2>/dev/null; then
		echo "o monitor de atraso não detectou um backup velho" >&2
		exit 1
	fi
	echo "backup atrasado é sinalizado: sucesso mais velho que o limite faz 'check' falhar"

	# Desastre: perde banco e storage (volumes de dados); o repositório e o espelho sobrevivem.
	keys_value=$(sed -n 's/^CHATISP_DATA_KEYS=//p' "$tmp/env")
	keys_sha=$(printf 'CHATISP_DATA_KEYS=%s
CHATISP_DATA_KEY_VERSION=%s
' "$keys_value" 1 | sha256sum | cut -d' ' -f1)
	unset keys_value
	"${compose[@]}" down --remove-orphans >/dev/null
	docker volume rm "${project}_postgres-data" "${project}_garage-config" "${project}_garage-meta" "${project}_garage-data" >/dev/null
	# Perda real das chaves locais: só a cópia cifrada do destino pode recuperá-las.
	sed -i '/^CHATISP_DATA_KEYS=/d; /^CHATISP_DATA_KEY_VERSION=/d' "$tmp/env"
	unset CHATISP_DATA_KEYS CHATISP_DATA_KEY_VERSION
	# Um anel divergente precisa impedir a reabertura antes de tocar no banco perdido.
	if CHATISP_DATA_KEYS="1:$(openssl rand -base64 32)" CHATISP_DATA_KEY_VERSION=1 RESTORE_KEYS_OUT="$tmp/rejected.env" "$root/deploy/backup/chatisp-restore.sh" >"$tmp/key-divergence.txt" 2>&1; then
		echo "restore aceitou anel divergente" >&2; exit 1
	fi
	grep -q 'diferem' "$tmp/key-divergence.txt"
	if [ -n "$("${compose[@]}" ps -q --status running chatisp-api chatisp-worker 2>/dev/null)" ]; then
		echo "aplicação reabriu após falha de chaves" >&2; exit 1
	fi
	echo "restore recusa chaves divergentes e mantém atendimento fechado"
	RESTORE_KEYS_OUT="$tmp/recovered.env" ENV_EXPECTED_KEYS_SHA256="$keys_sha" "$root/deploy/backup/chatisp-restore.sh"
	wait_ready
	journey restored
	[ "$(sqlq "select count(*) from attachments where id = '${excluded_id}'")" = 0 ]
	[ "$(sqlq "select count(*) from attachments where id = '${kept_id}'")" = 1 ]
	[ "$(sqlq "select count(*) from deletion_records where subject = 'attachment' and subject_id = '${excluded_id}'")" = 1 ]
	excluded_contact=$(state_get "d['operation']['contact']")
	[ "$(sqlq "select count(*) from contacts where id='${excluded_contact}' and name='titular excluído' and privacy_erased_at is not null and phone is null")" = 1 ]
	[ "$(sqlq "select count(*) from messages m join conversations c on c.tenant_id=m.tenant_id and c.id=m.conversation_id where c.contact_id='${excluded_contact}' and m.body<>''")" = 0 ]
	echo "privacidade após restore: titular permanece anonimizado e corpos não ressuscitam"
	"${compose[@]}" run --rm -T --no-deps --entrypoint sh chatisp-migrate -c 'chatisp attachments-backup -dest /tmp/verify >/dev/null && cut -f1 /tmp/verify/objects.manifest' >"$tmp/restored-keys.txt"
	grep -q "^${kept_key}\$" "$tmp/restored-keys.txt"
	if grep -q "^${excluded_key}\$" "$tmp/restored-keys.txt"; then
		echo "o objeto excluído voltou ao storage restaurado" >&2
		exit 1
	fi
	echo "restauração ok: banco (ponto consistente do backup) + exclusões reaplicadas; anexo mantido íntegro no storage novo; anexo excluído ausente da base e do storage, exclusão preservada no livro; chaves recuperadas de cópia cifrada e aplicadas ao ambiente perdido; webhook assinado aceito"
fi

"${compose[@]}" down -v --remove-orphans
docker image rm "$runtime_image" "$edge_image" "$sink_image" >/dev/null
[ "$backup" = 1 ] && docker image rm "$pg_image" >/dev/null
left=$(docker ps -aq --filter "label=com.docker.compose.project=${project}"; docker volume ls -q --filter "label=com.docker.compose.project=${project}"; docker network ls -q --filter "label=com.docker.compose.project=${project}")
if [ -n "$left" ]; then
	echo "sobraram recursos do projeto ${project}:" >&2
	echo "$left" >&2
	exit 1
fi
echo "compose ok: build, healthchecks, migrações, jornada integrada, storage, SMTP, métricas privadas, reinício e encerramento sem sobras ($project)"
