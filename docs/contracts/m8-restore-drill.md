# M8 — ensaio de restore

Recorte de B8-22 e 14-backup. O procedimento restaura um dump de PostgreSQL de teste e os bytes sintéticos dos anexos num diretório novo, abre o segredo mantido com uma Ring nova lida só do artefato de chave recuperado, e reaplica `deletion_records` quando a tabela existe. Não é carga, não é alerta e não é validação de piloto com operador. Não declara RPO nem RTO do piloto.

## Banco descartável

`restoredrill.Run` só aceita o superusuário do cluster de teste no banco de manutenção `postgres`, por socket Unix, sem senha na URL e sem porta TCP. O diretório de dados precisa ser descartável. A decisão usa o destino resolvido, não o texto do caminho. Há dois casos:

- diretório sob o temporário do processo, fora do checkout canônico
- cluster efêmero do PostgreSQL embutido de teste, no formato `<checkout>/.tmp/testdb/pg-<número>`. O número é o sufixo criado por execução. Vale para um checkout chamado `chat-isp` e para uma worktree com outro nome. O servidor apaga esse diretório ao encerrar. O symlink curto no temporário, usado quando o socket não cabe no caminho do checkout, só passa quando resolve para esse cluster.

A forma `pg-<número>` não autoriza o caminho sozinha. Um symlink com esse nome é recusado quando o destino não é o cluster efêmero. Isso cobre `/var/lib/postgresql`, qualquer outro diretório fora do temporário e qualquer pasta do checkout real que não seja o próprio cluster.

A função recusa:

- `CHATISP_DATABASE_URL` e `CHATISP_TEST_DATABASE_URL`
- host remoto e porta TCP
- qualquer banco já chamado `chatisp` ou com prefixo `chatisp`
- diretório de dados do serviço, como `/var/lib/postgresql`
- qualquer outro diretório de dados dentro de `/home/enok/srv_projetos/workspace/chat-isp`, inclusive `.tmp/testdb/pg` sem o sufixo numérico
- dump fora de um diretório temporário vazio, e qualquer dump dentro de `/home/enok/srv_projetos/workspace/chat-isp`

O ensaio não abre o banco da aplicação e não lê dados reais. Os bancos `chatisp_restore_drill_` existem só durante a execução.

O ensaio cria dois bancos `chatisp_restore_drill_` seguidos de hex, aplica as migrações e apaga só esses bancos ao final. O arquivo de dump fica no diretório temporário pedido pelo chamador e não é apagado.

## Dump, objetos e chave

O pacote embutido de teste traz `postgres`, `initdb` e `pg_ctl`. Não traz `pg_dump`. O ensaio grava um dump lógico no formato COPY binário do PostgreSQL, uma tabela de `public` por arquivo, com manifesto `format postgres-binary-copy`. `goose_db_version` fica de fora porque o banco restaurado recebe as mesmas migrações antes da carga. A restauração usa `session_replication_role = replica` só nessa carga.

O ensaio gera dois anexos sintéticos com bytes aleatórios num diretório privado temporário. O backup copia esses bytes para `objects/`, com `objects.manifest` (`format restoredrill-objects-1`) limitado a oito objetos e 64 KiB por objeto. A conferência não aceita só `object_key` e `object_version`. Depois do restore, a leitura usa a referência gravada na linha restaurada, recalcula o SHA-256 dos bytes no diretório novo e compara com o hash da linha e do manifesto. A fonte é apagada antes dessa leitura. O conteúdo que ainda está na memória do processo que gerou o arquivo não serve de prova.

O segredo usa `crypto.Ring` de verdade. O material sintético fica em `recovery/material` e a versão corrente em `recovery/current`, ambos modo `0600`, no diretório `recovery` modo `0700`. `secrets.manifest` guarda só o identificador, a versão e o hash do plaintext. Não guarda a chave nem o plaintext. A Ring que decifra é carregada só da cópia restaurada, com a versão e o AAD da linha (tenant, entidade, identificador da entidade e campo, no mesmo formato canônico do produto). Hash, versão ou AAD divergente, arquivo ausente ou arquivo corrompido abortam o ensaio. Esses erros não incluem o material. Um sucesso parcial não vira relatório de sucesso.

