# Briefing técnico do ChatISP para construir ou redesenhar o frontend

Base examinada: commit `896cc93`, repositório `/home/enok/Documentos/projetos/chatisp`. Documento produzido por leitura dos manifests e código; não é teste novo da instalação pública. Versões com `^` são intervalos declarados; `web/package-lock.json` fixa a resolução. Não atualizar bibliotecas automaticamente para seguir outro tutorial.

Atualização de entrega: o pacote inclui também a área administrativa de downloads no Painel e os contratos `/api/panel/packages`. O build usa Go 1.27.4, com correção de segurança da biblioteca HTTP. A referência de commit acima identifica o levantamento inicial, não limita os arquivos incluídos neste pacote regenerado.

## 1. Produto e arquitetura

ChatISP é uma central própria de atendimento para provedores de internet, com múltiplos atendentes, setores, caixas, protocolos e histórico. O foco de canal da V1 é WhatsApp. Há isolamento entre provedores (multi-tenant) e papéis Administrador, Supervisor e Atendente, além de administração da plataforma. O painel web é o posto de trabalho da equipe; não existe widget de visitantes entregue.

Arquitetura: monorepo, frontend SPA React/TypeScript, backend monólito modular Go. Em produção, o build estático do frontend é servido pelo Caddy; API e worker executam o mesmo binário Go com comandos distintos. O navegador usa HTTP JSON e WebSocket. PostgreSQL armazena dados e jobs River; Garage fornece armazenamento S3 privado. Node participa do desenvolvimento/build/testes, não é o servidor de negócio. Não introduzir Next.js, SSR, React Server Components ou um backend Node para redesenhar a interface sem uma decisão arquitetural nova.

Fluxo conceitual: navegador → proxy HTTPS/Caddy → API Go → PostgreSQL ou armazenamento S3; API/worker → eventos duráveis e WebSocket → navegador. O ingresso atual possui Nginx central e Apache como proxies antes do Caddy, detalhe de implantação, não dependência de outro produto.

## 2. Frontend: dependências de produto

| Biblioteca | Versão declarada | Uso |
|---|---|---|
| `@fontsource-variable/geist` | `^5.3.0` | Fonte principal local. |
| `@fontsource-variable/geist-mono` | `^5.3.0` | Fonte monoespaçada local. |
| `@hookform/resolvers` | `^6.0.2` | Integração dos formulários com validação Zod. |
| `@tailwindcss/vite` | `^4.3.3` | Integração do Tailwind com Vite. |
| `@tanstack/react-query` | `^5.105.0` | Consultas, cache remoto, invalidação e estados assíncronos. |
| `@tanstack/react-virtual` | `^3.14.23` | Virtualização de listas/histórico. |
| `class-variance-authority` | `^0.7.1` | Variantes de estilo dos componentes. |
| `cn` | `^0.5.0` | Composição de classes CSS. |
| `lucide-react` | `^1.63.0` | Ícones. |
| `openapi-fetch` | `^0.17.0` | Cliente HTTP tipado pelo OpenAPI. |
| `qrcode.react` | `^4.2.0` | QR Code SVG do MFA gerado localmente. |
| `radix-ui` | `^1.8.1` | Primitivas acessíveis usadas pelo design system. |
| `react` | `^19.3.1` | Componentes e renderização da interface. |
| `react-dom` | `^19.3.1` | Montagem da aplicação no navegador. |
| `react-hook-form` | `^7.91.0` | Formulários. |
| `react-router` | `^8.5.1` | Rotas no navegador, BrowserRouter; não é roteamento Next.js. |
| `shadcn` | `^4.23.0` | Base e ferramentas de componentes UI; componentes locais em src/components/ui. |
| `tailwindcss` | `^4.3.3` | Estilos utilitários e tokens CSS. |
| `tw-animate-css` | `^1.4.0` | Utilitários de animação CSS. |
| `zod` | `^4.6.7` | Validação tipada de entradas. |
| `zustand` | `^5.0.16` | Estado local da sessão, identidade e preferências/contexto conforme stores. |

Ferramentas de desenvolvimento e teste:

