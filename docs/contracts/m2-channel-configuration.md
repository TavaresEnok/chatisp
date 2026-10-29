# Contrato M2 — configuração manual do canal Meta

Este contrato descreve o pacote `backend/internal/channel/configuration`. Ele grava a caixa Meta do tenant e publica a resolução que o HTTP e o ingresso futuros consomem. Não há rota, tela, chamada externa, ERP nem Evolution utilizável.

A persistência de app, WABA, número, ciphertext e quarentena continua no pacote `internal/attendance/store` e na migração `00006_attendance.sql`. A cifra é `crypto.Ring`. Este pacote não implementa AES.

## Quem configura

`Configure`, `Status`, `AcknowledgeAlert` e `Inspect` exigem o hash SHA-256 da sessão, com 32 bytes. O tenant sai de `auth_load_session`. O formulário `Manual` não tem tenant e o pacote não chama `set_config`.

A sessão precisa estar ativa, o vínculo precisa ser `admin` ativo, `auth_session_gate` precisa devolver `assurance=full` e TOTP confirmado. Papel de atendente ou supervisor, sessão sem MFA e sessão só de cadastro (`enrollment`) são recusados. O vínculo é relido dentro da transação do tenant.

## O que é gravado

Cada caixa é um `channel_connectors.connector_type=meta_cloud` ativo, uma `inboxes` e a cadeia `meta_apps` → `meta_wabas` → `meta_phone_numbers`. A primeira caixa de um app usa `store.RegisterMeta`. A segunda caixa do mesmo app reutiliza o app e, quando o WABA já é desse app, só acrescenta o número. App, WABA ou número já usados por outro tenant falham com `ErrConflict`, sem devolver o outro tenant. WABA de outro app do mesmo tenant é `ErrInvalid`.

O token fica em `credential_secrets` (`entity=channel_connector`, `field=access_token`). O App Secret fica em `meta_app_secrets` (`entity=meta_app`, `field=app_secret`), não na caixa. Uma gravação nova sela o segredo com a chave corrente do ring e AAD `tenant`, entidade, id da entidade e campo. O mesmo App Secret não reescreve o ciphertext: a versão da chave que já está na linha permanece, ainda que o ring do processo tenha avançado. O token da caixa é selado de novo a cada gravação, com a chave corrente. A resposta `Box` traz essas versões persistidas, nunca o segredo, o ciphertext nem o AAD.

`channel_connectors.version` começa em `1` e sobe a cada gravação daquela caixa. Trocar o App Secret também sobe a versão das outras caixas do mesmo app. Trabalho antigo que apresente outra versão recebe `ErrStaleVersion`.

O tipo ativo não é atualizado por este pacote. A linha `evolution` continua recusada pelo `CHECK` quando `enabled=true` e pelo gatilho quando a conexão já está `connected` ou `degraded`.

## Servidor

`Service.Server` é objeto de processo. Não nasce de payload.

`ResolveRoute(appID, phoneNumberID)` usa `db.WithIngressTx` e `store.ResolvePhone`. O tenant é o da linha que junta app, WABA e número ativos. O chamador não escolhe tenant.

`ResolveMaterial(tenantID, connectorID, version)` abre token e App Secret só para o conector `meta_cloud` habilitado, `connected`, na versão pedida e com a cadeia app–WABA–número. Degradação devolve `ErrDegraded`. `Material` recusa JSON e redige `String`, `GoString` e log.

`ResolveApp(appID)` autentica o webhook antes de rotear o corpo. O `appID` é o do endpoint já configurado; não sai de payload ainda não autenticado e o chamador não informa tenant. A transação é `db.WithIngressTx`. A linha ativa de `meta_apps` fornece tenant e id da entidade. O ciphertext vem de `store.ReadAppSecret`. O AAD é reconstruído com tenant, `meta_app`, esse id e `app_secret`, e aberto com `crypto.Ring`. A operação não lê o token de envio nem o status do conector: canal `degraded` continua autenticável. App ausente ou inativo devolve `ErrNotFound`. Segredo transplantado devolve `ErrSecret`. `TrustedApp` traz o App ID e o segredo em claro só no processo; recusa JSON e redige `String`, `GoString` e log. Não devolve ciphertext.

`ReportInvalidCredential(tenantID, connectorID, version)` recebe a versão que o worker usou. Na mesma transação, com a linha bloqueada, só degrada se essa versão ainda é a corrente: marca `degraded`, sobe a versão uma vez e grava um alerta `channel.credential_invalid` com `result=open` em `audit_logs`. O texto do token não entra no alerta. Repetir a mesma versão, ou repetir depois que essa degradação já subiu a versão, não duplica o alerta aberto nem volta a subir a versão. Resultado de versão já substituída — o worker resolveu a versão 1, o admin publicou a versão 2, a chamada antiga volta `token_invalid` — devolve `ErrStaleVersion` sem mudar status, versão ou alerta. `AcknowledgeAlert` passa o alerta aberto a `acknowledged`.

## Inspeção

`Status` lista caixas Meta e alertas abertos do tenant da sessão.

`Inspect` devolve três listas, só com identificadores:

- quarentena de ingresso cujo `app_id` pertence ao tenant, sem o corpo de `ingress_deliveries`;
- `webhook_items` do tenant com `state=quarantined`, sem payload;
- jobs `discarded` em `river.river_job` cujo `args.tenant_id` é o tenant da sessão, sem argumentos e sem texto de erro.

`chatisp_platform` não recebe conversa, segredo nem envelope. `chatisp_ingress` não lê `credential_secrets` nem mensagens. `chatisp_app` não lê o envelope bruto. O contexto da transação continua sendo o de `db.Store` e de `db.WithIngressTx`.

## Erros

`ErrUnauthenticated`, `ErrForbidden`, `ErrMFARequired`, `ErrInvalid`, `ErrConflict`, `ErrNotFound`, `ErrDegraded`, `ErrStaleVersion`, `ErrSecret`, `ErrUnavailable`.

Nenhum deles inclui segredo. Credencial de teste deste pacote é fictícia e não exerce a Cloud API.
