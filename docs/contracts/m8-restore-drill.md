# M8 — ensaio de restore

Recorte de B8-22 e 14-backup. O procedimento restaura um dump de PostgreSQL de teste, confere a referência do anexo e a referência da chave, e reaplica `deletion_records` quando a tabela existe. Não é carga, não é alerta e não é validação de piloto com operador.

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

## Dump e conferência

O pacote embutido de teste traz `postgres`, `initdb` e `pg_ctl`. Não traz `pg_dump`. O ensaio grava um dump lógico no formato COPY binário do PostgreSQL, uma tabela de `public` por arquivo, com manifesto `format postgres-binary-copy`. `goose_db_version` fica de fora porque o banco restaurado recebe as mesmas migrações antes da carga. A restauração usa `session_replication_role = replica` só nessa carga.

Antes do dump existem um anexo mantido (`object_key` e `object_version`), um anexo que será excluído depois e um `credential_secrets.key_version` com o ciphertext de ensaio. A conversa apontada pelo anexo precisa continuar existindo. Depois do dump o ensaio apaga o segundo anexo e insere `deletion_records`. O restore devolve esse anexo. A reaplicação apaga de novo a linha e conserva o anexo mantido e a chave.

Se `deletion_records` não existir no banco restaurado, nada é reaplicado e o relatório diz isso. O código não cria a tabela. A migração exclusiva `00040_deletion_records.sql` cria a tabela mínima (`attachment` ou `credential_secret`, por tenant) com RLS forçado para `chatisp_app`. O teste com essa migração prova a reaplicação.

## Comando de desenvolvimento

A partir de `backend/`:

```
go run ./cmd/restoredrill -url '<dsn do superusuário descartável>' -dump "$DUMP"
```

`-url` é o socket Unix do cluster descartável, no banco `postgres`, sem senha e sem porta. `-dump` é um diretório temporário vazio, fora de `/home/enok/srv_projetos/workspace/chat-isp`. O comando recusa destino que não seja descartável e não imprime relatório nesse caso. Não há modo que aponte para produção, para `CHATISP_DATABASE_URL` nem para um dump no checkout real.

A saída padrão é um único JSON:

- `banco`: nome do banco descartável que recebeu o restore
- `anexos`: a referência do anexo conferiu
- `chaves`: a referência da chave conferiu
- `exclusoes_reaplicadas`: `deletion_records` foi reaplicada
- `duracao_ms`: duração medida do ensaio, do início da execução até a conferência. Esse valor é o RTO medido deste exercício descartável
- `medida`: `rto`, para deixar explícito que `duracao_ms` é esse RTO medido

O JSON não é meta de produção. Não lê nem grava dado real.

## O que o relatório afirma

O texto interno do ensaio diz que a referência do anexo e a referência da chave conferem, que as exclusões foram reaplicadas quando a tabela existe, que o backup imutável não é prometido como apagado na hora, e que a carga não foi aceita como capacidade. Esse texto não traz k6, p95, RPO, RTO nem número de capacidade. O RTO medido fica só no JSON do comando, no campo `duracao_ms`. Deixar o dump somente leitura no temporário impede o próprio ensaio de reescrevê-lo. Isso não é política de backup imutável de produção e não apaga cópia nenhuma.

## Fora deste recorte

pgBackRest, WAL de produção, apagamento de backup imutável, perfil da D2-07, k6, alertas, guia de instalação e revisão de segurança do piloto.
