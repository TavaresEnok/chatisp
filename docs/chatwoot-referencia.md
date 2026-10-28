# Referência Chatwoot

Checkout opcional, fora do build e fora do módulo Go. O Chat ISP não clona, não importa, não compila e não empacota este diretório. A especificação em `docs/especificacao.md` continua sendo a fonte de escopo. A coluna Chatwoot da matriz é referência aproximada de comportamento; não foi revalidada linha a linha contra este commit (D.1).

## Checkout fixado

| Campo | Valor |
| --- | --- |
| Caminho | `/home/enok/Documentos/projetos/webchat/chatwoot` |
| Commit | `f428ad8e6f0390faa0ef2ac8e94d8886d56f3017` |
| Commit curto | `f428ad8e6f` |
| Descrição Git | `v4.11.2-1137-gf428ad8e6f` |
| Assunto do commit | `feat(voice): save generated audio for greeting and unanswered announcements (#16444)` |
| Data da fixação | 28/10/2026 |

Qualquer estudo usa este commit. Outro commit exige atualizar esta tabela antes de citar arquivo ou linha.

## Regras de uso

- Estudar somente código Community, para comportamento e ideias de caso de teste.
- O `LICENSE` da raiz declara MIT Expat para o conteúdo que não está nas exceções. Tudo sob `enterprise/` usa a licença definida em `enterprise/LICENSE`, que é licença comercial da Chatwoot.
- `enterprise/` existe neste checkout. Esse diretório não é base de implementação, cópia, adaptação ou teste do Chat ISP.
- Não copiar componente de licença comercial, inclusive recursos Premium, Enterprise ou de produto comercial citado na matriz (Captain, SLA contratual, políticas de atribuição fechadas, white label completo).
- Não importar gem, pacote npm, asset ou trecho deste checkout para `backend/` ou `web/`.
- Não compilar, não rodar `bundle`, `pnpm`, `npm`, Docker ou a suíte do Chatwoot como parte do build do Chat ISP.
- Quando um comportamento for de fato lido no código, registrar na tabela abaixo o commit, o arquivo, o que foi observado e a diferença intencional. Sem essa linha, o código do Chat ISP não deve ser justificado como “igual ao Chatwoot”.

## Comportamentos estudados

Tabela vazia de propósito neste M0. Nenhum arquivo do checkout foi usado como base de implementação. Linhas entram só depois de um estudo pontual, ligado a um marco.

| Data | Commit | Arquivo no checkout | Comportamento observado | Diferença intencional no Chat ISP | Marco |
| --- | --- | --- | --- | --- | --- |

## O que a especificação já resolve sem este checkout

A matriz das seções 01 a 15 diz o que é V1, V1 simples, V2 ou fora do produto. Dúvida de produto volta para `docs/especificacao.md`, não para o código do Chatwoot. Integrações de ERP, Meta e Evolution seguem documentação primária desses sistemas e `docs/perguntas.md`.
