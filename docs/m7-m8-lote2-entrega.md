# Entrega lote 2 M7/M8 — aceite local integrado

Data: 04/11/2026. Este recorte não conclui M7, M8, V1 nem o piloto. Não houve deploy, produção, dados reais, conectores reais, API paga nem expurgo.

A base já integrada é o HEAD `76802c6568de8ccca117616558ad918bae43ab54`. A jornada abaixo roda em cima desse commit, com o modo `m2demo m7` ainda não commitado nesta worktree.

## O que esta rodada executou

Chromium `140.0.7339.186` pelo launcher instalado `/opt/paseo-browser/chromium-launcher`. Não houve download de navegador. `npm --prefix web run test:e2e:m7-ops` terminou com código 0 e a frase `M7-OPS E2E passou (simulação local, sem skip)`.

Há duas execuções distintas. Os números 41709, 38969, 45107 e 36857 citados numa versão anterior deste arquivo não estão em artefato nenhum e deixam de valer.

Execução A, artefato lido antes da correção do orçamento, carimbo local do arquivo `2026-11-04 14:19`, modo `0600`. O JSON `.paseo-build-tmp/m7-ops-browser/m7-ops-jornada.json` dessa leitura traz `code` `76802c6568de8ccca117616558ad918bae43ab54`, Chromium `140.0.7339.186`, API `http://127.0.0.1:43531`, métricas `http://127.0.0.1:39117/metrics` e painel `https://127.0.0.1:41861`. Não registra a porta do Vite. Um diretório residual `.paseo-build-tmp/m7-lnTzsx` (14:05) tem outras portas, 41747, 35737 e 37129, e não é essa jornada.

Execução B é a desta correção, com o prazo do launcher já no script. O comando `npm --prefix web run test:e2e:m7-ops` saiu 0 e imprimiu `M7-OPS E2E passou (simulação local, sem skip)`. Chromium `140.0.7339.186` em `/opt/paseo-browser/chromium-launcher`. O log do processo trouxe Vite `http://127.0.0.1:40997`, API `http://127.0.0.1:40619`, métricas `http://127.0.0.1:36661/metrics` e painel `https://127.0.0.1:33003`. O anexo substituiu o JSON. O arquivo agora em `.paseo-build-tmp/m7-ops-browser/m7-ops-jornada.json` repete o mesmo `code`, o mesmo Chromium, a API 40619, as métricas 36661 e o painel 33003. A porta do Vite continua só no log, não no JSON. O log também trouxe certificado desconhecido, `http: proxy error: context canceled` e `falha ao ler sessão`; a jornada concluiu.

O banco é o PostgreSQL embutido do `m2demo`. A fixture não aponta para banco existente, não usa credencial real e não para serviço do host. O fuso do recorte é `America/Sao_Paulo`, no dia civil `2026-11-04`.

O que o Chromium mostrou, só com dados sintéticos:

- O admin gravou o nome `Provedor M7 Simulado` e um PNG local de 1×1. O shell mostrou o nome e o logo. O título da página passou a terminar com esse nome.
- O agente não viu o editor de identidade. O `PUT /api/panel/branding` com o CSRF dele retornou 403. O nome gravado permaneceu.
- O relatório sem filtro mostrou Caixa Alfa M7 com 2 ciclos e 4 mensagens e Caixa Beta M7 com 1 ciclo e 1 mensagem, na mesma hora do mapa. O filtro de setor Alfa ficou em 2/4. Setor Alfa com a caixa Beta ficou vazio. Sem setor e com a caixa Beta ficou em 1/1. Os CSV baixados repetiram `resumo`, `caixa` e `calor` desses recortes e não trouxeram a outra caixa.
- O agente carregou os dois recortes porque a semente o vinculava aos dois setores. A fixture então revogou, num HTTP só de loopback, o vínculo com o setor Beta. O select do painel, ainda com a opção carregada, pediu esse `sector_id`. A API respondeu 403. A grade anterior e o botão "Baixar CSV" saíram da tela, com o texto "Acesso negado.".
- Em Conversas, a caixa Alfa abriu um WebSocket. O scrape privado mostrou `chatisp_websocket_connections` 1, `chatisp_river_collect_ok` 1, a série `chatisp_river_jobs` da fila `ingress` em `available` com 0, e `chatisp_db_pool_max_connections` para `app`, `platform`, `ingress` e `jobs`. O corpo não continha DSN, senha, e-mail, segredo, token nem nome de banco. `GET` da API em `/metrics` não serviu essas séries.

