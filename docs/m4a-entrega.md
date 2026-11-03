# Entrega M4A — aceite integrado do recorte

Data: 03/11/2026. Este recorte não conclui M3, M4 nem M5. Revisão independente e o gate amplo final continuam com o controlador.

## O que a prova executou

Chromium `140.0.7339.186` (revisão Playwright 1193). Nesta rodada o runner usou `.paseo-build-tmp/browser/chromium-launcher`, que executa o `headless_shell` já presente na árvore, com bibliotecas de `.paseo-build-tmp/browser/flat-lib`. Se o launcher e o cache apontado por `PLAYWRIGHT_BROWSERS_PATH` não forem executáveis, o mesmo comando procura o Chromium 1193 em `~/.cache/ms-playwright` e falha listando os caminhos. Não baixa outro browser e não faz skip.

`npm --prefix web run test:e2e:m4a` terminou com código 0 nesta rodada. O anexo `.paseo-build-tmp/m4a-browser/m4a-jornada.json` registra a jornada no painel, pela fixture `m2demo m4a`, com PostgreSQL embutido, workers, sessão, CSRF e Origin. O log do proxy mostrou alertas TLS de clientes que recusam o certificado descartável e uma linha `http: proxy error: context canceled`; o cenário mesmo assim concluiu e gravou a evidência abaixo.

- Respostas prontas na tela de Configurações: criar `/reiniciar`, excluir `/descartar` e atualizar o título. O agente digitou `/reiniciar`, escolheu a opção e o compositor ficou com `Olá Cliente Simulado, protocolo …`.
- Assinatura: o admin ligou o prefixo `{{nome_atendente}}: `. A aceitação humana gravou o e-mail autenticado uma vez. O template não leva prefixo.
- Horário: a tela gravou segunda 09:00–12:00, os três textos e o plantão `SIMULAÇÃO Destino M3B`, no fuso `America/Sao_Paulo`. O teste espera o PUT, o refetch `GET .../business-hours` com a versão gravada e um GET da API confirmando essa versão antes de preencher o instante. `2026-03-02T13:00:00Z` abriu; `2026-03-01T13:00:00Z` fechou. Cada par preencher/consultar/asserir repete com `toPass` porque o formulário pode remontar. Nenhum texto foi enviado.
- Janela: o painel mostra que o prazo oficial não é calculado no navegador. Um lease segurou a fila. `POST /simulation/expire-window` recuou `messages.trusted_at` 25 horas, soltou o lease e avançou o relógio local das URLs. O texto livre terminou `cancelled`. O status da simulação continuou com zero chamadas ao stand-in. O `clock_timestamp()` do PostgreSQL não é adulterado.
- Opt-out: a concessão de `cobranca` fica válida durante o catálogo. A revogação é um passo posterior. Ela cancelou os templates proativos já enfileirados; o texto livre seguia `queued`. Uma tentativa nova, depois da revogação, respondeu 422 com detalhe `política de canal recusou o envio: consent_required`.
- Catálogo: `POST /simulation/close-window` recua a âncora 25 horas e não solta o lease nem avança as URLs. Com esse consentimento ainda concedido, o template driver `reinicio_simulacao` / `sim-driver-reinicio` entrou na fila (201, estado `queued`). Sincronizar pelo admin troca o snapshot pelo `FakeCatalog` explícito. O POST de `sim-fake-aviso` respondeu 422 com detalhe `política de canal recusou o envio: template_not_authorized`. O corpo do problema deixa de ser só o status. Isso não consulta a Graph.
- Anexos: HTML nomeado `foto.jpg` mostrou `anexo inválido`. O PDF `nota.pdf` validou, o download autorizado respondeu 200 com `X-ChatISP-Simulation: local-attachment`, o agente sem setor recebeu 403 e, depois do relógio da fixture, a mesma URL respondeu 410.

