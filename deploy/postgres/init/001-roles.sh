#!/bin/sh
# Roda só na primeira inicialização do volume, como superusuário da imagem.
# As senhas vêm do ambiente do contêiner, não deste arquivo.
set -eu

: "${CHATISP_OWNER_PASSWORD:?defina CHATISP_OWNER_PASSWORD}"
: "${CHATISP_APP_PASSWORD:?defina CHATISP_APP_PASSWORD}"
: "${CHATISP_PLATFORM_PASSWORD:?defina CHATISP_PLATFORM_PASSWORD}"
: "${CHATISP_INGRESS_PASSWORD:?defina CHATISP_INGRESS_PASSWORD}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	-v owner_password="$CHATISP_OWNER_PASSWORD" \
	-v app_password="$CHATISP_APP_PASSWORD" \
	-v platform_password="$CHATISP_PLATFORM_PASSWORD" \
	-v ingress_password="$CHATISP_INGRESS_PASSWORD" <<'SQL'
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE ROLE chatisp_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD :'owner_password';
CREATE ROLE chatisp_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD :'app_password';
CREATE ROLE chatisp_platform LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD :'platform_password';
CREATE ROLE chatisp_ingress LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD :'ingress_password';

SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_owner, chatisp_app, chatisp_platform, chatisp_ingress', current_database()) \gexec
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO chatisp_owner;
GRANT USAGE ON SCHEMA public TO chatisp_app, chatisp_platform, chatisp_ingress;
ALTER SCHEMA public OWNER TO chatisp_owner;
SQL
