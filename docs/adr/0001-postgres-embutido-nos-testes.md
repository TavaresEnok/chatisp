# ADR 0001 — PostgreSQL embutido nos testes

- Status: aceita para este repositório
- Data: 28/10/2026
- Escopo: somente testes automatizados
- Relação com a especificação: exceção operacional a B.3.4, que cita testcontainers-go. Produção continua em PostgreSQL 18 externo. B.3.2 fixa o major 18. B.6 pede problema, alternativas, custo, operação e reversibilidade.

## Problema

Os testes de RLS, de chave composta e de concorrência precisam de PostgreSQL real. O host de desenvolvimento não tem Docker, não tem Podman, não tem PostgreSQL instalado e não tem root para instalar serviço ou testcontainers.

O verificador da fila é mais restrito: os testes rodam em contêiner sem rede, com a raiz somente leitura, `/tmp` em tmpfs de 128 MiB, usuário 1000, `HOME=/tmp` e cerca de 1,5 GiB de RAM. O único comando com rede é `npm ci`, e ele roda com `--ignore-scripts`. Um helper que baixe binários na hora do `go test` não consegue subir o banco. O host também não tem `xz` nem `unzip`.

## Alternativas

1. testcontainers-go com imagem PostgreSQL 18. É o caminho escrito em B.3.4. Exige Docker. Neste host e no verificador é inviável. Continua válido onde houver Docker, atrás da mesma variável de conexão.
2. PostgreSQL instalado no sistema, com papel de superusuário local. Exige root e pacote de sistema. Foi descartada neste host.
3. Banco em memória ou SQLite. Não implementa RLS, `FORCE ROW LEVEL SECURITY` nem o isolamento exigido. Foi descartada.
4. Biblioteca Go `github.com/fergusstrange/embedded-postgres`, que baixa binários zonky na primeira execução e depende de `github.com/xi2/xz`. O download acontece dentro do `go test`, sem rede no verificador. Foi descartada e removida do módulo e do `vendor`.
5. Pacote npm `@embedded-postgres/linux-x64` na versão exata `18.4.0-beta.17`, instalado por `npm ci` em `backend/tools/pg`. O tarball já traz os binários do PostgreSQL 18.4, inclusive `unaccent` e `pg_trgm`. O `npm ci` é o passo que tem rede. É a alternativa adotada quando não há URL externa.
6. Servidor PostgreSQL externo apontado por `CHATISP_TEST_DATABASE_URL`. Cobre CI com serviço de banco e qualquer desenvolvedor que já tenha um Postgres 18. Convive com a alternativa 5.

A versão 18.4, e não um patch 18.6, é a que esse pacote npm publica e a que foi conferida neste host: o processo sobe e oferece `unaccent` e `pg_trgm`. Continua sendo PostgreSQL 18, o major de B.3.2. O minor dos testes fica preso ao pacote npm. Produção não usa esse binário.

## Decisão

- Testes que precisam de PostgreSQL sobem 18.4 a partir dos binários em `backend/tools/pg/node_modules/@embedded-postgres/linux-x64` apenas no código de teste.
- `backend/tools/pg/package.json` é privado e declara a devDependency exata `"@embedded-postgres/linux-x64": "18.4.0-beta.17"`, com `package-lock.json` correspondente.
- O `postinstall` desse pacote recria symlinks de bibliotecas (`libpq`, ICU e afins). O verificador passa `--ignore-scripts`, então o helper Go lê `native/pg-symlinks.json` e cria os mesmos symlinks. A criação é idempotente, relativa ao pacote, recusa caminho que escape do diretório e usa trava exclusiva para pacotes de teste paralelos.
- Se os binários não estiverem no disco, o teste falha com instrução explícita para rodar `npm ci --prefix backend/tools/pg --no-audit --no-fund`. Não há skip silencioso.
- O cluster de teste é criado com `initdb -U postgres -A trust -E UTF8` (mais `--no-sync`, porque o servidor de teste também desliga `fsync` e o diretório é apagado ao final). O servidor escuta só em socket Unix dentro do diretório de dados (`listen_addresses` vazio), com `shared_buffers=16MB`, `dynamic_shared_memory_type=mmap` e `fsync=off`.
- Os arquivos do cluster ficam em `.tmp/` no workspace, fora do tmpfs de 128 MiB. Se o caminho do socket passar de 107 bytes, o helper expõe o mesmo diretório por um symlink curto em `/tmp`; os dados continuam no workspace. O diretório e o symlink são removidos ao final.
- O processo da aplicação (`backend/cmd/chatisp`) não importa esse pacote e não leva o binário para produção.
- Se `CHATISP_TEST_DATABASE_URL` estiver definida, os testes usam essa URL e não iniciam o Postgres embutido. A URL precisa ser PostgreSQL 18.
- O runtime dos testes de domínio usa os papéis de B.4.4 (`chatisp_owner`, `chatisp_app`, `chatisp_platform`, `chatisp_ingress`), com `NOBYPASSRLS` no papel de aplicação. O superusuário do cluster de teste só cria papéis e extensões. Ele não é o papel das consultas de domínio.
- Produção e o Compose continuam com PostgreSQL 18 administrado fora do binário. O patch 18.4 é pin do teste, não um segundo servidor de produção.

## Custo

- Um devDependency npm de cerca de 56 MiB, baixado só no `npm ci`. O `go test` seguinte não baixa nada.
- Alguns segundos para `initdb` e para subir o servidor, uma vez por pacote de teste que precisa de banco. Dois pacotes em paralelo ocupam dezenas de MiB em `.tmp/` e pouca RAM com `shared_buffers` pequeno.
- Não há processo extra em produção, não há root e não há licença adicional além da licença do próprio PostgreSQL e do pacote npm.
- CI com `CHATISP_TEST_DATABASE_URL` evita subir o embutido.

## Operação

- Helper único, compilado só com os testes. Sem porta TCP. Autenticação local `trust` no socket, cujo diretório não é atravessável por outros usuários.
- `CHATISP_TEST_DATABASE_URL` é lida no início da suíte. Valor presente desliga o embutido por completo. A URL não entra em repositório, fixture versionada ou log de aplicação.
- Migrações goose rodam contra o banco escolhido, como `chatisp_owner`. O teste que exige RLS conecta em seguida como `chatisp_app`.
- Falha ao subir o embutido é falha de teste. Skip só existe se o pacote declarar, com mensagem, que aquele caso não precisa de banco.
- Backup, restore e pgBackRest continuam assuntos de produção e do M8. O embutido não substitui esse ensaio.

## Reversibilidade

A decisão está isolada no helper de teste, no pacote npm de `backend/tools/pg` e na variável `CHATISP_TEST_DATABASE_URL`. Esquema, SQL e papéis não dependem de como o servidor subiu.

Para voltar a testcontainers onde Docker existir, o helper passa a usar a URL vinda do contêiner. Para apontar outro PostgreSQL 18, basta definir a variável. Remover o devDependency não pede migração de dados reais: produção nunca executa esses binários.

Trocar o patch 18.4 por outro 18.x, quando existir um pacote npm que também caiba no verificador offline e traga `unaccent` e `pg_trgm`, é uma revisão curta deste ADR: atualizar a versão exata em `package.json`, o lockfile e o número esperado em `server_version_num`.
