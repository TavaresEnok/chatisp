# Perfil sintético local

Isto não é a D2-07. Não autoriza k6, não publica capacidade e não define RPO nem RTO do piloto.

| Campo | Valor deste ensaio |
| --- | --- |
| Aceite | `local-sintetico` |
| Banco | PostgreSQL 18.4 embutido do teste, socket Unix, diretório descartável |
| Alvo | somente o listener `127.0.0.1` que o próprio teste abriu |
| Processo | um `chatisp` em modo `all` |
| Usuário | um atendente sintético criado no banco descartável |
| Concorrência | 8 clientes |
| Pedidos | 25 por cliente, 200 no total, alternando `GET /healthz` e `GET /readyz` |
| Login | um `POST /api/panel/session` antes da rajada, para o processo ser o de atendimento |
| Meta, ERP, Evolution | não são chamados |

O teste falha se `CHATISP_DATABASE_URL`, `CHATISP_TEST_DATABASE_URL` ou `CHATISP_LOAD_URL` estiver definida, e se o PostgreSQL usado não for o embutido. Não há argumento de URL.

Comando, na raiz:

```
./scripts/ops-load-local.sh
```

O relatório sai no log do teste. `aceito_como_capacidade` permanece falso. p95, RSS, E/S e ticks de CPU são medidas desse processo na janela da rajada. A E/S é a do processo do teste, não a do PostgreSQL. Zero nessa janela significa que o contador do processo não avançou; não é capacidade de disco.

## Como fechar a D2-07

Quem opera o piloto escreve, fora deste arquivo, o perfil real: quantidade de provedores, atendentes simultâneos, números, mensagens, mídia, retenção, p95, RPO e RTO. O lugar previsto é a resposta da D2-07 em `docs/perguntas.md`. Enquanto essa resposta não existir, k6 não roda e nenhum número medido aqui entra em oferta ou em relatório de capacidade.

## Base k6 posterior — 06/11/2026

O [roteiro parametrizado de laboratório](k6-lab.md) foi adicionado depois deste ensaio. O binário k6 foi executado somente contra um servidor HTTPS falso para verificar protocolo, TLS, guardas e encerramento de sessão. Isso não é execução de carga contra o ChatISP, perfil do piloto, medição de capacidade nem definição de metas D2-07. As limitações deste documento continuam válidas.
