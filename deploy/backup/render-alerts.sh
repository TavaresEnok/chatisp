#!/usr/bin/env bash
# Renderiza as regras de alerta de ausência e atraso dos componentes obrigatórios do backup
# (postgres, attachments, keys e ledger) para uma instalação identificada.
# BACKUP_MAX_AGE_SECONDS é obrigatório (decisão operacional D2-07; não equivale sozinho ao RPO).
# BACKUP_MAX_LEDGER_AGE_SECONDS é opcional (padrão: BACKUP_MAX_AGE_SECONDS).
# BACKUP_INSTALLATION identifica a instalação na agregação central (obrigatório, único por instalação).
set -euo pipefail
: "${BACKUP_MAX_AGE_SECONDS:?defina BACKUP_MAX_AGE_SECONDS (segundos; decisão do provedor, sem padrão)}"
ledger_age=${BACKUP_MAX_LEDGER_AGE_SECONDS:-$BACKUP_MAX_AGE_SECONDS}
: "${BACKUP_INSTALLATION:?defina BACKUP_INSTALLATION (identidade única da instalação)}"
installation=$BACKUP_INSTALLATION
case "$BACKUP_MAX_AGE_SECONDS" in '' | *[!0-9]* | 0) echo "BACKUP_MAX_AGE_SECONDS deve ser um inteiro positivo" >&2; exit 1 ;; esac
case "$ledger_age" in '' | *[!0-9]* | 0) echo "BACKUP_MAX_LEDGER_AGE_SECONDS deve ser um inteiro positivo" >&2; exit 1 ;; esac
case "$installation" in '' | *[!a-zA-Z0-9_.:-]*) echo "BACKUP_INSTALLATION contém caracteres inválidos" >&2; exit 1 ;; esac
here=$(cd "$(dirname "$0")" && pwd)
sed \
	-e "s/__BACKUP_MAX_AGE_SECONDS__/${BACKUP_MAX_AGE_SECONDS}/g" \
	-e "s/__BACKUP_MAX_LEDGER_AGE_SECONDS__/${ledger_age}/g" \
	-e "s/__BACKUP_INSTALLATION__/${installation}/g" \
	"$here/alerts.rules.yml.tmpl"
