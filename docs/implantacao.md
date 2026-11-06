# Implantação: endereço público, certificado, origens, cookies, CSRF e WebSocket

Revisado na retomada de 05/11/2026. Evidências atuais: [relatório](medicoes/retomada-20261105/relatorio.md). Dois arquivos de borda, os dois com o mesmo desenho:

| Arquivo | Para quê | Certificado | Portas publicadas |
| --- | --- | --- | --- |
| `deploy/Caddyfile` (com `deploy/compose.yaml`) | laboratório e validação | CA interna do Caddy (`tls internal`) | só `127.0.0.1:${CHATISP_PUBLIC_PORT:-8443}` → 443 |
| `deploy/Caddyfile.production` (com `deploy/compose.production.yaml`) | domínio próprio | público, emitido por ACME | 80 e 443 (`CHATISP_PUBLIC_BIND`, padrão `0.0.0.0`) |

**Nada de produção foi ativado**: nenhum domínio, DNS ou porta pública neste servidor. Os arquivos de produção não têm domínio nem contato escritos; `CHATISP_SITE_HOST`, `CHATISP_STORAGE_HOST` e `CHATISP_ACME_EMAIL` são obrigatórios e o Compose recusa a subida sem eles (`TestProductionDeployFilesStayInactiveAndCoherent` guarda isso).

## A regra de coerência

Cinco pontos usam o **mesmo nome** que o navegador digita. Se um deles diverge, algo quebra de forma específica:

| Ponto | Onde se configura | Se divergir |
| --- | --- | --- |
| Nome do painel | `CHATISP_SITE_HOST` (Caddy) | certificado não cobre o nome; erro de TLS |
| Origem permitida | `CHATISP_PANEL_ORIGINS` = `https://<CHATISP_SITE_HOST>[:porta publicada]` | `Origin` recusado em escrita (403) e no WebSocket |
| Cookies | `__Host-session` e `__Host-csrf` com `Secure`, sem `Domain`, caminho `/` | com HTTP ou outro host o navegador não guarda o cookie |
| Nome do storage | `CHATISP_STORAGE_HOST` (Caddy) = `CHATISP_ATTACHMENTS_S3_PUBLIC_ENDPOINT` | a assinatura SigV4 cobre o `Host`; download assinado dá 403 |
| Endpoint interno do storage | `CHATISP_ATTACHMENTS_S3_ENDPOINT` | a aplicação não grava anexos (erro 5xx claro) |

No laboratório os dois nomes são `localhost` e `storage.localhost` com a porta publicada; em produção, os dois domínios do provedor.

## CSRF, Origin e WebSocket

- Escritas do painel exigem cookie de sessão **e** `X-CSRF-Token` igual ao cookie legível `__Host-csrf` **e** `Origin` permitido (`http.CrossOriginProtection` + token).
- O WebSocket (`/api/panel/ws`) exige sessão e `Origin` da lista; cada assinatura é autorizada por tenant e caixa, e a sessão é revalidada periodicamente.
- O Caddy repassa o `Upgrade` sem configuração extra (verificado: a mensagem nova chega ao painel sem recarregar, em `scripts/compose-validate.sh`).
- Webhooks da Meta usam o próprio HMAC (`X-Hub-Signature-256`), não CSRF.

## Métricas

`/metrics` **não** passa pelo Caddy público (responde 404). As portas 9090 de `chatisp-api` e `chatisp-worker` ficam só na rede interna do Compose e **não são publicadas**. Para coletar, rode o Prometheus na mesma rede Compose (alvos `chatisp-api:9090` e `chatisp-worker:9090`) ou publique a porta em uma interface privada com firewall por decisão do operador. A coleta interna foi conferida (`# HELP` presente) e a ausência de publicação também.

## Validação (o que foi e o que não foi provado)

`scripts/compose-validate.sh` prova, num navegador real, na origem `https://localhost:<porta>` e com o storage em `https://storage.localhost:<porta>`:

- login, MFA, convite, aceite pelo link e recuperação de senha;
- cookies com `Secure`, `HttpOnly` e `SameSite`; escrita sem CSRF e com `Origin` estranho recusadas; WebSocket com `Origin` estranho recusado; WebSocket legítimo atualizando a tela;
- anexo gravado e lido no Garage por TLS, URL assinada de 60 s;
- `/metrics` ausente do acesso público e portas publicadas só as esperadas.

**Limite honesto da prova de TLS**: o Chromium está fixado no certificado do laboratório por SPKI (`--ignore-certificate-errors-spki-list`) e as chamadas de Node confiam só na raiz da CA do Caddy. Isso exercita a cadeia e o nome, mas não equivale a instalar a CA no repositório de confiança do sistema, e **não prova** um certificado público ACME, que exige domínio e DNS reais. Essa prova fica para a implantação (tabela de dependências externas).

## Conferência antes de abrir ao público

1. DNS dos dois nomes aponta para o servidor; portas 80 e 443 livres.
2. `deploy/.env` completo; `CHATISP_ENV=production`; segredos trocados.
3. `docker compose -f deploy/compose.yaml -f deploy/compose.production.yaml --env-file deploy/.env config -q`.
4. Subir; abrir `https://<CHATISP_SITE_HOST>/entrar` e conferir o certificado emitido.
5. `chatisp bootstrap` para o primeiro administrador.
6. Conferir que `https://<CHATISP_SITE_HOST>/metrics` responde 404 e que `docker compose ps` não mostra 9090 publicada.
7. Conferir o download de um anexo de teste e o WebSocket (mensagem nova sem recarregar).

## Correção da composição de produção nesta retomada

A borda de produção tem sonda interna em `:8081/edge-health`, sem porta publicada. O serviço `trust-export` apenas prepara o diretório: o runtime usa a trust store pública do sistema (`CA_FILE` vazio). Assim a subida de produção não depende do arquivo da CA interna do laboratório, que não existe em ACME.

Configuração de produção preparada e validada em sintaxe não significa certificado público emitido nem implantação autorizada. A prova integrada usa laboratório local; domínio, DNS, ACME e passagem de operador continuam na [tabela única de dependências](dependencias-externas.md).

## Atualização com migrações de privacidade

As migrações `00053`–`00055` exigem uma janela sem API, ingresso nem workers antigos. Não há suporte a atualização gradual com versões misturadas nem a executar binário antigo depois da migração. Prepare as imagens e o backup antes da janela; use os mesmos arquivos Compose, nome do projeto e arquivo de ambiente da instalação em todos os comandos.

1. Fechar o acesso operacional e parar `chatisp-api` e `chatisp-worker`. A API também é o ingresso de webhook. Confirmar a saída dos processos e o término das conexões/transações desses serviços no PostgreSQL; contêiner parado sozinho não comprova ausência de outro executor.
2. Executar `docker compose <argumentos-da-instalação> run --rm --no-deps chatisp-migrate` com a imagem nova. Qualquer erro mantém aplicação e ingresso fechados.
3. Recriar API e worker com as imagens novas, conferir readiness e validar acesso antes de liberar atendimento. Não reutilizar contêiner/binário antigo. Os rollbacks das novas migrações recusam descartar a fila e a proveniência de exclusões.

O corte `privacy_legacy_cutoff` representa a ativação conservadora dessa proteção para contatos historicamente apagados; não é uma reconstrução do instante de exclusão. Entradas anteriores sem prova ficam em quarentena com payload restrito preservado para conciliação autorizada. A recuperação recusa livros ou snapshots com exclusões históricas sem cobertura comprovada. Antes de atualizar uma instalação com esse histórico, planejar a conciliação e o procedimento de recuperação com o operador; não transformar registros antigos em comprovados por edição manual de flags. Nenhuma conciliação ou instalação de produção foi executada nesta missão.