| Biblioteca | Versão declarada |
|---|---|
| `@eslint/js` | `^10.0.1` |
| `@playwright/test` | `1.55.1` |
| `@testing-library/jest-dom` | `^7.0.1` |
| `@testing-library/react` | `^16.3.3` |
| `@testing-library/user-event` | `^14.6.8` |
| `@types/node` | `^26.6.5` |
| `@types/react` | `^19.3.0` |
| `@types/react-dom` | `^19.3.0` |
| `@vitejs/plugin-react` | `^5.2.0` |
| `axe-core` | `4.10.2` |
| `eslint` | `^10.13.0` |
| `eslint-plugin-react-hooks` | `^7.1.1` |
| `eslint-plugin-react-refresh` | `^0.5.7` |
| `globals` | `^17.13.0` |
| `jsdom` | `^30.1.3` |
| `openapi-typescript` | `^7.13.0` |
| `typescript` | `^5.9.3` |
| `typescript-eslint` | `^8.72.1` |
| `vite` | `^8.3.5` |
| `vitest` | `^5.0.5` |

Node exigido: >=24; package manager declarado: npm 11.19.1. Dockerfile declara Node 24.22.1. TypeScript é estrito, target ES2022, bundler resolution, JSX react-jsx, noUncheckedIndexedAccess, noUnusedLocals e noUnusedParameters. Alias `@/` aponta para `web/src/`.

shadcn está configurado com estilo `radix-nova`, TSX, variáveis CSS, base neutral, ícones Lucide e `rsc: false`. CSS inclui Tailwind v4, tw-animate-css, shadcn e fontes Geist/Geist Mono. Há tokens para superfícies, texto, borda, foco, sidebar e gráficos. Existência de tokens dark ou chart não comprova seletor de tema ou biblioteca de gráficos pronta. Não foram declarados Recharts, Chart.js, Redux, Axios, Material UI ou Framer Motion como dependências diretas neste manifesto.

## 3. Backend: linguagem, frameworks e bibliotecas

Go: módulo `chatisp`, diretiva `go 1.27`; Dockerfile usa toolchain Go 1.27.4. HTTP usa net/http, Chi e Huma. API e regras de autorização ficam no servidor.

| Biblioteca | Versão | Responsabilidade |
|---|---|---|
| github.com/go-chi/chi/v5 | 5.5.0 | Roteamento HTTP |
| github.com/danielgtaylor/huma/v2 | 2.40.0 | Operações HTTP tipadas e OpenAPI |
| github.com/jackc/pgx/v5 | 5.11.0 | Driver e pools PostgreSQL |
| sqlc | 1.31.1, ferramenta | Geração de consultas Go tipadas a partir de SQL |
| github.com/pressly/goose/v3 | 3.29.0 | Migrações SQL |
| github.com/riverqueue/river | 0.50.0 | Jobs duráveis em PostgreSQL |
| riverdriver/riverpgxv5, rivertype | 0.50.0 | Integração River/pgx e tipos |
| github.com/coder/websocket | 1.8.15 | WebSocket no servidor |
| github.com/aws/aws-sdk-go-v2 | 1.41.5 | SDK AWS usado para S3 compatível |
| github.com/aws/aws-sdk-go-v2/service/s3 | 1.97.3 | Objetos e URLs assinadas |
| github.com/dgraph-io/ristretto | 0.2.0 | Cache local controlado de configuração/metadados |
| github.com/caarlos0/env/v11 | 11.4.1 | Configuração por ambiente |
| github.com/google/uuid | 1.6.0 | Identificadores UUID |
| github.com/pquerna/otp | 1.5.0 | TOTP/MFA |
| golang.org/x/crypto | 0.58.0 | Inclui Argon2id para senhas |
| github.com/prometheus/client_golang | 1.25.0 | Métricas técnicas |
| gopkg.in/yaml.v3 | 3.0.1 | Leitura de YAML |

Logs estruturados usam log/slog. Segredos recuperáveis usam AES-256-GCM com versão de chave e contexto de tenant/entidade/campo. A aplicação usa SQL explícito/sqlc, não um ORM geral. Dependências indiretas de ferramentas em go.mod, como drivers de outros bancos, não significam que o ChatISP use esses bancos no runtime. O banco principal é PostgreSQL. As dependências Go são vendorizadas.

## 4. Banco, armazenamento e implantação