Cada arquivo de dump, manifesto ou objeto fica abaixo do temporário do processo. O leitor recusa symlink, nome com `..` e caminho que saia desse temporário. O ensaio não altera permissão de diretório de dados, socket ou checkout para abrir outro destino. O material de chave não vai para a saída padrão, para o erro nem para os manifestos de topo.

Antes do dump existem um anexo mantido, um anexo que será excluído, um segredo mantido e um segredo que será excluído. A conversa apontada pelo anexo precisa continuar existindo. Depois do backup o ensaio apaga a fonte de objetos e de chaves, apaga a linha do segundo anexo e a linha do segredo removido, e insere `deletion_records` para os dois. O restore devolve as linhas e os bytes. A reaplicação apaga de novo as duas linhas e o objeto elegível no diretório restaurado. Conserva o anexo mantido, o segredo mantido, a Ring recuperada e a cópia do backup, inclusive o objeto que foi excluído depois do backup.

Se `deletion_records` não existir no banco restaurado, nada é reaplicado e o relatório diz isso. O código não cria a tabela. A migração exclusiva `00040_deletion_records.sql` cria a tabela mínima (`attachment` ou `credential_secret`, por tenant) com RLS forçado para `chatisp_app`. O teste com essa migração prova a reaplicação dos dois alvos.

## Comando de desenvolvimento

A partir de `backend/`:

```
go run ./cmd/restoredrill -url '<dsn do superusuário descartável>' -dump "$DUMP"
```

`-url` é o socket Unix do cluster descartável, no banco `postgres`, sem senha e sem porta. `-dump` é um diretório temporário vazio, fora de `/home/enok/srv_projetos/workspace/chat-isp`. O comando recusa destino que não seja descartável e não imprime relatório nesse caso. Não há modo que aponte para produção, para `CHATISP_DATABASE_URL` nem para um dump no checkout real.

No mesmo diretório de `-dump`, o sucesso grava `metrics.prom` com uma série e sem rótulo:

```
chatisp_restore_drill_duration_seconds <segundos com três casas>
```

O arquivo não leva nome de banco, caminho, chave nem segredo. Não é RPO nem RTO. Falha do ensaio não grava esse arquivo.

A saída padrão é um único JSON:

- `banco`: nome do banco descartável que recebeu o restore
- `anexos`: a referência do anexo conferiu
- `chaves`: a referência da chave conferiu
- `conteudo_legivel`: os bytes do anexo mantido foram lidos pela referência restaurada e o hash conferiu
- `chave_recuperada`: uma Ring nova foi carregada só do artefato recuperado
- `segredo_decifrado`: essa Ring decifrou o segredo mantido com a versão e o AAD corretos
- `exclusoes_reaplicadas`: `deletion_records` foi reaplicada
- `duracao_ms`: duração medida deste exercício, do início da execução até a conferência
- `medida`: permanece `rto` para nomear essa medição já existente do exercício
- `duracao_nota`: diz que a duração é deste exercício descartável e não é meta de piloto

`duracao_ms` e `medida` não são RPO nem RTO do piloto e não são meta de produção. O JSON não é homologação. Não lê nem grava dado real. Se o conteúdo, a chave, o hash, a versão ou o AAD falham, o comando sai com erro e não imprime esse JSON.

## O que o relatório afirma

O texto interno do ensaio diz que a referência do anexo conferiu, que o conteúdo mantido foi lido pela referência restaurada, que a chave nova veio do artefato recuperado e decifrou o segredo, que as exclusões foram reaplicadas quando a tabela existe, que o backup imutável não é prometido como apagado na hora, e que a carga não foi aceita como capacidade. Esse texto não traz k6, p95, RPO, RTO nem número de capacidade. A duração medida fica no JSON, nos campos `duracao_ms` e `duracao_nota`. Deixar os arquivos de dump e de objeto somente leitura no temporário impede o próprio ensaio de reescrevê-los. O material de chave continua `0600` num diretório privado. Isso não é política de backup imutável de produção e não apaga a cópia do backup.

## Fora deste recorte

pgBackRest, WAL de produção, apagamento de backup imutável, perfil da D2-07, k6, alertas, guia de instalação e revisão de segurança do piloto.
