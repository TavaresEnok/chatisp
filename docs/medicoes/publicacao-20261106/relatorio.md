# Domínio ChatISP — 06/11/2026

Data: America/Fortaleza. A instalação `chatisp-pronto-hoje` foi preservada e configurada para **https://chatisp.tokstiloesporte.com/entrar**, com origem, cookies, CSRF, WebSocket e anexos coerentes. **O acesso pela Internet ainda não foi comprovado:** o DNS normal resolve para `177.104.156.25`, mas 80/443 recusaram conexão. A prova funcional usou o nome público conectado diretamente ao proxy interno `10.10.0.10`; isso não equivale a uma conexão pelo IP público.

## Versão e equipe

- Base Git `9cbc484`; runtime e painel existentes construídos do código `0002570`, sem atualização de esquema nesta execução.
- Configuração de encaminhamento integrada em `026af83`, montada nos serviços existentes; as imagens originais foram reutilizadas. Os commits seguintes desta rodada registram revisão do backup e evidências.
- Modelo efetivo do principal: `gpt-6.1-sol`, `medium`; três subagentes nativos `gpt-6.1-terra`, `medium`, sem descendentes. Confirmado pelos registros `turn_context` das quatro sessões e pela configuração local. A equipe histórica do Paseo foi substituída conforme instrução do usuário; nenhum serviço, agente ou fila do Paseo foi usado ou retomado.
- Não houve push nem conexão com fornecedores reais. Meta oficial, ERP, Evolution, SMTP e expurgo automático continuam desligados.

## Encaminhamento corrigido

O Caddy do ChatISP atendia somente em `127.0.0.1:8443`. As portas 80/443 do servidor `10.10.0.26` pertencem ao Apache do PNETLab. O proxy TLS localizado em `10.10.0.10` é Nginx, com certificado Let's Encrypt para o nome do ChatISP, válido de 06/11/2026 a 04/02/2027. Ele encaminhava o nome por HTTPS ao vhost padrão do PNETLab.

Foi adicionado um vhost Apache **exclusivo para o nome ChatISP**, sem substituir os vhosts padrão. A cadeia agora é:

`nome ChatISP → Nginx 10.10.0.10:443 → Apache 10.10.0.26:443 → Caddy 127.0.0.1:8480 → aplicação/Garage`.

O Apache preserva Host, encaminha WebSocket e aceita somente o IP do proxy e loopback. Foi aplicado por reload após `apache2ctl configtest`; não houve reinício global. O certificado de transporte Apache preexistente foi reutilizado; a validação desse certificado pelo Nginx não pôde ser inspecionada. O certificado visto pelo navegador é público e foi validado sem ignorar erros TLS.

O PNETLab manteve o comportamento observado em 80/443/18080 (`301` para seu próprio fluxo). A rota Nginx de `nexoisp.tokstiloesporte.com` não foi alterada: antes e depois, ela retornou `301` para `/store/public/admin/main/view`, do PNETLab. Isso preserva o estado anterior, mas **não comprova que o destino próprio do NexoISP esteja correto ou operacional**. Não foi iniciado nem modificado serviço do NexoISP.

## Segurança e anexos

A primeira origem permitida é o domínio público, também usada na formação de URLs de convite/recuperação. A origem local `https://localhost:8443` foi mantida em segundo lugar para recuperação. Os cookies continuam `__Host-*`, Secure e sem Domain. SMTP continua indisponível; não foi enviado convite ou recuperação real.

URLs assinadas usam o próprio domínio, no caminho do bucket `/chatisp-anexos/*`, com Host, caminho e query preservados até o Garage S3. Não é necessário registro DNS adicional. A rota pública permite somente GET/HEAD; PUT respondeu 404, e leitura sem assinatura foi recusada. O storage interno continua HTTPS com a CA original. A API administrativa do Garage, PostgreSQL e métricas continuam sem portas publicadas; `/metrics` respondeu 404 pela borda.

O backend recebe o IP compartilhado do proxy em seus controles por IP. Não foi habilitada confiança indiscriminada em cabeçalhos de cliente. O log de acesso do novo vhost Apache omite queries; a configuração de logs do Nginx ainda precisa ser inspecionada pelo seu administrador, porque URLs assinadas são credenciais temporárias.