- PostgreSQL 18.6 nos perfis Compose atuais; RLS e papéis separados de aplicação, plataforma, ingresso e jobs.
- River usa PostgreSQL; não pressupor Redis ou RabbitMQ como requisito.
- PostgreSQL LISTEN/NOTIFY sinaliza eventos para o serviço de tempo real; eventos duráveis dão suporte à recuperação/ressincronização.
- Garage v2.5.0, S3 privado; URLs assinadas temporárias, controle de acesso, validação de arquivo e quota.
- FFmpeg no runtime para conversão de áudio quando aplicável.
- Caddy 2.10.2 serve o build e encaminha API/storage. Nginx/Apache compõem o ingresso existente.
- Docker e Docker Compose; builds multi-stage. Há bases Debian e perfil Alpine de compatibilidade, que não deve ser confundido com migração de arquitetura.
- pgBackRest e scripts para backup/restore; perfil opcional de Prometheus/Alertmanager. Não presumir coleta/alertas externos ativados.
- Scripts auxiliares em shell e Python; k6 de laboratório separado. Não são frameworks de negócio do frontend.

## 5. Organização do código

```text
web/
  src/main.tsx                entrada React/StrictMode
  src/app.tsx                 providers e BrowserRouter
  src/routes.tsx              rotas
  src/index.css               tokens e estilos globais
  src/api/                    clientes, adaptadores e schema gerado
  src/providers/              sessão e integração com cache
  src/stores/                 estado local
  src/components/ui/          componentes base shadcn/Radix
  src/components/layout/      estrutura e navegação
  src/components/auth/        login, convite, MFA e recuperação
  src/components/settings/    componentes administrativos
  src/pages/                  composição das páginas
  src/features/               módulos funcionais
  src/lib/                    utilitários e chaves de consulta
  src/test/                   doubles e suporte de testes
  scripts/                    jornadas de navegador
backend/
  cmd/chatisp/                API, worker, migração e CLI
  internal/                   módulos e regras
  db/migrations/              esquema e migrações
  db/queries/                 SQL de origem do sqlc
  internal/platform/db/dbgen/ Go gerado por sqlc
  api/openapi.json            contrato do painel
  api/public-openapi.json     contrato da API pública
  sqlc.yaml                   configuração de geração
```

## 6. Integração obrigatória do novo frontend

1. Usar os contratos de `backend/api/openapi.json` e o cliente em `web/src/api/`; não inventar endpoints/campos para completar uma tela.
2. `web/src/api/schema.ts` é gerado por openapi-typescript. Não editar manualmente. Regenerar quando o contrato mudar.
3. A API do painel usa `/api/panel`; a administração da plataforma usa `/api/platform`. A API pública para integrações é um contrato separado, não a substituição do login do painel.
4. O cliente usa a mesma origem do navegador, fetch/OpenAPI e `credentials: include`.
5. Sessão é cookie de servidor, não um JWT para guardar em localStorage. Cookie `__Host-session` é Secure/HttpOnly; CSRF usa `__Host-csrf` e cabeçalho `X-CSRF-Token`. Escritas exigem Origin permitido. Preservar o middleware e o tratamento de perda de sessão.
6. GET `/api/panel/session` restaura o estado. MFA é opcional; quem confirmou fator continua precisando do código. Usar `mfa_enabled` e respeitar o contrato legado `mfa_enrollment_required`.
7. Usar TanStack Query para estado do servidor e stores locais conforme a arquitetura existente. Não copiar toda resposta de API para uma store global duplicada.
8. Chaves/cache precisam respeitar tenant, usuário/sessão e recorte autorizado. Limpar conteúdo protegido na troca/perda de acesso; cancelar requisições antigas e descartar respostas tardias.
9. WebSocket nativo em `/api/panel/ws`, esquema `ClientMessage`/`ServerMessage`. Preservar subscribe, snapshot, resync, heartbeat, reconexão, deduplicação e revogação. Não trocar por Socket.IO por aparência de conveniência.
10. Ações de atendimento têm versões e intenções/idempotência. Preservar conflitos, posse, capacidade, resultado incerto e diferença entre repetir a mesma tentativa e enviar nova mensagem.
11. Não tratar enfileirado/aceito como entregue/lido. Não assumir estado do canal ou consentimento sem dados do servidor.
12. Manter histórico virtualizado, rascunho isolado por contexto e preservação do rascunho ao alternar vistas no celular. Rascunho local não é modo offline completo.
13. Mídia usa Blob/File, APIs do navegador e validação no servidor. Downloads são assinados e expiram. Não colocar chaves S3, credenciais Meta, SMTP ou segredos no bundle.
14. Uma permissão escondida visualmente continua precisando da autorização do backend. O superadmin não ganha leitura irrestrita das conversas.

## 7. Páginas e cobertura visual