`go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo` passou nesta rodada (`acceptance` 17.918s, `m2demo` 16.581s). `TestM4AIntegrated` cobre a mesma jornada no HTTP, inclusive outro tenant no download, decisão `dispatch`/`window_closed` e zero chamadas ao stand-in. Antes do template fake, a linha de `contact_consents` continua concedida e a âncora inbound já tem mais de 24 horas. O template driver grava decisão `enqueue`/`approved_template` e HTTP 201. O fake devolve o detalhe `template_not_authorized`. A revogação, num passo seguinte, grava `cancelled`/`consent_revoked` na tentativa que estava na fila e o POST seguinte devolve o detalhe `consent_required`. `TestM4AFixtureWiring` exige o token de simulação e lista respostas com a sessão do admin. O contrato M3A continua sendo fila 201 e falha no despacho, antes de qualquer driver. Os dois chamadores de `mapDecision` estão em `admit`. `contact_blocked` devolve nil só para essa fila permanecer HTTP 201; `ErrForbidden` de setor e principal continua antes desse caso, e `consent_required` continua `PolicyDenied`, relido em `evaluateProactive`. `EvaluateAttemptPolicy` não cancela esse motivo. `DispatchOutbound` relê `contacts.blocked` quando a origem é humana e grava a tentativa `sending` como `failed` antes de `SendText` ou `SendTemplate`. `TestM3AIntegrated` exige esse 201, zero chamadas ao stand-in e estado `failed`. Recusar o bloqueio na aceitação responderia 403 e falharia esse teste; essa recusa não é o contrato. Os testes M3A desse pacote passaram nesta rodada; isso não fecha o marco M3.

## O que foi reutilizado

- Playwright 1.55.1 e o desenho de `web/scripts/m3b-e2e.mjs`: afinidade `taskset` 0,1, `GOMAXPROCS=1`, Vite em `127.0.0.1:5173`, proxy TLS em `https://localhost:5174`. `web/playwright.config.ts` continua com `testMatch` só de M3B, para um `playwright test` solto não disparar M4A sem o harness. O runner M4A importa `runM4A` no mesmo processo.
- O arranque de socket do cenário M3B, para o Chromium headless não deixar o painel com conexões presas.
- Espera do bundle do Vite antes da navegação. O `npm ci` apaga o cache do optimizer; navegar no meio da reescrita derruba o renderer único.
- PostgreSQL embutido de `backend/tools/pg` e o demo `m2demo`, inclusive atores M3B. M4A acrescenta admin, agente sem setor, outro tenant, lease, template driver e o controle loopback.
- Contratos já publicados de respostas, política, anexos e horário. O roteador central, OpenAPI, dbgen e o schema TypeScript não foram editados.

## Checks desta rodada

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`: passou. O npm avisou que o postinstall de `@embedded-postgres/linux-x64` não está na lista de scripts; o binário já hidratado em `native/bin/postgres` foi o que os testes usaram.
- `npm ci --prefix web --no-audit --no-fund`: passou, 537 pacotes.
- `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo`: passou nesta rodada (`acceptance` 17.918s, `m2demo` 16.581s), com `TestM3AIntegrated` e `TestM4AIntegrated`.
- `npm --prefix web run test:e2e:m4a`: passou, sem skip, no Chromium 140.0.7339.186 via `chromium-launcher`. O runner espera o bundle do Vite antes de navegar. A consulta de horário só começa depois do PUT, do refetch da versão gravada e do GET da API; cada decisão repete com `toPass`. Uma rodada anterior falhou antes da fixture porque o cache em `/tmp/.cache/ms-playwright` não tinha o binário; o launcher desta árvore e o fallback do cache da conta cobrem esse caso sem download. Outra rodada viu `Dentro do expediente.` sumir porque o formulário remonta ao mudar a versão.

## Backlog que permanece

- M3: auto-reabertura X e documentos CPF/CNPJ autorizados.
- M4B: pipeline de mídia outbound, áudio gravado e conversão ffmpeg. Não há player, duração nem Meta real. Arrastar arquivo, colar imagem e emoji não foram exercitados neste browser.
- M5: round-robin, presença e triagem. O horário desta entrega não distribui conversa nem envia os textos gravados.
- Produto, horário: em `web/src/features/businesshours/hours-settings.tsx`, `HoursForm` usa a chave `version:configured`. Depois de gravar, `setQueryData` (e um refetch posterior) troca a versão, o formulário remonta e o estado local zera o instante avaliado e a decisão. Esta entrega não altera esse componente; o E2E espera a versão estável e repete a consulta. A limpeza do instante/decisão no remonte continua pendência de produto.
- Integrações, relatórios e M8.

Simulações identificadas: stand-in Meta loopback, `FakeCatalog` explícito, template driver semeado na fixture, armazenamento local e o relógio que recua a âncora e a URL. Nenhuma chamada externa foi feita pelas provas que passaram.
