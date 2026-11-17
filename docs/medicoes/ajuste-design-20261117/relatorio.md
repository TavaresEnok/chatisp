# Ajuste de acabamento do design — 17/11/2026

Publicação só do frontend, a pedido do proprietário. Código: `7a8056e` e `608b2f9`, compilados a partir de `1343857`. API, worker, PostgreSQL e Garage não foram reiniciados nem alterados; não há migração nesta atualização.

## Alterações

- A aba de expandir o menu volta a fazer parte da borda da pílula, sem fundo próprio. A área de toque de 24 px foi mantida.
- Cartões da fila em três linhas: protocolo, setor e sinais na mesma linha. O contador de não lidas mostra só o número; o texto por extenso continua no documento para leitores de tela.
- Abas da fila e dos detalhes com a largura do próprio texto: "Não atribuídas" não é mais cortada e os quatro grupos dos detalhes cabem em uma linha.
- Painel: laranja só no alerta de espera aceso; sem cartão dentro de cartão.
- Detalhes voltam a ficar ao lado da conversa a partir de 1280 px. Com o corte em 1440 px, os roteiros M3B e M4A, que rodam em 1280 px, falhavam no `main`.

## Verificação antes de publicar

Em ambiente local descartável (PostgreSQL embutido, API e worker reais, Meta simulado): Vitest 335/335 em 65 arquivos; `tsc`, ESLint e build sem erros; Playwright sem skip aprovado em `m3b`, `m4a`, `m4b-m6`, `m7-ops` e `ux` (52 telas auditadas, 0 falhas). O CI do GitHub passou no ramo `ajuste/design-20261116`.

## Publicação

O frontend foi compilado no servidor a partir de uma cópia limpa do commit, com as mesmas variáveis dos quatro acessos públicos de demonstração já autorizados. A imagem nova do Caddy é a anterior com `/srv/panel` substituído. Só o serviço `caddy` foi recriado; os cinco serviços ficaram saudáveis.

Conferido em https://chatisp.tokstiloesporte.com depois da troca: `/entrar` 200, `/healthz` 200, sessão anônima 401; a folha de estilo servida contém as regras novas; entrada pelo atalho público "Supervisão", fila com 50 cartões de 86 px de altura, aba do menu sem fundo, quatro abas de detalhes em uma linha, detalhes visíveis em 1440 px, sem rolagem horizontal e sem erros de página. Nenhum erro nos logs de Caddy, API e worker na hora seguinte.

Edge: `sha256:856b6fdea6bebd50169086fa7612b932a31f75044b242354977570dfd4cb9d21`. Runtime inalterado.

## Não incluído

- O commit `1343857` (permissão de reindexação do River) é de backend e **não foi publicado**: exige imagem nova do runtime e migração. Os erros noturnos `maintenance.Reindexer` continuam no log do worker até essa publicação.
- Os pacotes de download não foram regenerados; os arquivos servidos são os mesmos da publicação anterior.
- WhatsApp, ERP, Evolution e SMTP reais não foram ativados nem testados.

## Reversão

```sh
docker tag chatisp-pronto-hoje:edge-before-ajuste-design-20261117 chatisp-pronto-hoje:edge
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py up -d --no-deps --no-build --wait caddy
```

Provas privadas: `.tmp/ajuste-design-release/`.