Seis áreas: Painel, Conversas, Contatos, Equipes, Relatórios e Configurações. Rotas administrativas: canais, usuários e provedores. Rotas de conta: login, MFA, convite, solicitar recuperação e confirmar recuperação. Preservar todos os destinos de `src/routes.tsx`.

Desktop: fila, conversa e contexto com rolagens adequadas. Celular: alternar fila/conversa/detalhes sem perder contexto; não encolher três colunas. Preservar navegação por teclado, foco visível, rótulos, contraste, diálogos e nomes acessíveis. Históricos e listas devem suportar volume sem renderizar tudo.

Cobrir estados vazios, carregamento, falha/retry, sem permissão, sessão expirada, desconexão/resync, conflito, canal indisponível, perda de posse, limite de capacidade, envio incerto, upload recusado, download expirado e dados sem amostra.

O inventário funcional e a checklist anexos detalham recursos e limites. Reorganizar a apresentação é permitido; remover funcionalidade ou inventar métrica/contrato para produzir uma imagem não é cobertura do produto.

## 8. Integrações e limites reais

Meta Cloud API tem código de integração, sujeito a autorização/configuração/homologação. Evolution e ERP real continuam com implementação parcial. Expurgo automático não tem executor completo. SMTP real precisa ser configurado. Não prometer IA generativa, transcrição, campanhas, outros canais ou widget de visitantes como funções existentes. Não criar servidor de mocks que substitua silenciosamente o backend real na entrega. Fixtures servem apenas para demonstração/teste e precisam ser identificadas.

## 9. Comandos de desenvolvimento e verificação

Na raiz: `./scripts/dev.sh`, `./scripts/test.sh`, `./scripts/lint.sh`, `./scripts/generate.sh`. O launcher de desenvolvimento usa HTTPS para o navegador aceitar cookies Secure; consultar README para portas e requisitos. Não apontar preparo de desenvolvimento para banco ativo.

No frontend: `npm ci --prefix web`, `npm --prefix web run dev`, `npm --prefix web run build`, `npm --prefix web run lint`, `npm --prefix web run typecheck`, `npm --prefix web run test`. O comando Vite isolado pressupõe API preparada; para fluxo completo usar o launcher.

Geração cliente: `npm --prefix web run generate:api`. Jornadas Playwright por módulo estão em package.json. Há testes de interface com Testing Library/Vitest/jsdom e auditorias axe-core. No backend, testes Go, race detector, go vet, golangci-lint e govulncheck. As jornadas de integração usam PostgreSQL real descartável; não trocar essas provas por componentes mockados.

Esses comandos foram identificados no repositório; não foram todos reexecutados para escrever este briefing.

## 10. Instrução pronta para a outra IA

> Trabalhe no frontend existente do ChatISP, uma SPA React + TypeScript + Vite, usando Tailwind v4, shadcn/Radix, Lucide, TanStack Query, Zustand, React Hook Form e Zod. Preserve o backend Go Chi/Huma, PostgreSQL e contratos OpenAPI. Leia primeiro o inventário funcional, a checklist e as rotas/componentes atuais. Proponha arquitetura de informação e componentes reutilizáveis cobrindo todas as páginas e estados, em desktop e celular. Use tokens visuais consistentes e componentes acessíveis. Reaproveite clientes de API, sessão por cookie/CSRF, isolamento de cache, WebSocket, virtualização e regras de envio existentes. Não introduza Next.js, backend paralelo, autenticação simulada ou dependências externas sem justificativa e decisão explícita. Não remova funções para simplificar a tela; não invente dados/endpoints ou integrações concluídas. Se precisar de um campo novo, declare a mudança de contrato necessária. Valide build, tipos, lint, testes de interação e jornadas relevantes, documentando o que foi realmente executado. Não toque em credenciais, dados ou publicação ativa para produzir o novo design.

## Atualização visual grafite — 16/11/2026

O frontend deste pacote inclui o redesenho aprovado no ramo `redesign/grafite-20261115`: tema claro/escuro, menu em pílula, detalhes em abas e páginas split. Leia `docs/design/redesign-20261115/IMPLEMENTACAO.md`, `REVISAO.md` e `VERIFICACAO.md` para distinguir a implementação, os ajustes de revisão e as provas efetivamente executadas. O backend e os contratos funcionais foram preservados. Pacote regenerado durante a revisão do PR 1; não implica publicação automática na instalação ativa.
