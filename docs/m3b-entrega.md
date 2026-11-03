# Entrega M3B — integração e navegador

Base desta worktree: `2fd88fc4f7e5fa9e1b96830d2d8e3fa13b80714c` (integração posterior à referência M3A `5d0db868`). Os arquivos desta tarefa estão sem commit para revisão do controlador.

O comando opt-in `go -C backend run ./cmd/m2demo m3b` adapta a demo M2: PostgreSQL 18.4 descartável, API, workers, Meta falso e setores reais. Cria dois agentes (origem; origem e destino) e supervisor, com sessões reais de garantia completa. A saída JSON contém exclusivamente credenciais efêmeras desta fixture e deve ficar em arquivo privado, nunca em logs de CI. O modo `demo` anterior mantém seu comportamento.

O runner abre Vite em `127.0.0.1:5173`, API em `127.0.0.1:8080`, métricas em `127.0.0.1:9090`, Meta falso em `127.0.0.1:18081` e proxy TLS em `https://localhost:5174`. O certificado é gerado em memória e descartado. Cookies `__Host-session`, CSRF, origem, tenant e política de setores continuam ativos. Recusa DSNs preexistentes e portas ocupadas. Não usa credenciais externas.

```sh
export PATH=/home/enok/.local/share/go1.27.3/bin:/usr/local/go/bin:/home/enok/.local/share/node24/bin:$PATH
mkdir -p .paseo-build-tmp
export GOTMPDIR="$PWD/.paseo-build-tmp" TMPDIR=/tmp
npm ci --prefix backend/tools/pg --no-audit --no-fund
npm ci --prefix web --no-audit --no-fund
go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo
npm --prefix web run test:e2e:m3b
```

Playwright é fixado em `1.55.1`, com lockfile. Seu manifesto exige Chromium `140.0.7339.186`, revisão `1193`. O runner prioriza `M3B_CHROMIUM_EXECUTABLE`, depois o launcher relativo à raiz `.paseo-build-tmp/browser/chromium-launcher`, e por fim o caminho padrão Playwright. Valida permissão de execução e usa o mesmo caminho absoluto no preflight e no Chromium do cenário. O cenário roda no próprio processo do runner, com afinidade `taskset` nas CPUs 0 e 1, porque o cgroup deste runner limita a contagem de threads. O provisionamento do navegador, bibliotecas (inclusive NSS) e fontes é externo. Sem o executável, o comando falha e não baixa outro browser. O cleanup remove somente a fixture temporária, preservando `browser`. Não há instalação automática, skip, servidor externo ou fixture falsa de fallback. Vitest inclui somente testes em `src`; Playwright usa configuração e tsconfig separados.

O cenário central em `web/e2e/m3b.spec.ts` foi escrito para três contextos reais e dois setores: webhook assinado → worker → fila; assumir e limite de uma vaga; nota privada e leitura individual; transferência para agente e devolução liberando capacidade; Ctrl+Enter, resposta HTTP perdida após commit, rede offline, preservação de draft e intenção e repetição idêntica; transferência de setor, remoção visual e do QueryClient da origem, busca/histórico/eventos novamente autorizados, supervisor autorizado, resposta aceita pelo Meta falso e encerramento. As sessões são semeadas pela fixture, sem simular respostas de autenticação. A perda de resposta é induzida após a requisição real. Traces e screenshots de falha ficam em `.paseo-build-tmp/m3b-browser` e podem conter dados exclusivamente simulados.

## Evidência do cenário (03/11/2026)

HEAD: `2fd88fc4f7e5fa9e1b96830d2d8e3fa13b80714c`, alterações ainda sem commit. Launcher relativo `.paseo-build-tmp/browser/chromium-launcher`. Preflight: Chromium `140.0.7339.186`. A execução final de `npm --prefix web run test:e2e:m3b` terminou com código 0, sem skip. O anexo `.paseo-build-tmp/m3b-browser/m3b-evidence.json` registra três contextos, dois setores, protocolo `202611-000001`, cursor `2` → `16`, repetição idêntica da intenção e cache revogado.

O fluxo exercitado no navegador: receber, assumir, nota privada, leitura individual, capacidade de uma vaga, transferência para atendente, devolução à fila, Ctrl+Enter com resposta perdida depois do commit, queda do socket e reconexão com draft e chave preservados, transferência de setor, sumiço visual e do cache de quem perde o setor, histórico HTTP 403 sem o texto, busca vazia, eventos sem o agregado e sem o texto, supervisor ainda vê a conversa, encerramento com motivo. PostgreSQL, API, workers, Meta falso e Vite são da fixture local.

