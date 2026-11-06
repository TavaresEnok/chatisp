# ChatISP publicado — fechamento de 06/11/2026

O **ChatISP está publicado em https://chatisp.tokstiloesporte.com/entrar**. O proprietário confirmou que a página abre em outra rede, em resposta à solicitação de teste externo. Essa confirmação cobre a página pública. A automação de login, TLS, sessão, WebSocket e proteção de anexos foi executada com o nome do domínio conectado diretamente ao proxy interno; seus escopos são registrados separadamente. A recusa ao IP público a partir deste servidor não é evidência de indisponibilidade externa e não motivou mudanças no ingresso compartilhado.

O ChatISP é um produto independente. A publicação reutilizou a instalação existente; outros sistemas não são dependências de funcionamento do ChatISP e não foram alterados.

## Versão instalada

- Código de API, worker e painel: **`8150c9b`**, migração **57**, com evidências de implantação em `4ce0fc7`.
- Encaminhamento implantado em `026af83`; registros anteriores em `5ca8dc9` e `ac04166` foram complementados pela confirmação externa desta retomada.
- Projeto preservado: `chatisp-pronto-hoje`. Banco, anexos, chave de dados e credenciais originais reutilizados; cinco serviços saudáveis após a captura atual.
- MFA opcional por decisão do proprietário: ativação em Configurações → Segurança da conta, com QR Code gerado localmente. Fatores já ativos continuam exigidos. Nenhum fator definitivo foi cadastrado em nome do proprietário.
- Principal efetivo Sol 6.1 Médio, confirmado no registro de sessão; três subagentes Terra 6.1 Médio, sem descendentes. Paseo não foi usado nem retomado. Não houve push nem limpeza global.

## Publicação e verificações

O nome público usa o certificado já existente no Nginx central. O Apache seleciona o vhost exclusivo do ChatISP e encaminha ao Caddy em loopback. A origem pública é a primeira da lista e forma os links de convite/recuperação; a origem local de recuperação permanece permitida. Cookies `__Host-*` mantêm Secure, HttpOnly na sessão, ausência de Domain e caminho `/`; CSRF e Origin continuam exigidos nas escritas. URLs assinadas usam o próprio domínio, preservando Host, caminho e query até o Garage. **Nenhum DNS adicional de storage é necessário.** A API administrativa do Garage e as métricas permanecem internas.

[verificacao.md](verificacao.md) distingue as provas: confirmação externa manual da página; automação de TLS/HTML/health/ready, métricas 404, bucket anônimo 403, PUT bloqueado 404, login, CSRF, Origin HTTP/WS e logout pela borda interna. Depois do novo backup, o integrador repetiu com sucesso todas as verificações passivas. Atendimento, MFA optativo, WebSocket autenticado, upload/download assinado, expiração e persistência foram reaproveitados das provas válidas de [publicação](../publicacao-20261106/relatorio.md) e [MFA opcional](../mfa-opcional-20261106/relatorio.md); não se declara nova execução dessas suítes completas.

Foi entregue `scripts/check-public-domain.mjs` para repetir as verificações a partir de uma máquina externa autorizada. A ferramenta usa TLS normal, limita tempo e tamanho de resposta e não imprime credenciais/cookies. O login só ocorre após todas as verificações passivas passarem; PUT só é testado depois da identidade do painel, saúde, prontidão e recusa anônima do bucket. Uma fixture TLS isolada comprovou zero login e zero PUT após falha de saúde. Uma conta com MFA ativo tem o desafio respeitado, sem solicitação automática de códigos.

## Recuperação atual comprovada

Foi criada a cópia completa **`snapshot-20261106T163926Z-zhlJRM8r`**, em `/home/enok/.local/share/chatisp/backups-publicacao/`, com diretório 0700 e arquivos 0600. Todos os checksums passaram. O arquivo inclui banco, dados/configuração Garage, configuração privada e imagens correspondentes ao código instalado. PostgreSQL foi restaurado isoladamente sem rede: migração 57, 122 tabelas e estados de MFA conferidos com a instalação ativa. Garage restaurado iniciou e respondeu à listagem S3 autenticada; bucket vazio como no ativo. Os recursos descartáveis próprios foram removidos e os cinco serviços originais retomaram saudáveis.

A prova não inicializou a aplicação restaurada de ponta a ponta. A cópia permanece no mesmo host e não substitui destino independente ou cofre. A CA interna do Caddy é regenerável; trust-export e reexportação da raiz local fazem parte da recuperação. Procedimento, sequência e limites em [recuperacao.md](recuperacao.md).

Configuração ativa: `/home/enok/.local/share/chatisp/pronto-hoje-20261106/`, incluindo `ambiente.env`, `rede.yaml`, `estado.json` e `controle.py`. Recibo e configuração final privada: `/home/enok/.local/share/chatisp/publicacao-20261106/fechamento.json` e `config-fechamento.tar.gz`, com checksum. Senhas e chaves não foram incluídas no Git. Não executar novo bootstrap, trocar o nome do projeto ou usar `down -v`.

## Pendência técnica independente concluída

A revisão integral da tabela identificou preparação possível do item 7 sem inventar metas: foi acrescentada uma base k6 parametrizada exclusivamente para laboratório descartável, com IP loopback, contas sintéticas, nonce antes de credenciais, validação TLS, limiares de correção funcional e logout em finally. O teste de protocolo executou k6 v1.8.2 contra HTTPS falso: sucesso saiu 0; falhas de leitura/JSON saíram 99 com logout; nonce incorreto e CA não confiável saíram 107 antes do login. O parser offline também passou. Esses limiares não são metas de desempenho.

A prova **não usa o ChatISP nem mede capacidade**. Não houve carga na instalação ativa. Perfil representativo, metas, CPU/RAM/E/S, RPO/RTO e aceite continuam faltantes. Instruções e origem verificável do binário em [k6-lab.md](../../../load/k6-lab.md). A lista única de pendências permanece em [dependencias-externas.md](../../dependencias-externas.md), distinguindo implementação faltante de homologação.

## Acesso e estado operacional

Abra https://chatisp.tokstiloesporte.com/entrar. O login original é `admin@chatisp.local`; a senha preservada está no arquivo privado `acesso.txt` do diretório ativo. MFA é opcional e pode ser ativado pelo titular na conta. Convites/recuperação por e-mail dependem do SMTP, que continua desligado. Para inspeção ou retomada use `python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py ps` ou `up -d --no-build --wait`; `stop` preserva os volumes.

Meta oficial/simulação, ERP, Evolution, SMTP real e expurgo automático continuam desativados. A confirmação de publicação não é homologação dessas integrações, aceite do piloto ou declaração de produto 100% concluído. Esta rodada não alterou o código nem as imagens da aplicação já validada; acrescentou ferramentas operacionais, prova de recuperação atual e documentação.