O launcher `web/scripts/m7-ops-e2e.mjs` chama a jornada direto, fora do runner do Playwright, e por isso não herda o timeout do projeto. Ele próprio corta preparação e jornada aos 330s, contados do início do processo. Nesse vencimento fecha só os browsers que abriu, manda SIGTERM e, se preciso, SIGKILL aos filhos que ele mesmo criou, apaga o diretório da fixture e sai com falha. Um limite duro de 350s encerra o processo se a limpeza passar da margem. Os dois ficam abaixo dos 360s do comando. O projeto `m7-ops` em `playwright.config.ts` também está em 330s; o launcher não lê esse campo. Sinal externo segue o mesmo encerramento dos recursos próprios.

Os projetos `m3b` e `m4a`/`m4b` do Playwright permanecem. O E2E de mídia e de ERP não foi repetido: esta rodada não muda essa área.

## Checks desta rodada

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`: passou, 1 pacote.
- `npm ci --prefix web --no-audit --no-fund`: passou, 537 pacotes.
- `go -C backend test -count=1 ./cmd/m2demo/`: passou nesta correção, `ok chatisp/cmd/m2demo 9.010s`. O pacote não mudou; o comando faz parte dos checks desta rodada.
- `npm --prefix web run test:e2e:m7-ops`: passou, código 0, na execução B (portas 40619, 36661 e 33003). A execução A não foi repetida; o arquivo dela foi substituído pelo anexo desta.
- `npm --prefix web run build`: passou, `tsc -b` e Vite, 2437 módulos, built in 9.19s. O aviso de chunk acima de 500 kB já existia no build; não é falha.

A suíte ampla de fechamento do plano não rodou aqui. Ela continua no `final_tests`.

## Reuso, sem nova execução

Estas entregas já estão no HEAD e não foram reimplementadas nem reauditadas:

- `f6183fc` (merge `76802c6`): nome e logo do provedor.
- `51888f4` (merge `e0327d3`): séries de pool, WebSocket e River.
- `eacd1af` (merge `f7c0bee`): filtros, grade e mapa de calor do relatório.
- `ccabd76` (merge `2407824`): ensaio de restore com conteúdo e chaves.
- `2c6744e`: correção do lote 1 sobre a qual o lote 2 foi integrado.

O ensaio de objetos e crypto não foi executado de novo neste runner. A prova reutilizada é o resultado da tarefa `m8-restore-content-keys`, sessão Grok `01a1a69c-18df-7103-9ea8-dbb00be3f268` na worktree `task-eec2118bdf14-m8-restore-content-keys`, em 04/11/2026. Não há segundo log de orquestração além dessa sessão. O comando foi `go -C backend test -count=1 ./internal/restoredrill/ ./cmd/restoredrill/`. Saiu com código 0: `ok chatisp/internal/restoredrill 8.222s` e `ok chatisp/cmd/restoredrill 7.983s`. Na mesma sessão, antes, `npm ci --prefix backend/tools/pg --no-audit --no-fund` saiu 0 (1 pacote) e, depois, `go -C backend vet ./internal/restoredrill/ ./cmd/restoredrill/` saiu 0. O código testado partiu de `2c6744e968aa1fcc4a04084efccad2b8681c8de1` e foi integrado em `ccabd76b91996f626f436c0e5d8b3bf989346f54` (merge `2407824899d876898c1d5ab48a335d5cf67e32f7`). Depois desse `go test` a sessão só alterou uma frase de `docs/contracts/m8-restore-drill.md` e o `.paseo-result.json`; os pacotes Go do ensaio não foram editados de novo.

Esse `ok` do pacote inclui `TestDrillRestoresDumpAndReapplies`, que falha se `ContentReadable`, `KeyRecovered` ou `SecretDecrypted` forem falsos, e inclui `TestRestoreObjectsAfterSourceIsGone` e `TestRecoveredRingDecryptsAfterSourceIsGone`. O backup anterior desta linha aceitava conferir a referência (`object_key` e `object_version`) sem esses três resultados. A falha antiga da baseline, "diretório de dados do checkout real", é outro código, anterior a `ccabd76`, e não é o resultado desta prova. A duração medida no relatório do ensaio não é RPO, RTO, k6, p95 nem capacidade de piloto.

## Dependências externas

Chromium já instalado no caminho acima. PostgreSQL embutido do teste. Node que já estava no runner. Nenhuma API paga, nenhum serviço de produção e nenhum banco pré-existente.

## Backlog que este aceite não fecha

- Privacidade, exportação completa e exclusão continuam condicionadas à política. O expurgo permanece desligado. Nada foi apagado além do vínculo sintético da fixture.
- Carga e metas do piloto não foram medidas.
- Faltam sinais e alertas de observabilidade além das séries de pool, WebSocket e River exercidas aqui.
- Segurança operacional e o guia de operação não foram escritos nem aceitos.
- Meta, ERP, Evolution e qualquer conector real continuam fora de escopo.
