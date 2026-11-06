#!/usr/bin/env bash
# Operação de backup do Chat ISP (instalação por Compose). Subcomandos:
#   init                     cria o stanza do pgBackRest e confere o arquivamento de WAL (uma vez)
#   backup [full|diff|incr]  pgBackRest (banco + WAL) e espelho de anexos, livro de exclusões,
#                            cópia das chaves e sinal de sucesso. Padrão: incr.
#   full | diff | incr       atalhos equivalentes a "backup full|diff|incr" (usados por systemd @%i)
#   ledger                   só livro de exclusões + mapa de anexos (barato; rode com mais frequência
#                            que o backup completo, porque é ele que reaplica exclusões na restauração)
#   check                    falha (saída 1) se algum sucesso estiver mais velho que BACKUP_MAX_AGE_SECONDS
#   info                     estado do repositório (pgbackrest info)
#
# Variáveis: COMPOSE_CMD (comando docker compose completo, incluindo -f deploy/compose.yaml
# -f deploy/backup/compose.backup.yaml --env-file deploy/.env), BACKUP_KEYS_PASSPHRASE_FILE (arquivo
# com a frase que cifra a cópia das chaves; guarde-o FORA do servidor), BACKUP_STATUS_FILE (arquivo
# de texto Prometheus, p.ex. no diretório do textfile collector), BACKUP_MAX_AGE_SECONDS (para
# "check"; sem valor padrão: é a defasagem máxima tolerada pela operação, D2-07),
# BACKUP_INSTALLATION (identificador único da instalação nas métricas; obrigatório). Nada é apagado no destino.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=common.sh
. "$here/common.sh"
backup_setup
trap backup_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
stanza=chatisp
now() { date -u +%s; }
stamp() {
	printf '%s-%s\n' "$(date -u +%Y%m%dT%H%M%S.%NZ)" "$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
}

record_success() { # tipo
	local kind=$1 ts
	ts=$(now)
	if [ -n "${BACKUP_STATUS_FILE:-}" ]; then
		local dir tmp
		dir=$(dirname "$BACKUP_STATUS_FILE")
		mkdir -p "$dir"
		[ ! -L "$BACKUP_STATUS_FILE" ] && [ ! -L "$BACKUP_STATUS_FILE.lock" ] || { echo "status de backup com symlink recusado" >&2; return 1; }
		exec 9>"$BACKUP_STATUS_FILE.lock"
		flock -x 9
		tmp=$(mktemp "$dir/.backup-status.XXXXXX")
		{
			if [ -f "$BACKUP_STATUS_FILE" ]; then
				awk -v label="chatisp_backup_last_success_timestamp_seconds{installation=\"$installation\",kind=\"$kind\"}" '$1 != label && $0 !~ /^#/' "$BACKUP_STATUS_FILE"
			fi
			echo "chatisp_backup_last_success_timestamp_seconds{installation=\"$installation\",kind=\"$kind\"} $ts"
		} | sort -u | {
			echo '# HELP chatisp_backup_last_success_timestamp_seconds Unix time of the last successful backup step.'
			echo '# TYPE chatisp_backup_last_success_timestamp_seconds gauge'
			cat
		} >"$tmp"
		mv "$tmp" "$BACKUP_STATUS_FILE"
		flock -u 9
		exec 9>&-
	fi
	# Cópia do sinal junto ao destino, para auditoria independente do servidor.
	printf '%s %s %s\n' "$installation" "$kind" "$ts" | in_mirror --entrypoint sh chatisp-migrate -c "set -eu; [ ! -L $mirror/status ] && [ ! -L $mirror/status/successes.log ]; mkdir -p $mirror/status; cat >> $mirror/status/successes.log"
}

export_ledger() {
	local ts
	ts=$(stamp)
	# SQL must finish successfully before a complete artifact can be published.
	sql -c "\\copy (select id, tenant_id, object_key, coalesce(object_version, ''), expected_bytes from attachments where object_key <> '' union select subject_id, tenant_id, object_key, coalesce(object_version, ''), object_bytes from deletion_records d where subject = 'attachment' and object_key is not null and not exists (select 1 from attachments a where a.tenant_id = d.tenant_id and a.id = d.subject_id) order by id) to stdout" >"$work_tmp/attachments.rows"
	{ echo '# chatisp-attachments-ledger-1'; cat "$work_tmp/attachments.rows"; echo '# end-chatisp-attachments-ledger-1'; } >"$work_tmp/attachments.tsv"
	publish_artifact "$work_tmp/attachments.tsv" "ledgers/attachments-$ts.tsv"
	sql -c "\\copy (select tenant_id, subject, subject_id from deletion_records union select tenant_id, 'contact_privacy', target_id from retention_deletion_records where class = 'content' and target_type = 'contact' and target_id is not null order by tenant_id, subject, subject_id) to stdout" >"$work_tmp/deletions.rows"
	{ echo '# chatisp-deletions-ledger-1'; cat "$work_tmp/deletions.rows"; echo '# end-chatisp-deletions-ledger-1'; } >"$work_tmp/deletions.tsv"
	publish_artifact "$work_tmp/deletions.tsv" "ledgers/deletions-$ts.tsv"
	echo "ledger: mapa e exclusões completos, com SHA-256 ($ts)"
}

