# M7 — Identidade simples do provedor

Contrato da seção 12 em recorte V1 simples: nome e logo na interface autenticada. Não é white label, não muda cor de marca e não cria domínio próprio. Não homologa produção, não liga expurgo e não usa storage público nem API paga.

As rotas ficam fora de `backend/api/openapi.json`. O processo as registra em `branding.Mount` com o `Store` e o `Auth` já existentes.

## Nome

A única autoridade do nome é `tenant_retention_profiles.display_name`. Não há segunda coluna de nome.

`GET /api/panel/branding` devolve esse nome, `fallback_name` = `Chat ISP` e metadados do logo. Nome vazio no shell e no título usa `Chat ISP`.

`PUT /api/panel/branding` aceita só `{"display_name":"..."}`. O valor é aparado e tem no máximo 80 caracteres, o mesmo limite da retenção. String vazia grava nulo e o shell volta ao fallback. Campo extra, inclusive `automatic_purge` ou prazo, responde 422 e não grava.

O `UPDATE` altera `display_name` e `updated_at`. Não altera `content_days`, `media_days`, `envelope_days`, `log_days`, `job_days` nem `automatic_purge`. Se a linha ainda não existe, a inclusão nasce com prazos nulos e `automatic_purge` falso. Guardar o nome na retenção também atualiza o shell: o cliente invalida a consulta `["branding", tenant, sessão]` sem apagar a política.

## Logo

`tenant_provider_logos` guarda um PNG ou JPEG por provedor, em `bytea`. A migração `00045_m7_provider_identity.sql` liga RLS e `FORCE ROW LEVEL SECURITY`. A policy exige `app.tenant_id`. `chatisp_app` recebe só `SELECT`, `INSERT`, `UPDATE` e `DELETE`. `PUBLIC`, `chatisp_platform` e `chatisp_ingress` não recebem grant.

Limites, no processo e no banco: no máximo 256 KiB e 1024 por 1024 pixels. O processo confere o tipo declarado com os bytes, recusa SVG, HTML, URL (`http`, `https`, `//`, `data:`), MIME enganoso e imagem que não decodifica. Em seguida reencoda com a biblioteca padrão, para não armazenar marcação depois dos bytes da imagem. Não há dependência nova nem URL remota persistida.

`PUT /api/panel/branding/logo` recebe o corpo cru com `Content-Type` `image/png` ou `image/jpeg`. `DELETE /api/panel/branding/logo` apaga só o logo. `GET /api/panel/branding/logo` responde os bytes reencodados com o `Content-Type` conferido de novo, `X-Content-Type-Options: nosniff` e `Cache-Control: private, no-store`. Sem logo, 404. A resposta JSON usa os mesmos cabeçalhos de cache. Log e auditoria não levam os bytes, a URL nem segredo; a auditoria registra a ação e o resultado.

## Acesso

Leitura: sessão do provedor ativo, papel `admin`, `supervisor` ou `agent`. Superadmin recebe 403 na leitura operacional. Escrita e remoção: só `admin` com MFA completo (`EnrollmentOnly` falso), cookie de sessão, `X-CSRF-Token` e `Origin` confiável. Sem isso, 403. Sem sessão, 401.

A consulta do navegador inclui provedor e sessão na chave. Trocar de provedor não reutiliza nome nem blob do anterior. A gravação composta fica presa ao provedor, à sessão e ao CSRF capturados no início: se a sessão mudar ou o editor desmontar enquanto o `PUT` do nome ainda responde, o cliente não envia o logo e não usa o CSRF da sessão nova. O editor fica em Configurações para o administrador com MFA completo, com prévia, erros e os botões de guardar e remover.

## Fora deste contrato

White label, cor, domínio, OpenAPI gerado, expurgo, mídia de atendimento, ERP, River, storage externo e homologação de produção. Este lote não declara o marco M7 ou M8 concluído.
