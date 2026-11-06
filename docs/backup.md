# Backup e recuperação da instalação Compose

Procedimento em revisão e prova nesta retomada de 05/11/2026. O relatório da execução fica em [retomada](medicoes/retomada-20261105/relatorio.md). Scripts existentes não constituem, por si, prova de execução. Não foi autorizado restore operacional, PITR, RPO ou RTO de produção.

O pacote usa pgBackRest com repositório cifrado e WAL, espelho de objetos Garage sem versionamento, livros de exclusões/mapas de anexos e cópia cifrada do anel de chaves. `deploy/.env`, a senha do repositório e a frase de recuperação devem estar em cofre independente; os scripts não fazem backup da configuração inteira.

Os volumes `backup-repo` e `backup-mirror` pertencem ao projeto Compose. Os comandos efêmeros montam o espelho pelo serviço declarado; nunca usam o nome literal global `backup-mirror`. No laboratório são volumes no mesmo host: isso não protege da perda do servidor. Destinos independentes e ensaio operacional estão na [tabela única de dependências](dependencias-externas.md).

## Operação

Requer Docker/Compose compatíveis com o pacote, Bash, Python 3, OpenSSL, `flock`, `sha256sum`, `tar` e ferramentas básicas do sistema. Caminhos em `COMPOSE_CMD` devem ser sem espaços; a interface separa argumentos por espaços, sem avaliação de shell.

```sh
export COMPOSE_CMD='docker compose -p minha-instalacao -f deploy/compose.yaml -f deploy/backup/compose.backup.yaml --env-file deploy/.env'
export BACKUP_INSTALLATION=minha-instalacao
export BACKUP_KEYS_PASSPHRASE_FILE=/cofre-montado/frase-chaves
export BACKUP_STATUS_FILE=/diretorio-privado/backup.prom

deploy/backup/chatisp-backup.sh init
deploy/backup/chatisp-backup.sh backup full
deploy/backup/chatisp-backup.sh ledger
BACKUP_MAX_AGE_SECONDS=<limite-definido-pelo-operador> deploy/backup/chatisp-backup.sh check
```

`BACKUP_REPO_CIPHER_PASS` e `BACKUP_RETENTION_FULL` são obrigatórios no ambiente Compose. Não há padrão comercial. `full`, `diff` e `incr` também são aliases aceitos pelo script, correspondendo às instâncias systemd. As unidades/timers são exemplos não instalados; ajuste a cadência antes de habilitá-los.

O lock compartilhado em `/mirror/.operation-lock` impede backup/restore concorrentes. Uma interrupção não tratável pode deixar o lock: o operador precisa comprovar que a execução anterior terminou antes de removê-lo. Nomes incluem fração de segundo e aleatoriedade. Livros e chaves são publicados com checksum, sem sobrescrita; artefatos parciais, órfãos ou corrompidos impedem a recuperação.

A exportação marca sua geração como em curso antes da primeira consulta. A marca só sai após os três pares de artefatos completos; interrupção mantém a evidência e impede restore silencioso de livros anteriores. O operador deve conferir a execução interrompida, preservar os artefatos, reconciliar ou renovar a exportação e só então remover a marca daquela geração. Não apagar marcas ou livros de outras execuções para forçar uma recuperação.

O espelho verifica SHA-256, tamanho e identidade antes de reutilizar um objeto. Mudança inesperada de versão é recusada explicitamente, preservando a cópia anterior. Manifesto tem contagem e hash de fechamento. Symlinks, operações concorrentes e objetos órfãos são recusados. Este formato ainda não lançado recusa os manifestos incompletos da implementação interrompida. Bucket versionado exige procedimento próprio de replicação/remapeamento; este comando o recusa.

Novas mídias recebidas usam `<namespace UUID>/<mensagem UUID>`, como exige o espelho. Uma intenção `attachments.state=upload`, com tamanho e orçamento reais, confirma antes de `Create`; a gravação revalida propriedade e contato sob locks limitados pelo timeout. Interrupções deixam a intenção para retry ou exclusão. A exclusão também enfileira chaves determinísticas de reservas antigas `in/<tenant>/<mensagem>` antes de redigir o contexto; tamanho desconhecido não é inventado no livro. Mídias antigas já publicadas nesse prefixo e órfãos sem vínculo recuperável não são migrados automaticamente: o espelho os recusa, e o operador precisa inventariar e definir tratamento antes do aceite de backup. Não houve reparação de armazenamento real nesta missão.

## Restore fechado até consistência

Somente numa instalação parada, com volumes de dados perdidos/vazios, repositório e espelho preservados e configuração recuperada do cofre:

```sh
RESTORE_KEYS_OUT=/diretorio-privado/chaves-recuperadas.env deploy/backup/chatisp-restore.sh
```

