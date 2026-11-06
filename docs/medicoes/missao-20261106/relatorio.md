# Missão de pendências — 06/11/2026

Data local: America/Fortaleza. Base `e58a8a3`, branch `continuation/cursor-integracao-2`, worktree `chat-isp-cursor-integracao-2`. O Git estava limpo na inspeção inicial. Código integrado no commit local `6647f7e`, criado após as provas com fontes idênticas às testadas; os commits anteriores foram preservados. Não houve push, implantação pública, alteração de produção, Paseo/Agent Control nem chamada a fornecedor real Meta, ERP, Evolution ou SMTP.

## Coordenação e decisões

O ambiente disponibilizou onze vagas, incluindo o principal. As dez frentes foram abertas com `gpt-6.1-terra`, raciocínio `medium` e `fork_turns=none`, com propriedade de arquivos definida e reutilização das frentes para os achados relacionados. A revisão independente foi feita por agente separado da implementação. Nenhum subagente foi criado pelos trabalhadores.

O usuário confirmou nesta missão que ainda não dispõe dos contratos de ERP/verificação, release/licença Evolution, regras de retenção por classe nem perfil/metas do piloto/RPO/RTO. A [tabela única de dependências](../../dependencias-externas.md) continua sendo o registro oficial dos dados necessários e da próxima ação. Ausência de dados não virou autorização para inventar contrato ou acionar fornecedor.

## Correções locais

| Problema | Solução e alcance |
| --- | --- |
| Resultado tardio de chamada ERP alterava uma sondagem posterior do disjuntor | Gerações impedem que chamadas anteriores fechem o circuito ou liberem a sondagem atual. Não implementa o ERP do fornecedor. |
| Fake Evolution entrava em pânico quando usado com valor zero | Inicialização segura do mapa; teste de isolamento entre caixas. Continua sendo fake, com transporte real ausente e flag desligada. |
| SMTP sem STARTTLS podia transmitir convite/recuperação em texto claro | STARTTLS obrigatório, TLS ≥ 1.2 e certificado confiável antes de autenticação ou envio. Sink descartável recebeu STARTTLS e CA temporária; nenhuma opção de desativar TLS foi criada. SMTP implícito em 465 não foi implementado; usar serviço STARTTLS compatível. |
| Backup anunciava sucesso local antes de concluir o registro no destino | Publicação da métrica ocorre depois do registro. Arquivo de frase com primeira linha vazia é recusado antes de backup/restore. |
| Exclusão física dentro da transação SQL podia apagar objetos e depois reverter somente o banco | Migração `00053`: fila com RLS, tombstones e anonimização confirmados antes da limpeza. Falha física mantém fila durável e responde 503 com instrução de repetição. Retry confirma cada objeto separadamente, sem ressuscitar referências. |
| Entrada antiga pendente ou expandida depois podia recriar contato apagado | Migração `00054`: HMAC por tenant/conector/versão, corte pelo recebimento original e checagem sob lock compartilhado compatível com a exclusão. Duplicatas são detectadas antes da criação de identidade; triagem recusa contato anonimizado. Mensagem nova após o corte continua permitida. |
| Mídia recebida podia ficar órfã durante exclusão ou crash e usava chave fora do formato do espelho | Intenção de upload durável antes de `Create`, com quota/tamanho reais; lock de mensagem e anexo, revalidação e timeout de storage. Novas chaves usam namespace UUID/mensagem UUID. Retry, falha de limpeza e mudança de tamanho mantêm estado recuperável; reservas antigas identificáveis também entram na fila antes da redação. |
| Livro anterior à proteção de ingresso não comprova ausência de replay após restore | Migração `00055` distingue a cobertura confirmada no mesmo commit da exclusão dos registros históricos. Backlog sem prova fica em quarentena preservada; restore recusa proveniência histórica não comprovada em vez de inventar identidade/corte. |
| Política vazia podia ser gravada antes de terminar a leitura; confirmação e exportações pertenciam ao contato anterior | Formulário aguarda a política; troca de contato e início de exclusão invalidam respostas tardias, limpam confirmação/resultados e orientam retry em falha parcial. IDs são codificados na URL. |
| Script de ensaio escondia o relatório e instalava dependências implicitamente | Saída detalhada do teste e pré-requisito explícito; ensaio continua sintético e recusa URLs/DSNs externos. Não comprova capacidade. |

## Limites de privacidade e recuperação

A fila de limpeza física é retomada pelo administrador ao repetir a solicitação; não foi criado executor automático de expurgo. O livro externo deve ser exportado após a decisão confirmada, inclusive quando a limpeza física ainda está pendente. Perder esse livro depois do último backup continua sendo um risco operacional documentado; os volumes locais não equivalem a destino independente.

Os marcadores de ingresso, suas versões de chave e seus cutoffs fazem parte do livro novo. A exportação lê exclusões antes dos mapas e marcadores, para que uma exclusão observada tenha suas associações nas consultas seguintes. Isso não é apresentado como snapshot atômico. Restore aplica migrações com API/worker fechados e preserva o corte original dos marcadores. Um conector ausente ou de outro tenant faz a recuperação falhar fechada.

Exclusões históricas sem prova não recebem automaticamente `ingress_guarded=true`. A quarentena conservadora preserva os payloads para conciliação autorizada, pois não é possível descobrir a identidade antiga com segurança. Seu limite de ativação é separado do horário de exclusão; não é prova do instante em que uma transação antiga confirmou. O procedimento exige parar trabalhadores durante a atualização e avaliar o backlog antes de liberar eventual conciliação. Não há replay automático dessa quarentena.

