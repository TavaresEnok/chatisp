# M3B — diretório operacional de atendentes

`GET /api/panel/inboxes/{inbox_id}/directory?after=<uuid>&limit=<1..100>` (operação `attendance-directory`).
Distinto de `/api/panel/memberships`, que continua exclusivo de administradores.

## Resposta
`{ "items": [{ "membership_id", "role", "label" }], "next_cursor": uuid | null }`

- `label`: o schema `users` não tem nome de exibição; o rótulo é o **e-mail** da identidade real do vínculo.
  Nenhum nome é inventado nem copiado: o valor é lido da identidade atual a cada consulta.
- Paginação explícita por `membership_id` (ordem crescente, `after` = último ID visto, padrão 50, máximo 100),
  independente da página de conversas. A UI percorre todas as páginas da caixa selecionada.

## Autorização
- Sessão persistida, tenant ativo e vínculo `admin|supervisor|agent` ativo; superadmin negado.
- A caixa precisa ser autorizada ao principal (mesma política de setores/caixas das demais rotas); agente fora
  dos setores ativos da caixa recebe 403.
- Retorna apenas vínculos ativos que operam a caixa: `admin`/`supervisor` (acesso a todas as caixas) e agentes em
  setores ativos vinculados à caixa. Para agentes, esses setores também devem pertencer ao próprio chamador:
  setores distintos que compartilham a caixa não expõem identidades entre si. Admin/supervisor preservam o
  escopo de toda a caixa. Interseção feita no banco, nunca filtrada no navegador.
- Implementação: função `operator_directory` (migração 00017), `SECURITY DEFINER`, `search_path` fixo,
  `EXECUTE` somente para `chatisp_app`. Revalida internamente tenant (`app.tenant_id`), vínculo, usuário, papel e
  escopo da caixa a partir de linhas persistidas; os argumentos não concedem acesso. `chatisp_app` continua sem
  `SELECT` em `users`.

## Limites
- Só resolve rótulos/opções de filtro. **Não amplia elegibilidade de transferência**: candidatos e capacidade
  continuam vindo de `assignment-candidates`.
- Nunca expõe credenciais, status global ou dados de outros tenants/caixas. Membro revogado some da lista; ID
  sem rótulo é exibido como "Atendente indisponível".
- Chaves de cache da UI: `m3b-directory` + tenant/usuário/sessão/papel/vínculo/caixa; removidas na revogação
  e ao trocar de principal; respostas de outra caixa não são reaproveitadas.
- Revogação por 1008 ou negação HTTP da fila cancela/remove o diretório mesmo sem conversa selecionada,
  desabilita seu consumo e descarta respostas pendentes. A paginação percorre até `next_cursor=null`, sem
  truncamento silencioso; cursor repetido falha explicitamente.

## Políticas do dono (migração 00017)
Como o dono da função está sujeito a `FORCE RLS`, a migração adiciona políticas `directory_owner_read` (somente
`SELECT`, papel `chatisp_owner`) em tenants, memberships, sectors, sector_memberships, sector_inboxes e inboxes.
Valem só para `tenant_id = app.tenant_id` e enquanto a própria função mantém `app.directory_read='on'` (restaurado
ao final). Não alteram o que `chatisp_app` enxerga nem concedem acesso a `users`.
### Destinos de transferência

`GET /api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/transfer-sectors`
valida a sessão persistida e o acesso à conversa de origem. Retorna somente ID
e nome dos setores ativos ligados à caixa, conforme o comando de transferência,
inclusive destinos fora dos vínculos do agente. Não concede acesso ao conteúdo
ou às identidades do destino. Setores sem ligação à caixa não são oferecidos.

Negativa 403/404 do próprio diretório remove os rótulos e seu cache; esse escopo
fica desabilitado durante a sessão da página, inclusive após invalidação de query.
