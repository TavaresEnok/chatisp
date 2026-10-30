# M2: demonstração e evidências

A partir da raiz da worktree, instale as dependências e inicie a fixture opt-in:

```sh
npm ci --prefix backend/tools/pg --no-audit --no-fund
npm ci --prefix web --no-audit --no-fund
go -C backend run ./cmd/m2demo demo
```

O comando cria um cluster PostgreSQL 18 novo e descartável, configura uma caixa **SIMULAÇÃO Atendimento**, inicia API e worker River reais e um Meta falso em loopback. Recusa variáveis de DSN existentes, inclusive `CHATISP_TEST_DATABASE_URL`. As portas 8080, 9090, 18081, 5173 e 5174 devem estar livres. O PostgreSQL embutido precisa executar como usuário sem privilégios de root. Não há chamadas externas.

O terminal mostra o e-mail e senha transitórios do agente e o comando `receive` com os identificadores e segredo fictícios gerados. Em outro terminal:

```sh
npm --prefix web run dev -- --host 127.0.0.1 --port 5173 --strictPort
```

Abra http://127.0.0.1:5174, faça login com as credenciais exibidas e execute o comando `receive` exibido em outro terminal. O proxy local do demo encaminha o painel ao Vite e a API, incluindo WebSocket, ao servidor real. Na caixa SIMULAÇÃO, abra a conversa, assuma, responda e encerre informando motivo, resultado e síntese. O painel mostra o histórico e os estados disponíveis. O Meta falso aceita o envio, mas não gera automaticamente recibos delivered/read. O segredo fictício aparece na linha de comando; nunca use segredo real. Ctrl+C no demo encerra o worker e remove apenas o cluster criado; os dados desaparecem. O modo anterior `go -C backend run ./cmd/m2demo` continua oferecendo apenas o Meta falso, e `receive` continua disponível separadamente.

`backend/internal/acceptance/vertical_test.go` cobre PostgreSQL 18, webhook assinado, persistência do envelope após ACK, duas caixas por tenant, mensagem e histórico em duas caixas, isolamento, assumir, resposta idempotente, River, recibo antecipado, delivered/read e encerramento. A sessão administrativa de fixture tem MFA confirmado; as provas dirigidas de bloqueio durante inscrição estão em `backend/internal/platform/httpapi/mfa_test.go` e não foram reexecutadas aqui. `backend/internal/attendance/pipeline/pipeline_test.go` cobre recuperação após despacho, e `backend/internal/realtime/realtime_test.go` cobre snapshot e cursor. O teste RTL está em `web/src/test/m2-attendance.test.tsx`. Nenhum navegador real foi exercitado nesta entrega; as evidências são HTTP e RTL.

Base: `789e9b131701b24c9fe71393b04153de9f22d4e3`, com alterações locais ainda não commitadas. Revisão independente e regressão ampla final do controlador permanecem pendentes. Credenciais e Graph real dependem de D2-03 e autorização; não há ERP ou Evolution nesta demonstração.

Os checks essenciais desta entrega são `npm ci --prefix backend/tools/pg --no-audit --no-fund`, `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo`, `npm ci --prefix web --no-audit --no-fund` e `npm --prefix web test -- src/test/m2-attendance.test.tsx`. O teste do demo verifica prontidão, login HTTP real e upgrade WebSocket autenticado pelo proxy local.