## Verificações efetivamente executadas

[verificacao.txt](verificacao.txt) registra as duas jornadas sintéticas: login e MFA próprios, operação autenticada, snapshot WebSocket, upload e download com bytes conferidos, recusa sem assinatura e após a expiração real da URL. A identidade do proprietário não foi usada para cadastrar MFA.

As duas fixtures ficaram identificadas e desativadas: tenants suspensos, usuários/vínculos desativados e sessões revogadas. Os contatos foram apagados pelo fluxo suportado de privacidade; consulta posterior encontrou zero anexos e zero objetos pendentes de limpeza, com dois registros de exclusão preservados. Não houve remoção manual de trilhas. O administrador original manteve senha e MFA sem cadastro.

PostgreSQL, Garage, Caddy, API e worker foram reiniciados. Depois disso passaram novamente página, health/readiness, login original, cookies, recusa CSRF/Origin, recusa de WebSocket com origem estranha e logout, tanto pelo nome conectado ao proxy interno quanto pela URL local com a CA própria. As credenciais originais e as configurações de acesso persistiram. Saídas sanitizadas em [reinicio.txt](reinicio.txt).

As configurações passaram por `docker compose config -q`, `caddy validate` e `apache2ctl configtest`; `git diff --check` passou. O script de backup passou em `bash -n` e no seu preflight. Os testes locais de ERP/Evolution passaram sem rede de fornecedor. As suítes amplas de produto registradas em [pronto-hoje](../pronto-hoje-20261106/relatorio.md) foram reutilizadas, sem alegar nova execução: esta rodada alterou encaminhamento e operação, sem alterar o código do produto.

## Primeiro acesso e recuperação

O arquivo privado `/home/enok/.local/share/chatisp/pronto-hoje-20261106/acesso.txt` contém o mesmo usuário administrador e sua senha original. Abra o domínio somente com certificado válido; no primeiro acesso, o proprietário deve cadastrar seu próprio aplicativo MFA e guardar os códigos de recuperação. Não foi cadastrado fator definitivo em seu nome. Enquanto o acesso externo não estiver resolvido, a recuperação no próprio servidor permanece em `https://localhost:8443/entrar`, com a CA local já exportada no diretório privado. Não aceite um certificado desconhecido como solução para a borda pública.

Configuração e credenciais ficam em `/home/enok/.local/share/chatisp/pronto-hoje-20261106/` (diretório 0700, arquivos sensíveis 0600). `controle.py ps` inspeciona o projeto; `controle.py stop` preserva volumes; `controle.py up -d --no-build --wait` retoma a mesma instalação. Não use `down -v` nem outro nome de projeto.

Backups anteriores, teste de restauração e procedimento seguro estão em [backup.md](backup.md). O snapshot Apache anterior e a configuração posterior ficam também no diretório privado `/home/enok/.local/share/chatisp/publicacao-20261106/`, com checksums. Essas cópias permanecem no mesmo host; não substituem destino independente/cofre.

## Fechamento do escopo

A tabela oficial foi revisada integralmente: ERP, verificação/aprovação sensível, Evolution e expurgo ainda têm código dependente de contratos/regras ausentes; carga e operação dependem de perfil/metas; os demais itens exigem configuração ou homologação efetiva. Não foram inventados contratos, prazos legais, capacidade ou aceites. A única lista de pendências permanece em [dependencias-externas.md](../../dependencias-externas.md).

Para concluir o objetivo de Internet, é necessário esclarecer/corrigir o encaminhamento de **TCP 80/443 do IP `177.104.156.25` ao proxy `10.10.0.10`**, preservando as regras existentes, ou fornecer a identificação do ingresso público correto. O acesso administrativo disponível nesta sessão não alcançou esse equipamento. Foi solicitada essa informação sem interromper as partes independentes. Também permanece necessário conferir o destino próprio do NexoISP, cujo encaminhamento anterior já respondia com o fluxo do PNETLab. A instalação configurada no domínio e validada pela borda interna não é declaração de publicação externa concluída nem de homologação completa das integrações.
