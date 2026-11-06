#!/usr/bin/env bash
# Restauração de desastre do Chat ISP (instalação por Compose) a partir do destino de backup:
# chaves (cópia cifrada), banco (pgBackRest), reaplicação das exclusões e anexos (espelho),
# nesta ordem, ANTES de reabrir o serviço ao atendimento.
#
# Pré-condições: serviços parados (docker compose down, sem -v dos volumes de backup), volumes de
# dados vazios (postgres-data, garage-*), backup-repo e backup-mirror intactos, deploy/.env com as
# senhas do repositório (BACKUP_REPO_CIPHER_PASS) e dos papéis do banco. As CHAVES de dados
# (CHATISP_DATA_KEYS e CHATISP_DATA_KEY_VERSION) são recuperadas da cópia cifrada para
# RESTORE_KEYS_OUT e aplicadas automaticamente ao arquivo de ambiente (--env-file / RESTORE_ENV_FILE)
# caso tenham sido perdidas; se o ambiente já contiver chaves divergentes, a restauração falha.
#
# Variáveis: COMPOSE_CMD (como no backup), BACKUP_KEYS_PASSPHRASE_FILE, RESTORE_KEYS_OUT (arquivo
# 0600 de saída das chaves), RESTORE_ENV_FILE (opcional: caminho do .env; por padrão extraído de
# --env-file em COMPOSE_CMD), RESTORE_ARGS (argumentos extras do "pgbackrest restore", p.ex.
# --type=time --target=...; padrão: --type=immediate, o ponto consistente do backup, o que obriga o
# livro de exclusões externo a reaplicar o que veio depois), ENV_EXPECTED_KEYS_SHA256 (opcional:
# confere o resumo SHA-256 do arquivo de chaves recuperado).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=common.sh
. "$here/common.sh"
backup_setup
: "${BACKUP_KEYS_PASSPHRASE_FILE:?defina BACKUP_KEYS_PASSPHRASE_FILE}"
: "${RESTORE_KEYS_OUT:?defina RESTORE_KEYS_OUT}"
validate_keys_passphrase
restore_complete=0
restore_started=0
restore_cleanup() {
	local status=$?
	if [ "$restore_started" = 1 ] && [ "$restore_complete" != 1 ]; then
		# A failed recovery must not leave either process serving partially recovered data.
		"${compose[@]}" rm -sf chatisp-api chatisp-worker >/dev/null || status=1
	fi
	if [ "$operation_locked" = 1 ]; then
		in_mirror --entrypoint rmdir chatisp-migrate /mirror/.operation-lock >/dev/null || status=1
	fi
	rm -rf "$work_tmp"
	exit "$status"
}
trap restore_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
restore_args=${RESTORE_ARGS:---type=immediate --target-action=promote}
uuid_re='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'