Envelope bruto multi-tenant, recibos de status segregados tardiamente e backups imutáveis continuam sujeitos a acesso restrito e política por classe. A exclusão não promete eliminar toda cópia desses conteúdos. Status tardio pode conservar `recipient_id`, mas não recria contato. Essa limitação não habilita expurgo genérico sem D2-06.

Mídias antigas já publicadas no prefixo `in/<tenant>/<mensagem>` e órfãos históricos sem vínculo não recebem remapeamento presumido. O espelho continua recusando esse formato; o inventário e o tratamento do storage real precisam de procedimento do operador. A correção cobre novas gravações e as reservas antigas ainda identificáveis, sem alegação de saneamento retroativo de todo o bucket.

## Evidência e conclusão

Ambiente conferido: Go 1.27.3, Node 24.11.0, npm 11.6.1, Docker 20.10.9 e Compose 2.24.7. As provas anteriores em Debian continuam históricas; o novo laboratório usa Alpine. Nenhuma prova sintética constitui homologação, capacidade, RPO/RTO ou aceite humano.

Verificações executadas nesta missão, com saída zero:

- `go -C backend test -p 2 -count=1 -timeout 240s ./...`: 66 pacotes com testes aprovados no código integrado, inclusive intent/limpeza de mídia, upgrade, proveniência e restauração de privacidade.
- `go -C backend vet ./...` e `PATH=/tmp/chatisp-node24/bin:$PATH ./scripts/lint.sh`: vet, golangci-lint, govulncheck, ESLint, TypeScript, OpenAPI e sqlc em dia. Govulncheck reportou zero vulnerabilidades alcançáveis, zero em pacotes importados e uma em módulo requerido não alcançado; não foi alegada ausência de achados no módulo inteiro.
- `go test -race`: ERP, Evolution, crypto, mail e sink passaram; pipeline, retenção e HTTP de retenção passaram novamente depois das correções de mídia e reservas legadas.
- Vitest completo: 61 arquivos, 295 testes passaram. A primeira execução revelou teste de identidade que editava campo antes do carregamento; a fixture foi corrigida, o pacote repetido e a suíte inteira executada novamente. Código do produto web não mudou depois dessa suíte; a alteração posterior do runner UX foi validada pela própria jornada.
- `python3 -m unittest discover -s deploy/backup/tests -p 'test_*.py'`: 21 testes passaram. Cobrem artefatos pareados, legado sem prova, integridade, frase vazia, status no destino e ordem de exportação.
- `test:e2e:m7-ops` e `test:e2e:m4b-m6`: passaram, sem skip, com dados sintéticos e serviços locais. A jornada de mídia foi executada depois das correções de intent e limpeza.
- `test:e2e:ux`: 52 visões, zero falhas. A primeira tentativa falhou na importação dinâmica por confiança TLS da fixture; o runner passou a fixar somente o SPKI do certificado efêmero no Chromium da jornada e escolher API loopback livre. A repetição completa passou; nenhum controle do runtime ou do host foi desativado.
- `bash scripts/dev.test.sh` e `scripts/ops-security-check.sh`: passaram. O script de carga local exibiu 200 chamadas e zero falhas, explicitamente como ensaio sintético. As fontes dessas verificações permaneceram inalteradas depois delas; não se apresenta o ensaio como capacidade do piloto.

Regras Prometheus/Alertmanager não foram alteradas e suas provas históricas não foram repetidas nem atribuídas ao novo código. A validação de destinos reais continua pendente.

A tentativa Compose `chatisp-validate-1827821` foi interrompida de forma controlada para incorporar o tratamento de proveniência histórica. A tentativa `chatisp-validate-2014525` passou com as três migrações, falha de limpeza, retry e restore, antes da correção adicional de mídia. A última prova do conjunto, projeto **`chatisp-validate-2282618`**, passou integralmente com **saída 0** nas fontes do commit `6647f7e`. Resumo filtrado e hash do log bruto privado em [compose-final.txt](compose-final.txt).

O laboratório confirmou bootstrap/MFA, SMTP sobre TLS, convite/recuperação/falha, CSRF/Origin, worker/WebSocket, anexo privado com bytes idênticos, suspensão limitada, queda/retorno do storage e reinício. O backup cifrado de banco/WAL, espelho, chaves e livro foi seguido de exclusão com Garage parado: 503, anonimização e fila/livro persistentes, exportação do livro enquanto a limpeza estava pendente e retry após retorno do storage. A perda real de volumes e chaves foi recuperada; cinco valores cifrados foram autenticados, o anexo mantido retornou íntegro, os excluídos não voltaram e os fingerprints/cutoffs foram comparados exatamente antes/depois. Banco de 37,8 MB e três objetos/209 bytes: escala sintética. A recuperação do estado de intent e reservas de mídia foi exercitada nos testes PostgreSQL/race específicos, não apresentada como download Meta real no laboratório.

O inventário final em [recursos.txt](recursos.txt) encontrou **zero contêineres, volumes, redes e etiquetas de imagens dos três projetos**. Dois volumes, a rede e a etiqueta runtime remanescentes da tentativa interrompida foram removidos individualmente, após conferir propriedade e ausência de usuários. Os três diretórios temporários de Compose foram removidos. Caches de build e artefatos privados de diagnóstico permaneceram; nada global foi podado ou reiniciado.

A matriz foi recontada: **206 verificados, 32 aguardando homologação externa, 19 parciais, total 257**. Não foi promovido stub, fake, preview ou teste local a integração homologada. Permanecem as decisões da tabela única, o tratamento de eventual legado operacional e o aceite humano. Envio oficial e expurgo automático seguem desligados. O próximo commit registra somente documentação e evidências; ele não muda o código testado.
