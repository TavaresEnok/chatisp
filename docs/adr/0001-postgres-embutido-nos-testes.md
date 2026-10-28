# ADR 0001 — PostgreSQL embutido nos testes

- Status: aceita para este repositório
- Data: 28/10/2026
- Escopo: somente testes automatizados
- Relação com a especificação: exceção operacional a B.3.4, que cita testcontainers-go. Produção continua em PostgreSQL 18 externo. B.6 pede problema, alternativas, custo, operação e reversibilidade.

## Problema

Os testes de RLS, de chave composta e de concorrência precisam de PostgreSQL real. O host de desenvolvimento não tem Docker, não tem Podman, não tem PostgreSQL instalado e não tem root para instalar serviço ou testcontainers. Sem um banco real, o critério do M1 (“dois tenants isolados em banco real”) não tem como ser exercido aqui.

Há um segundo limite no mesmo host: os binários `xz` e `unzip` não existem. Um Postgres de teste empacotado em `.txz` ou dependente do comando `unzip` não sobe.

## Alternativas

1. testcontainers-go com imagem PostgreSQL 18. É o caminho escrito em B.3.4. Exige Docker. Neste host é inviável. Continua válido em CI ou em outra máquina que tenha Docker, atrás da mesma variável de conexão.
2. PostgreSQL instalado no sistema, com papel de superusuário local. Exige root e pacote de sistema. Foi descartada neste host.
3. Banco em memória ou SQLite. Não implementa RLS, `FORCE ROW LEVEL SECURITY` nem o isolamento exigido. Foi descartada.
4. `embedded-postgres` iniciando PostgreSQL 18.6.0 só dentro do processo de teste. Não pede Docker nem root. É a alternativa adotada quando não há URL externa.
5. Servidor PostgreSQL externo apontado por `CHATISP_TEST_DATABASE_URL`. Cobre CI com serviço de banco e qualquer desenvolvedor que já tenha um Postgres 18. Convive com a alternativa 4.

A biblioteca prevista para a alternativa 4 é `github.com/fergusstrange/embedded-postgres/v2`. Ela exige Go 1.26 ou mais novo; o host tem Go 1.27.3. A leitura do README dessa linha em 28/10/2026 mostra o atalho `V18` pinado em 18.6.1, não em 18.6.0. O plano aprovado fixa 18.6.0. A distribuição 18.6.0 existe em artefato `.tar.gz` (gzip) no projeto de binários que essa linha v2 utiliza. O teste deve selecionar 18.6.0 de forma explícita e recusar o default da biblioteca. Gzip é extraível pela biblioteca padrão do Go, sem `xz` e sem `unzip`. A linha v1 da mesma biblioteca, baseada em pacotes que historicamente chegam como JAR ou `.txz`, não é a escolha.

## Decisão

- Testes que precisam de PostgreSQL sobem a versão 18.6.0 por embedded-postgres apenas no código de teste.
- O processo da aplicação (`backend/cmd/chatisp`, modos `api`, `worker`, `migrate` e `all`) não importa essa biblioteca e não leva o binário embutido para produção.
- Se `CHATISP_TEST_DATABASE_URL` estiver definida, os testes usam essa URL e não iniciam o Postgres embutido. É o encaixe para CI e para servidor externo.
- Se a variável estiver ausente, o helper de teste sobe 18.6.0, aplica as migrações e apaga o diretório temporário ao final.
- O runtime dos testes de domínio usa os papéis de B.4.4 (`chatisp_owner`, `chatisp_app`, `chatisp_platform`, `chatisp_ingress`), com `NOBYPASSRLS` no papel de aplicação. O superusuário do cluster de teste só cria papéis e extensões. Ele não é o papel das consultas de domínio.
- Produção e o Compose continuam com PostgreSQL 18 administrado fora do binário. O patch 18.6.0 é pin do teste, não um segundo servidor de produção.

## Custo

- Dependência de teste e download único do binário para o cache do usuário (`os.UserCacheDir()/embedded-postgres/v2`, ou o cache que o helper configurar). Há uso de disco e alguns segundos de partida por pacote de testes.
- A primeira execução precisa de rede, salvo cache já presente ou URL externa.
- CI com `CHATISP_TEST_DATABASE_URL` evita o download e o tempo de subir o embutido.
- Não há processo extra em produção, não há root e não há licença adicional além da licença do próprio PostgreSQL e da biblioteca de teste.
- Conferir no M1 se o binário publicado de 18.6.0 carrega na glibc deste Debian. Se não carregar, o caminho da variável externa cobre o host até o artefato compatível existir. Isso não autoriza trocar para SQLite.

## Operação

- Helper único, compilado só com os testes. Porta efêmera (`Port(0)` ou equivalente), dados em diretório temporário, encoding UTF8.
- A versão passada ao helper é 18.6.0. Um log ou uma consulta `SHOW server_version` no teste de fumaça deve falhar se o processo subir 18.6.1 por default.
- Extração do arquivo compactado fica dentro do Go, a partir de gzip. Os testes não invocam `xz` nem `unzip`.
- `CHATISP_TEST_DATABASE_URL` é lida no início da suíte. Valor presente desliga o embutido por completo. A URL não entra em repositório, fixture versionada ou log de aplicação.
- Migrações goose rodam contra o banco escolhido. O teste que exige RLS conecta em seguida como `chatisp_app`.
- Falha ao subir o embutido é falha de teste, não um skip silencioso. Skip só existe se o pacote declarar, com mensagem, que aquele caso não precisa de banco.
- Backup, restore e pgBackRest continuam assuntos de produção e do M8. O embutido não substitui esse ensaio.

## Reversibilidade

A decisão está isolada no helper de teste e numa variável de ambiente. Para voltar a testcontainers onde Docker existir, o helper passa a ignorar o embutido quando a URL vier de um contêiner, ou um build tag de CI troca a implementação. Esquema, SQL e papéis não dependem do método de subir o servidor.

Remover a biblioteca não pede migração de dados reais: produção nunca a executa. Mudar o patch de 18.6.0 para outro 18.6.x, se o artefato 18.6.0 sair de publicação, é uma revisão curta deste ADR, ainda em PostgreSQL 18 e ainda só nos testes.
