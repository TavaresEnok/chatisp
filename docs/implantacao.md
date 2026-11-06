# Implantação: endereço público, certificado, origens, cookies, CSRF e WebSocket

Revisado em 06/11/2026. A instalação existente `chatisp-pronto-hoje` ganhou encaminhamento pelo nome **https://chatisp.tokstiloesporte.com**. A cadeia identificada é Nginx `10.10.0.10:443` (certificado público) → Apache `10.10.0.26:443` (vhost exclusivo do ChatISP) → Caddy `127.0.0.1:8480` → aplicação/Garage. O acesso pelo nome conectado diretamente ao proxy interno foi validado; o DNS normal aponta para `177.104.156.25`, cujas portas 80/443 recusaram conexão nesta execução. **A acessibilidade pela Internet continua sem comprovação**, dependendo do encaminhamento externo desse IP. Consulte [evidências da publicação](medicoes/publicacao-20261106/relatorio.md).

Perfis disponíveis:

| Arquivo | Para quê | Certificado | Portas publicadas |
| --- | --- | --- | --- |
| `deploy/Caddyfile` (com `deploy/compose.yaml`) | laboratório e validação | CA interna do Caddy (`tls internal`) | só `127.0.0.1:${CHATISP_PUBLIC_PORT:-8443}` → 443 |
| `deploy/Caddyfile.production` (com `deploy/compose.production.yaml`) | domínio próprio | público, emitido por ACME | 80 e 443 (`CHATISP_PUBLIC_BIND`, padrão `0.0.0.0`) |
| `deploy/Caddyfile.behind-proxy` (com `deploy/compose.behind-proxy.yaml`) | instalação existente atrás de outro proxy TLS | certificado público no proxy existente; CA local para storage interno/recuperação | somente loopback: 8443 TLS e 8480 HTTP |

O perfil direto `compose.production.yaml` não foi ativado: Apache já ocupa 80/443, e substituir esses listeners interromperia o PNETLab. Seus campos `CHATISP_SITE_HOST`, `CHATISP_STORAGE_HOST` e `CHATISP_ACME_EMAIL` continuam obrigatórios. A instalação existente reutiliza as imagens e volumes, acrescentando o perfil `behind-proxy`, sem habilitar fornecedores externos.

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

No perfil direto, configuração validada em sintaxe não comprova emissão pública. Na instalação atrás do proxy existente foi conferido o certificado público já emitido para o nome; conectividade externa e passagem do operador têm os limites registrados na [tabela única de dependências](dependencias-externas.md).

## Instalação existente atrás do proxy TLS

Desde `8150c9b`, o MFA é opcional por decisão do proprietário: ativação em Configurações → Segurança da conta, com QR Code local. Fatores já confirmados continuam exigidos no login. A migração 00057 exige API/worker antigos parados e não permite misturar binários; veja [implantação e provas do MFA opcional](medicoes/mfa-opcional-20261106/relatorio.md).

Não execute um novo bootstrap nem troque o projeto Compose. O controlador privado em `/home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py` usa, nesta ordem, `compose.yaml`, `compose.local-alpine.yaml`, a rede privada original e `compose.behind-proxy.yaml`, com o mesmo `ambiente.env`. Banco, chaves, credenciais e volumes originais são reutilizados. O nome adicional é `CHATISP_PROXY_SITE_HOST=chatisp.tokstiloesporte.com`.

A primeira origem permitida é `https://chatisp.tokstiloesporte.com`, usada para links de convite e recuperação. A segunda é `https://localhost:8443`, mantida para recuperação no servidor. Cookies continuam `__Host-*`, `Secure`, sem `Domain`, caminho `/`. SMTP permanece desligado: a formação correta de URLs não significa envio de e-mail disponível.

O endpoint S3 interno continua `https://storage.chatisp.internal`, com validação da CA original. O endpoint de assinatura público é `https://chatisp.tokstiloesporte.com`: o SDK acrescenta o bucket e a chave à URL. O Caddy encaminha **somente GET/HEAD** em `/chatisp-anexos/*` ao Garage S3, sem reescrever Host, caminho ou query. Não é necessário DNS adicional de storage. Acesso sem assinatura é recusado pelo Garage; escrita por essa rota pública responde 404. A API administrativa do Garage e as portas de métricas não são publicadas.

O arquivo instalado `/etc/apache2/sites-available/zz-chatisp.conf`, ligado em `sites-enabled`, corresponde a `deploy/apache-behind-proxy.conf.example`. São vhosts nomeados 80/443, com `ProxyPreserveHost On`, rota WebSocket específica e permissão apenas para o proxy `10.10.0.10` e loopback. O vhost padrão do PNETLab, seus certificados e os listeners 80/443/18080 foram preservados. O Nginx encaminha por HTTPS para o Apache, cujo certificado de transporte preexistente é autoassinado; não foi comprovada a configuração de validação desse certificado no Nginx. O navegador valida o certificado público do Nginx.

Antes de alterar o arquivo Apache, execute `sudo apache2ctl configtest`; aplique com `sudo systemctl reload apache2`, sem reiniciar outros serviços. Um nome de projeto novo, `down -v`, publicação do listener 8480 em outra interface ou substituição do vhost padrão não fazem parte deste procedimento.

O proxy público não foi administrado nesta execução. Logs locais do vhost omitiram a query, mas a política de logs do Nginx deve ser conferida pelo seu administrador: URLs assinadas são credenciais temporárias. A aplicação usa o endereço do proxy nos controles por IP; não foi habilitada confiança indiscriminada em `X-Forwarded-For`.

Backup anterior, configuração privada e recuperação: [procedimento específico](medicoes/publicacao-20261106/backup.md). A recuperação deve preservar as decisões de exclusão posteriores ao snapshot antes de reabrir atendimento.

## Atualização com migrações de privacidade

As migrações `00053`–`00055` exigem uma janela sem API, ingresso nem workers antigos. Não há suporte a atualização gradual com versões misturadas nem a executar binário antigo depois da migração. Prepare as imagens e o backup antes da janela; use os mesmos arquivos Compose, nome do projeto e arquivo de ambiente da instalação em todos os comandos.

1. Fechar o acesso operacional e parar `chatisp-api` e `chatisp-worker`. A API também é o ingresso de webhook. Confirmar a saída dos processos e o término das conexões/transações desses serviços no PostgreSQL; contêiner parado sozinho não comprova ausência de outro executor.
2. Executar `docker compose <argumentos-da-instalação> run --rm --no-deps chatisp-migrate` com a imagem nova. Qualquer erro mantém aplicação e ingresso fechados.
3. Recriar API e worker com as imagens novas, conferir readiness e validar acesso antes de liberar atendimento. Não reutilizar contêiner/binário antigo. Os rollbacks das novas migrações recusam descartar a fila e a proveniência de exclusões.

O corte `privacy_legacy_cutoff` representa a ativação conservadora dessa proteção para contatos historicamente apagados; não é uma reconstrução do instante de exclusão. Entradas anteriores sem prova ficam em quarentena com payload restrito preservado para conciliação autorizada. A recuperação recusa livros ou snapshots com exclusões históricas sem cobertura comprovada. Antes de atualizar uma instalação com esse histórico, planejar a conciliação e o procedimento de recuperação com o operador; não transformar registros antigos em comprovados por edição manual de flags. Nenhuma conciliação ou instalação de produção foi executada nesta missão.
