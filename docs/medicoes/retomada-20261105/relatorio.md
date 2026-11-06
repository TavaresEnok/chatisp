# Retomada da instalação e recuperação — 05/11/2026

Base confirmada: branch `continuation/cursor-integracao-2`, HEAD `b43edbb`. Fontes modificadas e arquivos novos preservados antes das edições em `.tmp/continuation-initial/source.tar.gz`, patch e inventário com SHA-256. Somente fontes: sem `.env`, credenciais ou dados operacionais. Inventário de 31 caminhos; SHA-256 do arquivo: `de2d7277a347384d8555c165dccc81305fa4f07951b5ad86532ac88c8ad07604`. A pasta antiga não foi usada; Paseo/Agent Control e Docker compartilhado não foram alterados/reiniciados.

## Comportamentos corrigidos

- Espelho usa publicação sem sobrescrita, recusa mudança de versão, symlinks/ancestrais, corrupção e concorrência. Manifesto com hash/contagem final; restore valida todos os arquivos antes de gravar e lê de volta os objetos criados.
- Backup/restore montam o volume do projeto pelo serviço Compose; aliases systemd full/diff/incr/ledger executáveis. Lock entre operações, nomes sem colisão no mesmo segundo, publicação completa com checksum e recusa de artefatos parciais.
- Livro exportado distingue vazio válido de ausente/truncado/corrompido. Associação tenant/anexo/objeto validada. SQL deduplica identidade completa e desconta orçamento uma vez.
- Exclusão guarda o mapa de objetos na mesma transação, inclusive criação/exclusão entre exportações. Restore reaplica privacidade de contatos, corpos/conteúdo estruturado e eventos, mantendo protocolos, ciclos e cursores. Dados de outro tenant permanecem.
- Chaves recuperadas são efetivamente aplicadas ao ambiente perdido; divergência recusa abertura. Gate AES-GCM autentica todas as sete classes cifradas com AAD e papel de recuperação, antes de abrir API/worker. Outro gate cruza referências de anexos do banco com manifesto.
- Catálogo Meta HTTP real ligado somente ao modo oficial autorizado; paginação limitada, TLS, versão explícita, credenciais por geração e snapshot completo. Nenhuma chamada real foi realizada.
- Entrada de tenant suspenso tem quantidade/bytes explícitos, recusa sem ACK ao esgotar, concorrência serializada, alerta e quarentena sem bot/replay automático após reativação. Flags desligadas por padrão.
- Interface permite plano de storage ao criar provedor. Links diretos aguardam restauração da sessão antes de redirecionar. Prova configura setor e caixa por API, sem ampliar superadmin.
- Produção deixou de depender da CA interna de laboratório: sonda interna própria e trust store pública. Sintaxe Compose e adaptação Caddy conferidas com domínios `.invalid`, sem ativação nem ACME.
- Alertas de banco/anexos/chaves/ledger preservam identidade da instalação; idade não é apresentada como RPO. Perfil de monitoramento opt-in, destinos SMTP TLS/webhook e regras operacionais parametrizados e desligados por padrão.

## Verificações

- `go test -p 2 -count=1 -timeout 240s ./...`: 65 pacotes passaram; `cmd/chatisp` encontrou dois defeitos no fixture novo de SQL (version obrigatória e múltiplos comandos preparados). Corrigidos somente no teste; `go test -count=1 -timeout 240s ./cmd/chatisp` repetido integralmente: passou, 29,929 s. Não foi descrito como nova execução completa sem falhas.
- `go vet ./...`: passou. `scripts/lint.sh`: golangci-lint 0, govulncheck 0 vulnerabilidades alcançáveis, ESLint, TypeScript e gerados conferidos. O achado de módulo não importado já documentado continua sendo distinção de alcance, sem alegação de ausência no módulo.
- Vitest completo: 61 arquivos, 290 testes passaram. Inclui restauração de sessão antes do redirect e limite de storage no formulário.
- `python3 -m unittest discover -s deploy/backup/tests -p 'test_*.py'`: 16 testes passaram; valida os programas reais de associação de objetos e os comandos de agenda, além de corrupção/artefatos parciais/concorrência.
- `test-alert-rules.py`: oito cenários promtool de ausência/atraso de cada componente, outra instalação saudável não mascara falha. Saída em `alert-rules.txt`.
- `test-ops-rules.py`: sete cenários promtool de métricas operacionais, flags off, parâmetros obrigatórios e isolamento de instalação. Saída em `ops-rules.txt`.
- `test-alert-delivery.py`: amtool validou configurações off, SMTP TLS e webhook; Alertmanager entregou alerta sintético a receptor HTTP loopback descartável com identidade da instalação. Saída em `alert-delivery.txt`. Não é entrega ao destino operacional real.

## Prova Compose e correções do roteiro

Comando: `CHATISP_VALIDATE_ALPINE=1 CHATISP_VALIDATE_BACKUP=1 ./scripts/compose-validate.sh`. Docker 20.10.9, Compose 2.24.7, bases Alpine de validação, PostgreSQL 18.6 com pgBackRest, Garage 2.5.0, Caddy e Chromium real com limites de threads. As imagens Debian originais foram provadas em rodada anterior; não se atribui essa prova antiga ao código novo.