env_file=${RESTORE_ENV_FILE:-}
if [ -z "$env_file" ]; then
	for ((i = 0; i < ${#compose[@]}; i++)); do
		if [ "${compose[$i]}" = "--env-file" ] && [ $((i + 1)) -lt ${#compose[@]} ]; then
			env_file=${compose[$((i + 1))]}
			break
		fi
		case "${compose[$i]}" in
		--env-file=*)
			env_file=${compose[$i]#--env-file=}
			break
			;;
		esac
	done
fi

[ -n "$env_file" ] && [ -f "$env_file" ] && [ ! -L "$env_file" ] || { echo "RESTORE_ENV_FILE ou --env-file deve indicar arquivo regular para persistir as chaves recuperadas" >&2; exit 1; }
[ "$RESTORE_KEYS_OUT" != "$env_file" ] && [ ! -L "$RESTORE_KEYS_OUT" ] || { echo "RESTORE_KEYS_OUT deve ser distinto do ambiente e sem symlink" >&2; exit 1; }
orig_proc_keys=${CHATISP_DATA_KEYS:-}
orig_proc_ver=${CHATISP_DATA_KEY_VERSION:-}
orig_file_keys=""
orig_file_ver=""
if [ -n "$env_file" ] && [ -f "$env_file" ]; then
	orig_file_keys=$(sed -n 's/^CHATISP_DATA_KEYS=//p' "$env_file" | tail -1)
	orig_file_ver=$(sed -n 's/^CHATISP_DATA_KEY_VERSION=//p' "$env_file" | tail -1)
fi
if [ -n "$orig_proc_keys" ] && [ -n "$orig_file_keys" ] && [ "$orig_proc_keys" != "$orig_file_keys" ]; then
	echo "CHATISP_DATA_KEYS no ambiente do processo difere de $env_file" >&2
	exit 1
fi
if [ -n "$orig_proc_ver" ] && [ -n "$orig_file_ver" ] && [ "$orig_proc_ver" != "$orig_file_ver" ]; then
	echo "CHATISP_DATA_KEY_VERSION no ambiente do processo difere de $env_file" >&2
	exit 1
fi
orig_keys=${orig_proc_keys:-$orig_file_keys}
orig_ver=${orig_proc_ver:-$orig_file_ver}

# Placeholders are used only for shell helpers before recovery, never for the application.
export CHATISP_DATA_KEYS="${orig_keys:-__restore_bootstrap__}"
export CHATISP_DATA_KEY_VERSION="${orig_ver:-1}"
acquire_operation_lock
restore_started=1
# Failure to close the application aborts recovery; no ignored daemon/permission error.
"${compose[@]}" rm -sf chatisp-api chatisp-worker >/dev/null

echo "1/7 chaves: recuperando e aplicando a cópia cifrada mais recente"
latest_keys=$(
	CHATISP_DATA_KEYS="${orig_keys:-__restore_bootstrap__}" CHATISP_DATA_KEY_VERSION="${orig_ver:-1}" \
		in_mirror --entrypoint sh chatisp-migrate -c '
			set -eu
			[ ! -L /mirror/keys ]
			set -- /mirror/keys/data-keys-*.enc
			[ -f "$1" ] || exit 1
			for file in /mirror/keys/* /mirror/keys/.[!.]*; do
				[ -e "$file" ] || [ -L "$file" ] || continue
				[ ! -L "$file" ] && [ -f "$file" ] || exit 1
				case "$file" in
				*.enc) [ -f "$file.sha256" ] || exit 1 ;;
				*.enc.sha256) [ -f "${file%.sha256}" ] || exit 1 ;;
				*) echo "cópia de chaves parcial ou inesperada" >&2; exit 1 ;;
				esac
			done
			ls -1 /mirror/keys/data-keys-*.enc | sort | tail -1
		'
) || { echo "nenhuma cópia de chaves no destino" >&2; exit 1; }
[ -n "$latest_keys" ] || { echo "nenhuma cópia de chaves no destino" >&2; exit 1; }
umask 077
CHATISP_DATA_KEYS="${orig_keys:-__restore_bootstrap__}" CHATISP_DATA_KEY_VERSION="${orig_ver:-1}" \
	in_mirror --entrypoint sh chatisp-migrate -c '
		set -eu
		file=$1
		[ ! -L /mirror/keys ] && [ ! -L "$file" ] && [ ! -L "$file.sha256" ]
		[ -f "$file.sha256" ] || { echo "checksum das chaves ausente" >&2; exit 1; }
		expected=$(cat "$file.sha256"); actual=$(sha256sum < "$file"); actual=${actual%% *}
		[ "$expected" = "$actual" ] || { echo "cópia cifrada de chaves corrompida" >&2; exit 1; }
		cat "$file"
	' keys "$latest_keys" \
	| openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -pass "file:$BACKUP_KEYS_PASSPHRASE_FILE" >"$work_tmp/recovered.env"

rec_keys=$(sed -n 's/^CHATISP_DATA_KEYS=//p' "$work_tmp/recovered.env" | tail -1)
rec_ver=$(sed -n 's/^CHATISP_DATA_KEY_VERSION=//p' "$work_tmp/recovered.env" | tail -1)
python3 "$here/validate-artifacts.py" verify-keys "$work_tmp/recovered.env"
if [ -n "${ENV_EXPECTED_KEYS_SHA256:-}" ] && [ "$(sha256sum <"$work_tmp/recovered.env" | cut -d' ' -f1)" != "$ENV_EXPECTED_KEYS_SHA256" ]; then
	echo "as chaves recuperadas diferem do resumo esperado (ENV_EXPECTED_KEYS_SHA256)" >&2
	exit 1
fi
if [ -n "$orig_keys" ] && [ "$orig_keys" != "$rec_keys" ]; then
	echo "as chaves recuperadas diferem das chaves presentes no ambiente" >&2
	exit 1
fi
if [ -n "$orig_ver" ] && [ "$orig_ver" != "$rec_ver" ]; then
	echo "a versão ativa da chave recuperada difere da presente no ambiente" >&2
	exit 1
fi
if [ -n "$env_file" ] && [ -f "$env_file" ]; then
	{
		awk '!/^CHATISP_DATA_KEYS=/ && !/^CHATISP_DATA_KEY_VERSION=/' "$env_file"
		printf 'CHATISP_DATA_KEYS=%s\nCHATISP_DATA_KEY_VERSION=%s\n' "$rec_keys" "$rec_ver"
	} >"$work_tmp/env.restored"
	[ ! -L "$env_file" ] || { echo "arquivo de ambiente com symlink recusado" >&2; exit 1; }
	env_tmp=$(mktemp "$(dirname "$env_file")/.restore-env.XXXXXX")
	cat "$work_tmp/env.restored" >"$env_tmp"
	chmod 600 "$env_tmp"
	mv "$env_tmp" "$env_file"
fi
install -m 0600 "$work_tmp/recovered.env" "$RESTORE_KEYS_OUT"
export CHATISP_DATA_KEYS="$rec_keys"
export CHATISP_DATA_KEY_VERSION="$rec_ver"

applied_check=$("${compose[@]}" run --rm -T --no-deps --entrypoint sh chatisp-migrate -c 'printf "%s\n%s\n" "$CHATISP_DATA_KEYS" "$CHATISP_DATA_KEY_VERSION"')
if [ "$applied_check" != "$(printf '%s\n%s' "$rec_keys" "$rec_ver")" ]; then
	echo "falha ao aplicar o anel de chaves recuperado ao ambiente do Compose" >&2
	exit 1
fi
echo "    chaves recuperadas em $RESTORE_KEYS_OUT e aplicadas ao ambiente"

echo "2/7 banco: pgbackrest restore ($restore_args)"
# shellcheck disable=SC2086
"${compose[@]}" run --rm -T --no-deps -u postgres --entrypoint pgbackrest postgres --stanza=chatisp $restore_args restore

echo "3/7 banco: subindo o PostgreSQL restaurado (aplicação permanece fechada)"
"${compose[@]}" up -d --wait --wait-timeout 300 postgres
# A base backup may predate the guard table; migrate while API and worker are closed.
"${compose[@]}" run --rm -T --no-deps chatisp-migrate

echo "4/7 exclusões: validando livros, conferindo vínculo provedor/anexo/objeto e reaplicando"
in_mirror --entrypoint sh chatisp-migrate -c "
	set -eu
	[ -d $mirror/ledgers ] || { echo 'diretório de livros ausente no espelho' >&2; exit 1; }
	set -- $mirror/ledgers/deletions-*.tsv
	[ -e \"\$1\" ] || { echo 'livro de exclusões ausente no espelho' >&2; exit 1; }
	set -- $mirror/ledgers/attachments-*.tsv
	[ -e \"\$1\" ] || { echo 'mapa de anexos ausente no espelho' >&2; exit 1; }
"
in_mirror --entrypoint tar chatisp-migrate -cf - -C "$mirror" ledgers >"$work_tmp/ledgers.tar"
python3 "$here/validate-artifacts.py" extract-ledgers "$work_tmp/ledgers.tar" "$work_tmp"
python3 "$here/validate-artifacts.py" verify-ledgers "$work_tmp/ledgers"

: >"$work_tmp/deletions-rows.tsv"
for del_file in "$work_tmp"/ledgers/deletions-*.tsv; do
	[ -r "$del_file" ] && [ -s "$del_file" ] || { echo "livro de exclusões ilegível ou vazio: $del_file" >&2; exit 1; }
	first_line=$(head -n 1 "$del_file")
	last_line=$(tail -n 1 "$del_file")
	if ! { [ "$first_line" = "# chatisp-deletions-ledger-1" ] && [ "$last_line" = "# end-chatisp-deletions-ledger-1" ]; } && ! { [ "$first_line" = "# chatisp-deletions-ledger-2" ] && [ "$last_line" = "# end-chatisp-deletions-ledger-2" ]; }; then
		echo "livro de exclusões truncado ou sem delimitadores íntegros: $(basename "$del_file")" >&2
		exit 1
	fi
	sed '1d;$d' "$del_file" >>"$work_tmp/deletions-rows.tsv"
done

{
	echo "CREATE TEMP TABLE ledger_external (tenant_id uuid, subject text, subject_id uuid);"
	while IFS= read -r line; do
		[ -n "$line" ] || { echo "linha vazia inesperada no livro de exclusões" >&2; exit 1; }
		IFS=$'\t' read -r tenant subject id extra <<<"$line"
		if [ -n "${extra:-}" ] || [[ ! "$tenant" =~ $uuid_re ]] || [[ ! "$id" =~ $uuid_re ]] || [[ "$subject" != "attachment" && "$subject" != "credential_secret" && "$subject" != "contact_privacy_guarded" ]]; then
			echo "livro de exclusões corrompido: $line" >&2
			exit 1
		fi
		echo "INSERT INTO ledger_external VALUES ('$tenant', '$subject', '$id');"
	done <"$work_tmp/deletions-rows.tsv"
	echo "CREATE TEMP TABLE guards_external (tenant_id uuid, connector_id uuid, key_version text, identity_mac bytea, cutoff timestamptz);"
	shopt -s nullglob
	for guard_file in "$work_tmp"/ledgers/guards-*.tsv; do
		while IFS=$'\t' read -r tenant connector version mac cutoff; do
			# All fields were strictly validated by verify-ledgers above.
			printf "INSERT INTO guards_external VALUES ('%s','%s','%s',decode('%s','hex'),'%s');\n" "$tenant" "$connector" "$version" "$mac" "$cutoff"
		done < <(sed '1d;$d' "$guard_file")
	done
	shopt -u nullglob
	cat "$here/reapply-deletions.sql"
} >"$work_tmp/reapply.sql"

: >"$work_tmp/attachments-rows.tsv"
for att_file in "$work_tmp"/ledgers/attachments-*.tsv; do
	[ -r "$att_file" ] && [ -s "$att_file" ] || { echo "mapa de anexos ilegível ou vazio: $att_file" >&2; exit 1; }
	first_line=$(head -n 1 "$att_file")
	last_line=$(tail -n 1 "$att_file")
	if [ "$first_line" != "# chatisp-attachments-ledger-1" ] || [ "$last_line" != "# end-chatisp-attachments-ledger-1" ]; then
		echo "mapa de anexos truncado ou sem delimitadores íntegros: $(basename "$att_file")" >&2
		exit 1
	fi
	sed '1d;$d' "$att_file" >>"$work_tmp/attachments-rows.tsv"
done

# Inclui também os registros presentes no banco restaurado antes de reaplicar as exclusões.
sql -c "\\copy (select id, tenant_id, object_key, coalesce(object_version, ''), expected_bytes from attachments where object_key <> '' order by id) to stdout" >>"$work_tmp/attachments-rows.tsv"
sql -c "\\copy (select tenant_id, namespace from attachment_budgets order by tenant_id) to stdout" >"$work_tmp/budgets.tsv"

awk -F'\t' '
FILENAME == ARGV[1] {
	if (NF != 2) {
		printf "orçamento de anexos inválido na linha %d\n", FNR > "/dev/stderr"
		exit 1
	}
	t = $1; ns = $2
	if ((t in tenant_ns) && tenant_ns[t] != ns) {
		printf "provedor %s com múltiplos namespaces (%s vs %s)\n", t, tenant_ns[t], ns > "/dev/stderr"
		exit 1
	}
	if ((ns in ns_tenant) && ns_tenant[ns] != t) {
		printf "namespace %s compartilhado entre provedores (%s vs %s)\n", ns, ns_tenant[ns], t > "/dev/stderr"
		exit 1
	}
	tenant_ns[t] = ns
	ns_tenant[ns] = t
	next
}
{
	if (NF < 5) {
		printf "registro de anexo inválido na linha %d\n", FNR > "/dev/stderr"
		exit 1
	}
	id = $1; t = $2; key = $3
	slash = index(key, "/")
	if (slash <= 1) {
		printf "object_key sem namespace válido: %s\n", key > "/dev/stderr"
		exit 1
	}
	ns = substr(key, 1, slash - 1)
	if ((t in tenant_ns) && tenant_ns[t] != ns) {
		printf "anexo %s do provedor %s aponta para namespace divergente (%s vs %s)\n", id, t, ns, tenant_ns[t] > "/dev/stderr"
		exit 1
	}
	if ((ns in ns_tenant) && ns_tenant[ns] != t) {
		printf "chave %s usa namespace %s pertencente a outro provedor (%s vs %s)\n", key, ns, ns_tenant[ns], t > "/dev/stderr"
		exit 1
	}
	tenant_ns[t] = ns
	ns_tenant[ns] = t
	identity = t SUBSEP id
	if ((identity in att_key) && att_key[identity] != key) {
		printf "associação inconsistente para o anexo %s\n", id > "/dev/stderr"
		exit 1
	}
	if ((key in key_att) && (key_att[key] != id || key_tenant[key] != t)) {
		printf "chave de objeto %s associada a múltiplos anexos/provedores\n", key > "/dev/stderr"
		exit 1
	}
	att_tenant[identity] = t
	att_id[identity] = id
	att_key[identity] = key
	key_att[key] = id
	key_tenant[key] = t
}
END {
	for (identity in att_key) {
		printf "%s\t%s\t%s\n", att_id[identity], att_tenant[identity], att_key[identity]
	}
}
' "$work_tmp/budgets.tsv" "$work_tmp/attachments-rows.tsv" | sort -u >"$work_tmp/attachments-map.tsv"

"${compose[@]}" exec -T postgres psql -U chatisp -d chatisp -v ON_ERROR_STOP=1 -At <"$work_tmp/reapply.sql" | tail -1

# A database snapshot may itself predate provenance tracking. Do not reopen
# unless every erased contact has a verified record, including imported ones.
unprotected=$(sql -c "select count(*) from contacts c where c.privacy_erased_at is not null and (exists (select 1 from retention_deletion_records r where r.tenant_id=c.tenant_id and r.class='content' and r.target_type='contact' and r.target_id=c.id and not r.ingress_guarded) or not exists (select 1 from retention_deletion_records r where r.tenant_id=c.tenant_id and r.class='content' and r.target_type='contact' and r.target_id=c.id and r.ingress_guarded))")
[ "$unprotected" = 0 ] || { echo "exclusões históricas sem prova de proteção de ingresso; concilie historicamente antes da restauração" >&2; exit 1; }

sql -c "\\copy (select tenant_id, subject_id from deletion_records where subject = 'attachment' order by tenant_id, subject_id) to stdout" >"$work_tmp/deleted-attachments.tsv"

awk -F'\t' '
FILENAME == ARGV[1] {
	identity = $2 SUBSEP $1
	att_key[identity] = $3
	next
}
{
	if (NF != 2) {
		printf "registro de exclusão inválido na linha %d\n", FNR > "/dev/stderr"
		exit 1
	}
	t = $1; id = $2
	identity = t SUBSEP id
	if (!(identity in att_key)) {
		printf "anexo excluído %s (provedor %s) ausente no mapa de anexos\n", id, t > "/dev/stderr"
		exit 1
	}
	print att_key[identity]
}
' "$work_tmp/attachments-map.tsv" "$work_tmp/deleted-attachments.tsv" | sort -u >"$work_tmp/restore-skip.txt"

in_mirror --entrypoint sh chatisp-migrate -c "set -eu; umask 077; cat > $mirror/restore-skip.txt" <"$work_tmp/restore-skip.txt"
skip_count=$(wc -l <"$work_tmp/restore-skip.txt" | tr -d ' ')
echo "    ${skip_count} objeto(s) excluído(s) pela política não voltarão ao storage"

# Every encrypted field in the recovered database must have its key in the recovered ring.
sql -c "select distinct key_version from (
  select totp_key_version as key_version from users where totp_key_version is not null
  union select key_version from credential_secrets
  union select key_version from meta_app_registry_secrets
  union select key_version from meta_app_secrets
  union select key_version from contact_document_peppers
  union select key_version from contact_documents where key_version is not null
  union select secret_key_version from webhook_subscriptions
  union select key_version from privacy_ingress_guards
) versions" >"$work_tmp/required-keys.txt"
while IFS= read -r required; do
	case ",$rec_keys," in
	*,"$required":*) ;;
	*) echo "chave necessária ao conteúdo cifrado do banco está ausente no anel recuperado" >&2; exit 1 ;;
	esac
done <"$work_tmp/required-keys.txt"

# Cryptographically authenticate every encrypted value with the recovered ring, using a
# one-shot recovery-only DSN with BYPASSRLS. Never log its credentials or leave it on the API.
recovery_password=$("${compose[@]}" exec -T postgres printenv POSTGRES_PASSWORD)
[ -n "$recovery_password" ] || { echo "senha de recuperação do banco ausente" >&2; exit 1; }
# URL-encode the password rather than letting @, colon or slash change the DSN meaning.
recovery_password_encoded=$(printf '%s' "$recovery_password" | python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.stdin.read(), safe=""))')
recovery_dsn="postgres://chatisp:${recovery_password_encoded}@postgres:5432/chatisp?sslmode=disable"
CHATISP_RECOVERY_DATABASE_URL="$recovery_dsn" "${compose[@]}" run --rm -T --no-deps -e CHATISP_RECOVERY_DATABASE_URL chatisp-migrate verify-data-keys
unset recovery_password recovery_password_encoded recovery_dsn

echo "5/7 anexos: subindo o armazenamento TLS e provisionando o bucket (aplicação permanece fechada)"
"${compose[@]}" up -d --wait --wait-timeout 300 garage caddy
"${compose[@]}" run --rm -T --no-deps trust-export
"${compose[@]}" run --rm -T --no-deps storage-setup

# The database base backup and object mirror are not one transaction. Never reopen if a
# surviving completed/validated reference is missing from the mirror or has divergent bytes.
sql -c "\\copy (select object_key, object_version, sha256, expected_bytes, state from attachments where object_version <> '' or state = 'validated' order by object_key) to stdout" >"$work_tmp/required-attachments.tsv"
in_mirror --entrypoint cat chatisp-migrate "$mirror/objects.manifest" >"$work_tmp/objects.manifest"
python3 "$here/validate-artifacts.py" verify-references "$work_tmp/objects.manifest" "$work_tmp/required-attachments.tsv"

echo "6/7 anexos: restaurando o espelho"
in_mirror chatisp-migrate attachments-restore -src "$mirror" -skip "$mirror/restore-skip.txt"

echo "7/7 serviço: subindo a aplicação"
"${compose[@]}" up -d --wait --wait-timeout 300
restore_complete=1
echo "restauração concluída; confira o painel, os anexos mantidos e a ausência dos excluídos antes de liberar o acesso"