backup_keys() {
	local pass=${BACKUP_KEYS_PASSPHRASE_FILE:-} ts
	[ -n "$pass" ] && [ -r "$pass" ] || { echo "defina BACKUP_KEYS_PASSPHRASE_FILE (arquivo legível com a frase das chaves)" >&2; return 1; }
	ts=$(stamp)
	"${compose[@]}" run --rm -T --no-deps --entrypoint sh chatisp-migrate -c 'test -n "$CHATISP_DATA_KEYS" && test -n "$CHATISP_DATA_KEY_VERSION" && printf "CHATISP_DATA_KEYS=%s\nCHATISP_DATA_KEY_VERSION=%s\n" "$CHATISP_DATA_KEYS" "$CHATISP_DATA_KEY_VERSION"' \
		| openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass "file:$pass" >"$work_tmp/keys.enc"
	publish_artifact "$work_tmp/keys.enc" "keys/data-keys-$ts.enc"
	echo "chaves: cópia cifrada e SHA-256 gravados ($ts)"
}

case "${1:-}" in
backup | full | diff | incr | ledger | init) acquire_operation_lock ;;
esac

case "${1:-}" in
backup | full | diff | incr)
	if [ "$1" = "backup" ]; then
		type=${2:-incr}
	else
		type=$1
	fi
	case "$type" in full | diff | incr) ;; *) echo "tipo inválido: $type" >&2; exit 2 ;; esac
	"${compose[@]}" exec -T -u postgres postgres pgbackrest --stanza="$stanza" --type="$type" backup
	"${compose[@]}" exec -T -u postgres postgres pgbackrest --stanza="$stanza" check
	record_success postgres
	in_mirror chatisp-migrate attachments-backup -dest "$mirror"
	record_success attachments
	export_ledger
	record_success ledger
	backup_keys
	record_success keys
	echo "backup $type concluído"
	;;
ledger)
	export_ledger
	record_success ledger
	;;
init)
	"${compose[@]}" exec -T -u postgres postgres pgbackrest --stanza="$stanza" stanza-create
	sql -c "select pg_switch_wal()" >/dev/null
	"${compose[@]}" exec -T -u postgres postgres pgbackrest --stanza="$stanza" check
	echo "stanza $stanza criado; arquivamento de WAL conferido"
	;;
info)
	"${compose[@]}" exec -T -u postgres postgres pgbackrest --stanza="$stanza" info
	;;
check)
	: "${BACKUP_STATUS_FILE:?defina BACKUP_STATUS_FILE}"
	: "${BACKUP_MAX_AGE_SECONDS:?defina BACKUP_MAX_AGE_SECONDS (defasagem máxima tolerada, D2-07; não há padrão)}"
	[[ "$BACKUP_MAX_AGE_SECONDS" =~ ^[1-9][0-9]*$ ]] && [[ "${BACKUP_MAX_LEDGER_AGE_SECONDS:-$BACKUP_MAX_AGE_SECONDS}" =~ ^[1-9][0-9]*$ ]] || { echo "limites de atraso devem ser inteiros positivos" >&2; exit 2; }
	[ -f "$BACKUP_STATUS_FILE" ] || { echo "backup atrasado: nenhum sucesso registrado" >&2; exit 1; }
	late=0
	for kind in postgres attachments keys ledger; do
		last=$(awk -v label="chatisp_backup_last_success_timestamp_seconds{installation=\"$installation\",kind=\"$kind\"}" '$1 == label {print $2}' "$BACKUP_STATUS_FILE" | tail -1)
		limit=$BACKUP_MAX_AGE_SECONDS
		[ "$kind" = ledger ] && limit=${BACKUP_MAX_LEDGER_AGE_SECONDS:-$limit}
		if [[ ! "$last" =~ ^[0-9]+$ ]] || [ "$last" -gt "$(now)" ] || [ $(($(now) - last)) -gt "$limit" ]; then
			echo "backup atrasado ($installation): $kind (último sucesso: ${last:-nunca})" >&2
			late=1
		fi
	done
	[ "$late" = 0 ] && echo "backups ($installation) dentro do limite de ${BACKUP_MAX_AGE_SECONDS}s"
	exit "$late"
	;;
*)
	sed -n '2,18p' "$0" >&2
	exit 2
	;;
esac
