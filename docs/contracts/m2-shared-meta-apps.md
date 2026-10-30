# App Meta compartilhado

A migração 00008 associa cada vínculo de tenant ao registro global de App ID. A configuração abre o segredo canônico pelo ingresso e compara a entrada em tempo constante; a função transacional `bind_meta_app` confirma sob bloqueio a identidade e o material lidos antes de associar o tenant. Um registro novo e seu vínculo são criados na mesma transação. A associação não permite rotação global por administrador de tenant. Tokens permanecem por conector.

Segredos migrados preservam ciphertext e contexto `legacy_tenant`, reconstruído e validado antes da abertura; novos segredos usam `ingress_registry/meta_app_registry/UUID/app_secret`. A listagem recebe apenas a versão da chave do registro associado ao tenant. O ingresso continua sendo a única interface que abre o segredo canônico. `ResolveApp` usa o registro global e `ResolveRoute` usa o vínculo app e número; a verificação do WABA do item permanece responsabilidade da fronteira de roteamento.

A reversão da 00008 recusa vínculos compartilhados e qualquer vínculo sem representação legada íntegra, incluindo identidade, estado, ciphertext e contexto autenticado. A verificação alcança todos os tenants na transação de migração; uma recusa preserva a associação canônica e as políticas RLS.

No upgrade, a 00008 sincroniza transacionalmente apps e segredos legados gravados após a 00007, preservando ciphertext e AAD; divergências com o registro canônico abortam a migração. A inspeção do painel não expõe quarentenas globais sem atribuição confiável ao tenant.
