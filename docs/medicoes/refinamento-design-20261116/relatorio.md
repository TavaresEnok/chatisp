# Refinamento do design grafite — 16/11/2026

Melhorias autorizadas pelo proprietário após a revisão visual. Base: `5857600`. Código da atualização: `b30010f`. O tema grafite e a arquitetura React/Go foram preservados.

## Alterações

- Conversas continua como primeira opção do menu. Fila com largura preferida de 280–400 px, limitada pelo espaço disponível. O ajuste fica no ícone ao lado de Conversas e é salvo somente após interação.
- Em telas abaixo de 1440 px, os detalhes começam recolhidos e abrem sobre a conversa. Em telas maiores, podem ficar lado a lado ou ser recolhidos. Fechar/Escape e mudança de breakpoint devolvem o foco ao controle quando necessário.
- Histórico ganhou aba própria: protocolos, situação, início e última atualização, usando o histórico autorizado existente. A API não fornece assunto ou responsável nesse retorno; esses campos não foram inventados.
- Fila com identificação e metadados separados; alertas de espera e contagem de não lidas legíveis por texto, além da cor. A API da fila não fornece prévia de mensagem, portanto ela não foi adicionada.
- ERP sem seleção inicia compacto, com expansão “Vincular cliente”. Operações, permissões e erros existentes continuam disponíveis.
- Painel destaca distribuição, espera e disponibilidade. Pacotes administrativos ficam em seção recolhível; download público continua disponível.
- Texto secundário do tema escuro com maior contraste, ações com pesos visuais distintos e controles de layout acessíveis por teclado. Preferências de aparência são apagadas ao sair, como as preferências anteriores.

## Verificação

Candidato compilado verificado em Chromium com a API Go e o PostgreSQL da instalação existente. O navegador serviu os novos assets localmente durante a prévia; a API não foi simulada. Foram conferidas 28 combinações de tela/estado, com temas claro/escuro, larguras de 320 a 1440 px e altura reduzida: zero violações detectadas pelo axe nos níveis A/AA/2.2 e nenhuma rolagem horizontal.

Login por atalho (incluindo senha), painel, pacotes, conversa demonstrativa, quatro protocolos do histórico, expansão do ERP, recolhimento de detalhes, ajuste/restauração de largura, persistência após reload, foco ao mudar breakpoint, logout e bloqueio anônimo fazem parte da prova.

Validação de código: 335 testes em 65 arquivos aprovados, lint e build aprovados, além da compilação TypeScript dos roteiros E2E. O build mantém o aviso já existente de chunk JavaScript acima de 500 kB. Uma execução intermediária da suíte terminou com SIGTERM sem resultado; a execução final completa passou em 73,92 s com dois workers. As primeiras falhas de seletores e a rolagem dos detalhes por teclado foram corrigidas antes da publicação. Os roteiros antigos M3B/M4A/M4B-M6 receberam os cliques de abertura necessários à nova apresentação; a validação desta mudança prioriza o fluxo autenticado real e os testes proporcionais, sem declarar nova homologação de fornecedores.

## Reversão

Imagens anteriores preservadas como `chatisp-pronto-hoje:edge-before-design-refinement-20261116` e `chatisp-pronto-hoje:runtime-before-design-refinement-20261116`. Não há migração de banco nesta atualização.

```sh
docker tag chatisp-pronto-hoje:runtime-before-design-refinement-20261116 chatisp-pronto-hoje:runtime
docker tag chatisp-pronto-hoje:edge-before-design-refinement-20261116 chatisp-pronto-hoje:edge
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py up -d --no-deps --no-build --wait chatisp-api chatisp-worker caddy
```

Configurações e credenciais continuam em `/home/enok/.local/share/chatisp/pronto-hoje-20261106`, com acesso privado. Identidade de PostgreSQL/Garage e hashes das credenciais foram registrados antes da troca para comparação posterior. O snapshot completo anterior permanece em `/home/enok/.local/share/chatisp/backups-publicacao/snapshot-20261116T162340Z-WkdK0QNG`; esta alteração de apresentação usa reversão de imagens, sem restaurar dados sobre interações posteriores.

Provas privadas: `.tmp/design-refinement-release/`. O nome público é resolvido ao Nginx `10.10.0.10`, com TLS normal; não se trata de uma nova prova por rede externa. WhatsApp, ERP, Evolution, SMTP real e expurgo não foram ativados. As pendências de integração permanecem em [dependências externas](../../dependencias-externas.md).

## Publicação concluída

Instalada em https://chatisp.tokstiloesporte.com/conversas. A verificação foi repetida com os assets servidos pela instalação: **28 telas/estados aprovados**, sem violações detectadas pelo axe nem rolagem horizontal. [Resultados do navegador](navegador.json).

Os três downloads autenticados tiveram tamanho e SHA-256 conferidos; `/download` corresponde ao bundle atualizado. Credenciais mantidas byte a byte e contêineres PostgreSQL/Garage preservados. API, worker e Caddy reiniciados saudáveis; cinco serviços saudáveis ao final.

Recibo privado: `/home/enok/.local/share/chatisp/pronto-hoje-20261106/publicacao-refinamento-design-20261116.json`.

Runtime: `sha256:3e49fefa5205a635a4703716e61e910dd6cbb53319b952a7b9e9414d83914526`. Edge: `sha256:f18033eaa02bcd55f22c8366625f8a2f79f77cbe5c13c44dad4c576ff8329a01`.