O script adquire exclusividade, fecha API/worker, verifica e decifra a cópia das chaves, valida o anel e o aplica atomicamente ao arquivo `--env-file` (ou `RESTORE_ENV_FILE`). Chaves existentes divergentes recusam recuperação. O arquivo recuperado é 0600; os valores nunca são avaliados como shell. O anel efetivamente recebido pelo contêiner é conferido.

O Compose deve indicar um único `--env-file` explícito. Se `RESTORE_ENV_FILE` também for informado, precisa designar o mesmo arquivo. `RESTORE_KEYS_OUT` deve ser distinto, sem aliases por symlink ou hardlink ao ambiente; a validação ocorre antes de parar os serviços.

Depois restaura o PostgreSQL no ponto consistente do backup (`--type=immediate --target-action=promote`), valida os livros completos e a associação provedor/anexo/chave, reaplica exclusões com deduplicação e orçamento idempotente. A exclusão do titular também reaplica anonimização e redação do conteúdo acessível pelo painel, preservando protocolos/ciclos e trilha mínima. O mapa de objetos excluídos é guardado no momento da exclusão.

Antes da abertura, o gate autentica os campos cifrados com AES-GCM/AAD e o anel recuperado; IDs de versão sozinhos não bastam. Também cruza os anexos sobreviventes do banco com o manifesto. Só então sobe Garage/Caddy, exporta a CA local quando aplicável, provisiona o bucket e restaura os objetos com leitura de conferência. API e worker abrem após esses passos; falha mantém o atendimento fechado.

A cópia imutável de backup continua contendo dados anteriores. O livro externo reaplica decisões na recuperação; ele precisa sobreviver ao desastre e ser atualizado após exclusões. Conteúdo bruto restrito de ingresso/backups segue a política por classe, sem promessa de apagamento de toda cópia.

Desde as migrações `00053`–`00055`, a decisão de exclusão e a fila de limpeza física confirmam juntas. A API pode responder 503 depois da anonimização: repetir a solicitação retoma a fila, sem reabrir referências. Exporte o livro mesmo quando houver limpeza pendente. Marcadores HMAC de ingresso e seus cutoffs integram uma terceira cópia pareada com o livro novo; preservar as versões correspondentes do anel de chaves é obrigatório. A recuperação aplica as migrações com API/ingresso/worker fechados antes de reaplicar o livro.

O formato novo distingue `contact_privacy_guarded`, cuja proteção foi confirmada na mesma transação, de `contact_privacy` histórico sem prova. Um livro antigo vazio ou somente com anexos continua válido; qualquer exclusão de contato sem cobertura comprovada impede a abertura do restore, inclusive em snapshot local e em registros contraditórios. A simples presença de um arquivo de marcadores não comprova uma exclusão histórica. Não existe autorização para editar flags e presumir identidades/cutoffs perdidos. A conciliação do legado precisa de procedimento do operador antes do aceite; a instalação ainda não foi homologada. O requisito de atualização sem binários antigos simultâneos está em [implantação](implantacao.md#atualização-com-migrações-de-privacidade).

## Sinais, alertas e limites

`check` exige sucesso recente de **postgres, attachments, keys e ledger**. `BACKUP_INSTALLATION` é obrigatório; identidade e componente são preservados nas métricas e regras centrais. `BACKUP_MAX_LEDGER_AGE_SECONDS` permite limite próprio para o livro. `render-alerts.sh` produz as regras; `compose.alerting.yaml` e `render-alertmanager.py` preparam coleta/entrega opt-in. Nenhuma porta de métricas é publicada.

Idade do backup não equivale ao RPO: o banco depende do backup base e do WAL arquivado; anexos dependem da última cópia efetiva; rotação exige copiar imediatamente o novo anel; exclusões dependem da atualidade do livro e do WAL. O RTO depende de volume, infraestrutura, verificações e operador. Nenhuma meta foi inventada ou medida como compromisso de produção.

Para alertas operacionais, `render-ops-alerts.py` produz regras vazias por padrão. Habilitar exige `CHATISP_OPS_ALERTS_ENABLED=true`, `BACKUP_INSTALLATION`, `OPS_MAX_JOB_AGE_SECONDS`, `OPS_MAX_UNCERTAIN_AGE_SECONDS` e `OPS_MAX_WEBHOOK_AGE_SECONDS`, sem valores comerciais implícitos. Grave em `OPS_RULES_FILE`; o exemplo Prometheus inclui esse arquivo e rotula cada alvo com a mesma identidade da instalação. As regras cobrem disponibilidade, falha de coleta, canal degradado, fila antiga, envio incerto e webhook antigo. O teste com promtool avalia cada sinal e o isolamento entre instalações. A entrega ao destino real exige a configuração e homologação do operador.