As tentativas são registradas em `.tmp/compose-attempt-*.txt`. Correções do roteiro foram motivadas por falhas concretas: setor padrão ausente; seleção da caixa/conversa; corrida real de restauração da sessão; evento WebSocket contém ID externo, não o corpo; consumo integral de métricas para evitar SIGPIPE no `grep -q`. A tentativa 10 completou atendimento, suspensão, métricas, queda/retorno do storage, reinício, pgBackRest/WAL e backup dos quatro componentes. Detectou que o roteiro esperava 404/409 para anexo excluído, mas a autorização retorna legitimamente 403. O laboratório foi preservado por `CHATISP_VALIDATE_KEEP_ON_FAILURE=1` só para diagnóstico. A continuação, no mesmo destino e backup, confirmou exclusão, perda de banco/storage/chaves, recusa de anel divergente, autenticação AES-GCM (5 valores cifrados reais do fixture), reaplicação de privacidade, restauração de um objeto mantido e não retorno dos excluídos, login/MFA de dois provedores e segredo do app legível. Os recursos desse laboratório foram encerrados por projeto e as quatro etiquetas próprias removidas.

O pgBackRest real também revelou e corrigiu `pg1-user`: o superusuário deste Compose é `chatisp`, não `postgres`. Uma sonda `stanza-create` idempotente conferiu a correção antes do ensaio. Após as correções, o roteiro final contínuo passou, saída 0, no HEAD `bdf7f5e` (`7827a90` contém o pacote principal). Resumo sem credenciais em `compose-final.txt`. Timestamps brutos das ferramentas são UTC; a data deste relatório usa America/Fortaleza.

## Estados e pendências

Configuração de produção está preparada, sem instalação pública. Homologação externa, observação por operador, capacidade, PITR, RPO/RTO e produção autorizada não foram declarados. ERP real continua stub; Evolution continua contrato; aprovação sensível sem consumidor e executor de expurgo continuam parciais. Preview de retenção não apaga nada. A [tabela única de dependências](../../dependencias-externas.md) distingue implementação de homologação e enumera ERP, autorização, Meta, Evolution, retenção, carga, limites, SMTP, alertas, backups e domínio.

Matriz reconciliada: 257 requisitos — 206 implementados e verificados, 32 implementados aguardando homologação externa, 19 parciais, zero inteiramente não implementados. Um item parcial pode conter código ainda ausente; não é homologação. Sem alegação de produto 100% ou de ausência de trabalho local por mera existência de scripts.

## Resultado final da prova e recursos

`CHATISP_VALIDATE_ALPINE=1 CHATISP_VALIDATE_BACKUP=1 ./scripts/compose-validate.sh` terminou com **saída 0**, projeto `chatisp-validate-998996`, em 05/11/2026 America/Fortaleza (logs UTC encerram em 06/11 às 02:53). Todas as fases passaram numa execução contínua: before, suspension, storage-down, storage-up, after, exclude e restored.

Foram comprovados bootstrap/MFA, SMTP de convite e recuperação/falha, atendimento autenticado, frame WebSocket identificado, recusas CSRF/Origin, anexos HTTPS/URL assinada/isolamento, métricas privadas, suspensão limitada, indisponibilidade e recuperação do storage, reinício e persistência. Depois: pgBackRest cifrado e arquivamento de WAL, backup dos quatro componentes, exclusão após backup, perda dos volumes de banco/Garage e remoção das chaves locais, recusa de anel divergente sem abrir atendimento, recuperação/aplicação do anel, reaplicação de exclusões e anonimização, autenticação dos cinco valores cifrados existentes no laboratório, restore de objeto íntegro e ausência dos objetos excluídos. Banco: 37,7 MB; espelho: três objetos, 209 bytes — escala sintética, não capacidade.

Encerramento conferido por inventário: **zero contêineres, volumes, redes ou etiquetas `chatisp-validate-*`**. As etiquetas auxiliares Prometheus, Alertmanager e Caddy baixadas para as sondas também foram removidas individualmente, após conferir ausência de contêiner dependente. Cache de build recriável preservado. As sondas antigas citadas no pedido já estavam ausentes na inspeção inicial. Nada global foi podado ou reiniciado. Detalhes em `recursos.txt`.

Commits de código desta retomada: `7827a90` e `bdf7f5e`. O commit que grava este relatório, continuidade, guias e evidências vem depois desses dois. Nenhum arquivo necessário à entrega permanece fora do Git; o snapshot inicial e os logs brutos de diagnóstico ficam em `.tmp`, fora do pacote, como artefatos locais recuperáveis.

A próxima ação depende das decisões descritas na tabela única: obter contrato/ambiente ERP e verificação do provedor para implementar adaptador/aprovação; release/licença Evolution para seu transporte; elegibilidade/preservação de cada classe para o executor de retenção; perfil/metas para carga; e credenciais/destinos/domínio/operador autorizados para homologação. Não existe autorização de implantação pública implícita nesta prova.
