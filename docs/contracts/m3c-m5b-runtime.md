# M3C e M5B — composição no runtime

Este contrato liga três módulos já entregues ao processo: documentos opcionais do contato, presença do atendente e o núcleo de round-robin. Não há chamada a Meta, ERP ou Evolution. Não há novo WebSocket. A atribuição automática nasce desligada.

## Rotas

O processo registra, mesmo sem serviço:

- `/api/panel/contacts/{contact_id}/document` e `.../document/reveal`
- `POST /api/panel/contacts/document-search`
- `/api/panel/presence`, `/api/panel/presence/heartbeat`, `/api/panel/presence/tabs/{tab_id}`, `/api/panel/presence/manual`, `/api/panel/presence/settings`
- `GET /api/panel/sectors/{sector_id}/presence`
- `GET` e `PUT /api/panel/sectors/{sector_id}/distribution`

Sem autenticação ou sem serviço, a operação responde 503. Documento, presença e a flag seguem sessão, Origin confiável e CSRF nas escritas. A leitura da fila é de admin e supervisor. Só o admin grava a flag. Superadmin e sessão só de cadastro MFA recebem 403.

`GET` da distribuição exige `inbox_id`. A resposta traz `enabled`, `version`, `configured`, o motivo da fila e os itens. `Cache-Control` é `no-store`. A fila continua visível com a flag desligada.

## Documento e pepper

O anel `CHATISP_DATA_KEYS` / `CHATISP_DATA_KEYS_FILE`, com `CHATISP_DATA_KEY_VERSION`, é a chave mestra. O pepper do documento é aleatório por tenant, cifrado com esse anel e gravado em `contact_document_peppers`. Não existe variável de pepper. `CHATISP_CONTACT_DOCUMENT_PEPPER` e `CHATISP_DOCUMENT_PEPPER`, se vierem preenchidas, impedem a subida e o erro cita só o nome `CHATISP_CONTACT_DOCUMENT_PEPPER`. O valor não entra na configuração, no erro nem no log. A máscara HTTP não devolve dígitos.

## Presença no ciclo da sessão

`GET /api/panel/session` renova o heartbeat da aba cujo id é o id da sessão. `DELETE /api/panel/session` fecha essa aba antes de revogar a sessão. O WebSocket já existente recebe um id de conexão próprio, distinto desse id de sessão. `AuthenticateSession` e `RevalidatePrincipal` renovam somente essa conexão. Com a conexão realtime ainda aberta, o servidor renova essa mesma conexão por um ticker próprio, a cada um terço do TTL do heartbeat (`presence.HeartbeatTTL / 3`, 10s enquanto o TTL for 30s). Esse ticker não espera o ping do navegador. Ele para quando a conexão fecha e quando a revalidação da sessão falha por expiração ou revogação; depois disso não há novo heartbeat dessa aba. O encerramento fecha só a conexão cujo handshake autenticou e gravou o heartbeat, e só depois de o ticker ter parado. Pedido recusado por método, Origin ou sessão não renova nem apaga outra aba. O fechamento não autentica de novo. Não há outro socket. Falha de presença nesse ciclo é registrada sem o segredo e sem derrubar a sessão. A rota `POST /api/panel/presence/heartbeat` continua sendo o heartbeat explícito de uma aba.

Uma sessão ativa em outra página, sem caixa selecionada, não abre este socket. O ticker desta conexão não a cobre. A renovação dessa sessão fica com o heartbeat HTTP já existente, `POST /api/panel/presence/heartbeat`, que a tarefa m5-panel-operations liga no layout do aplicativo. Esta composição não faz essa ligação.

## Flag e chegada

`distribution.Pick` trata setor sem linha como ligado. Este processo não. `readCompositionAuto` lê `distribution_sector_auto` pela consulta sqlc `GetDistributionSectorAuto`. Sem linha, `enabled` é falso, `version` é 0 e `configured` é falso. `AssignArrived` só chama `Pick` quando a linha existe e `enabled` é verdadeiro. Caso contrário devolve `queued` / `auto_disabled` e não reserva vaga nem move o cursor.

O worker de chegada chama `Arrive`. O handler de inbound existente corre primeiro. Uma chegada real entra como item `pending`, sem mensagem e sem conversa. Esse handler grava a mensagem, a conversa aberta e o setor padrão da caixa; `PickInbound` lê esses dados pelo `external_id` do payload. Com a flag da caixa desligada a conversa permanece sem responsável. Com a flag ligada a mesma chegada chega à atribuição. No sucesso, inclusive quando o item já estava processado, `PickInbound` executa uma tentativa. Isso recupera falha entre o inbound e a distribuição. A tentativa toma o advisory compartilhado do tenant, grava a chave do item pela consulta sqlc `ClaimDistributionArrival` e só então chama `AssignArrived` na mesma transação. A chave é o próprio `webhook_items`: a consulta acrescenta `distribution_arrival_claim` ao payload quando `kind` é `message`, `state` é `processed` e a propriedade ainda não existe. O inbound de item já processado retorna antes de reler o payload e ignora campo desconhecido. Zero linhas encerra a reprise sem outro `Pick`, também depois de devolução e também se a conversa do item continuar sem responsável. Item pendente, de status ou desconhecido não grava a chave. Flag desligada grava a chave e não atribui; ligá-la depois não redistribui esse item nem a cabeça da fila. A ordem de lock de `Pick` permanece a do módulo. A atualização trava a linha do item antes de `Pick`. A gravação da flag toma o advisory exclusivo do tenant, revalida a matrícula e só então chama `PutSectorAuto`.

M2, M3 e M4A não passam a distribuir sozinhos: a flag nasce desligada e a chegada sem linha não escolhe atendente.

## Geração

OpenAPI, sqlc e o esquema TypeScript desta worktree acompanham as rotas e a consulta da flag.