Checks desta rodada, sem skip:

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`: passou, um pacote.
- `npm ci --prefix web --no-audit --no-fund`: passou, 537 pacotes.
- `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo`: passou; `acceptance` 12,313s e `m2demo` 11,703s.
- `npm --prefix web run test:e2e:m3b`: a execução final passou. Tentativas anteriores desta retomada falharam e foram corrigidas no runner e no cenário: o cgroup de 256 threads exigiu afinidade em duas CPUs; o histórico revogado responde 403, como o serviço já prova.

Revisão independente e o gate amplo de 13 comandos não foram executados aqui. M3 não está 100% concluído.

## Histórico anterior ao staging (31/10/2026)

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`: passou; uma dependência instalada.
- `npm ci --prefix web --no-audit --no-fund`: passou; 537 pacotes instalados.
- `node web/node_modules/typescript/bin/tsc -p web/tsconfig.e2e.json`: passou; repetido com sucesso após acrescentar a observação explícita do snapshot de reconexão.
- `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo`: passou (`acceptance` 13,705s; `m2demo` 10,289s), incluindo as provas M2/M3A existentes e `TestM3BFixtureSessions` com sessões reais dos três atores. Uma execução do comando.
- `npm --prefix web run test:e2e:m3b`: **falhou, sem skip**, antes do cenário. Erro real: `browserType.launch: Executable doesn't exist at /home/enok/srv_projetos/runtime/isolated/codex-programmer/home/.cache/ms-playwright/chromium_headless_shell-1193/chrome-linux/headless_shell`. O cache informado do host `/home/enok/.cache/ms-playwright` também não existe no runner. Bibliotecas ainda não verificadas, pois não há executável para lançar. Necessário provisionamento/staging pelo controlador; nenhum sudo ou instalação de serviço foi feito pelo executor.

Nessa data o cenário browser não chegou a ser executado. Não houve aceite Playwright, aprovação independente ou gate final nessa tentativa. As provas anteriores de política, corridas de capacidade e leitura permanecem nos testes de service (`assignment_bridge_test.go`, `assignment_test.go`, `annotations_test.go`, `queue_test.go`) e no aceite M3A; sua existência não substitui o gate browser e não significa que foram reexecutadas nesta tarefa.

Na retomada de 31/10/2026, no mesmo HEAD acima, o comando `npm --prefix web run test:e2e:m3b` foi repetido uma vez e voltou a falhar com código 1 pelo mesmo executável ausente. Tanto `/home/enok/.cache/ms-playwright` quanto `/home/enok/srv_projetos/runtime/isolated/codex-programmer/home/.cache/ms-playwright` continuam ausentes; `PLAYWRIGHT_BROWSERS_PATH` não está definido. O manifesto instalado confirma revisão 1193 / versão 140.0.7339.186. Nenhum cenário foi iniciado e as bibliotecas ainda não puderam ser verificadas. O controlador precisa disponibilizar esse executável e suas bibliotecas na conta isolada ou fornecer um binário compatível via `M3B_CHROMIUM_EXECUTABLE`. Os checks npm/Go/TypeScript aprovados da tentativa anterior foram reutilizados, pois não houve alteração funcional nesta retomada. `git diff --check` passou novamente.

Nova verificação na retomada de 31/10/2026: uma execução adicional de `npm --prefix web run test:e2e:m3b` terminou com código 1, antes da fixture, pelo mesmo `chromium_headless_shell-1193/chrome-linux/headless_shell` ausente. Os dois caches seguem inexistentes, sem `M3B_CHROMIUM_EXECUTABLE` ou `PLAYWRIGHT_BROWSERS_PATH`; `/usr/bin/chromium` e `/usr/bin/google-chrome` também não existem. O manifesto instalado continua exigindo Chromium 140.0.7339.186 / revisão 1193. Sem mudança funcional, os checks anteriores aprovados foram reutilizados. O provisionamento continua necessário; nenhum browser foi instalado pelo executor.

M3 não está 100% concluído: auto-reabertura X e CPF/CNPJ autorizado com cifra/HMAC seguem pendentes. Os demais itens M3–M8 permanecem no backlog aprovado. Sem round-robin M5 ou integrações reais Meta/ERP/Evolution. Revisão independente e gate amplo herdado de 13 comandos são fases posteriores do controlador.
