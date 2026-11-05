# Matriz de requisitos V1 e V1 simples

Contagem: Implementado e verificado: 195; Implementado, aguardando homologação externa: 42; Não implementado: 5; Parcial, com lacuna concreta: 15.

Gerada em 04/11/2026 a partir das 257 linhas de `docs/plano.md`. Estados: **Implementado e verificado**, **Implementado, aguardando homologação externa**, **Parcial, com lacuna concreta**, **Não implementado**, e **A conferir**: o plano e as entregas citam o requisito, mas esta rodada ainda não confrontou código e teste. "A conferir" não é conclusão. Foram classificadas as linhas com estado explícito em documento de entrega e as de M1 a M8 conferidas em 04/11/2026 contra nomes de teste existentes e a suíte Go completa verde (`go test ./...`, código 0), sem reexecutar cada teste isoladamente; nenhuma foi marcada como verificada por existência de código.

| Requisito | Marco | Tipo | Estado | Evidência / documentos que citam |
| --- | --- | --- | --- | --- |
| 12-tenants | M1 | V1 | Implementado e verificado | platform/db/tenant_status_gate_test.go; TestSchemaForcesTenantRLSAndCompositeFK |
| 12-isolamento | M1 | V1 | Implementado e verificado | TestSchemaForcesTenantRLSAndCompositeFK; testes tenantB em contactdocs/httpapi, ingress/authmeta, platform/db |
| 12-usuarios-convites | M1 | V1 | Implementado e verificado | TestInviteAndResetTokensAreSingleUse, TestAgentLimitBlocksExtraInvites, TestFailedMailInvalidatesInvite |
| 12-papeis | M1 | V1 | Implementado e verificado | platform/httpapi/roles_test.go (papéis admin/supervisor/agent); RoleAllows |
| 12-login | M1 | V1 | Implementado e verificado | TestArgon2idHashIsVersionedAndNotReversible (platform/auth) |
| 12-reset-senha | M1 | V1 | Implementado e verificado | TestInviteAndResetTokensAreSingleUse |
| 12-mfa | M1 | V1 | Implementado e verificado | TestMFAAttemptLimit, TestAdminWithoutMFACannotUseProtectedRoutes, TestAdminMustEnrollBeforePanelAndRecoveryIsSingleUse, TestKeyRotationKeepsReadingOldTOTP |
| 12-sessoes | M1 | V1 | Implementado e verificado | TestSessionCookieFlagsAndHashedSecret, TestPanelRejectsBadOriginCSRFAndRevokedSession; login HTTPS real em 04/11 |
| 12-bloqueio-login | M1 | V1 | Implementado e verificado | TestProgressiveLockoutByAccountAndIP, TestMFAAttemptLimit |
| 12-https | M1 | V1 | Parcial, com lacuna concreta | Caddyfile e cookie Secure existem; HTTPS local verificado; Caddy/Compose não executado neste host (sem Docker) |
| 12-credenciais-cifradas | M1 | V1 | Implementado e verificado | TestSealRejectsTransplantedCiphertext, TestKeyRotationKeepsReadingOldTOTP, TestManualConfigurationIsolationVersionAndSecret |
| 12-fuso | M1 | V1 | Implementado e verificado | businesshours TestBoundariesAndDST, reports (fuso do provedor), lifecycle/service_test.go usam o fuso do tenant |
| 12-auditoria | M1 | V1 | Implementado e verificado | TestInviteAndResetTokensAreSingleUse confere invitation.create/accept, login, password_reset.request/confirm no audit_logs do provedor; a revogação (session.revoke) e a desativação de vínculo têm teste próprio; mfa.* grava auditoria por código sem teste dedicado |
| 05-convite | M1 | V1 | Implementado e verificado | mesmos testes de 12-usuarios-convites |
| 06-fuso | M1 | V1 | Implementado e verificado | businesshours TestBoundariesAndDST, reports (fuso do provedor), lifecycle/service_test.go usam o fuso do tenant |
| 13-painel-web | M1 | V1 | Parcial, com lacuna concreta | jornadas reais no Chromium 140 (05/11/2026) exercitaram Painel, Conversas, Relatórios e Configurações com login e sessão; Contatos e Equipes só em Vitest; responsividade e acessibilidade não auditadas |
| 13-responsiva | M1 | V1 | Parcial, com lacuna concreta | sem verificação em navegador nesta rodada |
| 13-pt-br | M1 | V1 | Implementado e verificado | web/index.html lang="pt-BR"; varredura de literais em inglês nos .tsx não achou rótulos de interface em inglês; mensagens do backend em português (problemas HTTP, validações); jornadas E2E no Chromium em 05/11/2026 só com textos em português. Sem auditoria linguística humana |
| 13-a11y | M1 | V1 | Parcial, com lacuna concreta | sem auditoria em navegador nesta rodada; Vitest com rótulos passou |
| 13-self-host | M1 | V1 | Parcial, com lacuna concreta | deploy/ existe; não executado sem Docker |
| 13-saas | M1 | V1 | Implementado e verificado | testes com segundo tenant: platform/db/tenant_status_gate_test.go, ingress/authmeta/handler_test.go |
| 13-compose | M1 | V1 | Parcial, com lacuna concreta | deploy/compose.yaml existe; não executado sem Docker |
| 14-rls | M1 | V1 | Implementado e verificado | TestSchemaForcesTenantRLSAndCompositeFK |
| 14-csrf | M1 | V1 | Implementado e verificado | TestPanelRejectsBadOriginCSRFAndRevokedSession; realtime_test.go (Origin do WebSocket) |
| 14-cache | M1 | V1 | Implementado e verificado | TestIdentityCacheServesTheTenantAndDropsOnWrite, TestCacheIsolatesTenantAndRejectsMessages |
| 14-isolamento | M1 | V1 | Implementado e verificado | RLS, cache por tenant (TestCacheIsolatesTenantAndRejectsMessages) e filas/anexos por tenant nos pacotes ingress, attachments; ver testes de dois tenants |
| 14-logs | M1 | V1 | Implementado e verificado | logging.WithTenant + handler (TestTenantFromContextIsLoggedAndOtherwiseAbsent); logs com escopo de tenant convertidos para variantes com contexto: auth (65), presença no ciclo da sessão (httpapi/panel.go), realtime (connection.go), chegada/distribuição (cmd/chatisp/arrival.go), configuração de canal (configure.go, resolve.go); expansion.go já passava tenant_id. Sem tenant por natureza: runtime, migrate, panic recovery, webhook da Meta antes de resolver a rota, login antes de achar o vínculo. Os pontos de chamada foram convertidos por revisão de código e compilação, sem teste por ponto; redação de segredos testada |
| 14-migracoes | M1 | V1 | Implementado e verificado | TestBootstrapAndMigrationsAreIdempotent; migrações aplicadas em banco novo pela suíte |
| 14-jobs-restart | M1 | V1 | Implementado e verificado | TestDispatch_RealHttptest_CrashRecoveryAndRiverRestart; platform/jobs/postgres_test.go |
| 14-modos | M1 | V1 | Implementado e verificado | platform/runtime/runtime_test.go, cmd/chatisp/smoke_test.go |
| 14-provisionamento | M1 | V1 | Implementado e verificado | platform/httpapi/panel_test.go; web/src/pages/platform-tenants-page.tsx |
| 01-cloud-api | M2 | V1 | Implementado, aguardando homologação externa | integracoes/meta.md (D2-03) |
| 01-config-manual | M2 | V1 | Implementado, aguardando homologação externa | TestManualConfigurationIsolationVersionAndSecret; servidor falso, sem Meta real (D2-03) |
| 01-multi-numeros | M2 | V1 | Implementado e verificado | channel/configuration/service_test.go, shared_app_test.go |
| 01-multi-caixas | M2 | V1 | Implementado e verificado | channel/configuration/service_test.go |
| 01-escolher-conector | M2 | V1 | Implementado e verificado | channel/catalog/schema_test.go |
| 02-receber-texto | M2 | V1 | Implementado, aguardando homologação externa | pipeline (TestInboundTextDoesNotCarryStructure), fake e metacloud/driver_test.go; sem Meta real (D2-03) |
| 02-enviar-texto | M2 | V1 | Implementado, aguardando homologação externa | pipeline (TestInboundTextDoesNotCarryStructure), fake e metacloud/driver_test.go; sem Meta real (D2-03) |
| 02-status | M2 | V1 | Implementado, aguardando homologação externa | pipeline (TestInboundTextDoesNotCarryStructure), fake e metacloud/driver_test.go; sem Meta real (D2-03) |
| 02-falha-envio | M2 | V1 | Implementado, aguardando homologação externa | pipeline (TestInboundTextDoesNotCarryStructure), fake e metacloud/driver_test.go; sem Meta real (D2-03) |
| 02-identidade-canal | M2 | V1 | Implementado e verificado | TestDatabaseRejectsIncompatibleBinding, TestMultitenantPayloadDoesNotAssignDefaultTenant |
| 03-caixa-compartilhada | M2 | V1 | Implementado e verificado | TestInboxesSessionAndMembership; queue tests |
| 03-tempo-real | M2 | V1 | Implementado e verificado | TestSnapshotCursorRollbackAndResync; features/conversation/use-attendance-events.test.tsx |
| 03-historico | M2 | V1 | Implementado e verificado | features/conversation/message-timeline.test.tsx, history-events.test.tsx; TestAttemptStatusAndProtocolAdvanceCursor |
| 03-estado-aberta | M2 | V1 | Implementado e verificado | TestInboundReopensWithinInboxWindow, lifecycle; TestAssumeClosePreservesCycle |
| 03-estado-aguardando | M2 | V1 | Implementado e verificado | store/accepted_reply_test.go (waiting_customer só com resposta aceita mais nova que a última do cliente), contracts/m2-storage.md |
| 03-estado-encerrada | M2 | V1 | Implementado e verificado | TestInboundReopensWithinInboxWindow, lifecycle; TestAssumeClosePreservesCycle |
| 03-assumir | M2 | V1 | Implementado e verificado | TestConcurrentAssumeDoesNotPromoteConversationShareLock, assignment_bridge_test.go (duas assunções simultâneas) |
| 03-protocolo | M2 | V1 | Implementado e verificado | TestConcurrentOpenKeepsOneProtocol |
| 03-eventos | M2 | V1 | Implementado e verificado | features/conversation/message-timeline.test.tsx, history-events.test.tsx; TestAttemptStatusAndProtocolAdvanceCursor |
| 04-encerrar-motivo | M2 | V1 | Implementado e verificado | close-conversation-dialog.test.tsx; TestClosedReplayAndConnectorBinding |
| 11-websocket | M2 | V1 | Implementado e verificado | realtime/realtime_test.go (Origin, assinatura), TestWebSocketGauge* |
| 14-id-mensagem | M2 | V1 | Implementado e verificado | índice único messages_external_key (tenant_id, connector_id, external_message_id) em 00006_attendance.sql; TestStatusDedupLinksWhenMessageArrives |
| 14-webhook-duravel | M2 | V1 | Implementado e verificado | TestInvalidSignatureDoesNotPersist; ingress/authmeta/handler_test.go |
| 14-assinatura-meta | M2 | V1 | Implementado e verificado | TestVerifySignatureUsesOriginalBytesAndSecret, TestInvalidSignatureDoesNotPersist |
| 14-dedup | M2 | V1 | Implementado e verificado | TestStatusDedupLinksWhenMessageArrives, TestBoundedCacheAndDedup |
| 14-outbox | M2 | V1 | Implementado e verificado | TestFakeIsolatedStatusStaysUncertain, TestClassifyAbsentResponseIsUncertain |
| 14-fila | M2 | V1 | Implementado e verificado | TestDispatch_RealHttptest_CrashRecoveryAndRiverRestart; platform/jobs |
| 14-filas-limite | M2 | V1 | Implementado e verificado | TestReserveDispatchTenantGateOrder, TestPublishReservedBudgetAndRelease |
| 14-retry | M2 | V1 | Implementado e verificado | TestBackoffJitterAndCap |
| 14-dlq | M2 | V1 | Implementado e verificado | platform/jobs/telemetry_test.go, opsalert/evaluate_test.go |
| 14-ordenacao | M2 | V1 | Implementado e verificado | web attendance-adapter.ts ordena por trusted_at (horário do driver) com fallback para ingested_at; servidor entrega (ingested_at, id) como desempate estável; attendance-adapter.order.test.ts cobre mensagem atrasada, sem horário do driver e empate. A ordenação no SQL continua por ingestão (a API pública também) |
| 14-conciliacao | M2 | V1 | Implementado e verificado | TestInbound_MessageAndStatusReconciliation, TestSuccessiveAcceptedReplies_SendAndReconcile |
| 14-concorrencia | M2 | V1 | Implementado e verificado | lifecycle/service_test.go (versão da conversa, conflito) |
| 14-rate-limit | M2 | V1 | Implementado e verificado | TestOutboundLeaseAndDiscardedInspection, TestStaleLeaseDoesNotReplaceFence, TestOutbound_ConcurrentLease_Dispute |
| 14-realtime | M2 | V1 | Implementado e verificado | TestSnapshotCursorRollbackAndResync (realtime) |
| 14-notify | M2 | V1 | Implementado e verificado | TestPGNotifySignaling |
| 14-autorizacao | M2 | V1 | Implementado e verificado | TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation, TestQueuedDeliveryRejectsRevokedSector, acceptance/m3a_revocation_test.go |
| 14-alerta-token | M2 | V1 | Implementado e verificado | channel/configuration/service_test.go, opsalert/evaluate_test.go, metacloud/driver_test.go |
| 14-limites-recursos | M2 | V1 | Implementado e verificado | TestPublishReservedBudgetAndRelease, TestReserveDispatchTenantGateOrder |
| 02-nao-lidas | M3 | V1 | Implementado e verificado | TestAnnotationsPrivateAndPersonal, TestAnnotationReadIntentVersion (attendance); features/annotations |
| 03-abas | M3 | V1 | Implementado e verificado | TestQueueHTTP, TestDirectoryPGHTTP; features/queue/queue-integration.test.tsx, queue-controls.test.tsx |
| 03-ve-setores | M3 | V1 | Implementado e verificado | TestInboxesSessionAndMembership, TestOperatorDirectoryFunctionIsRestricted, sectors tests, TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation |
| 03-historico-contato | M3 | V1 | Implementado e verificado | TestHistoryEvents / history-events.test.tsx, TestDirectoryPGHTTP; features/conversation/conversation-sidebar.test.tsx |
| 03-reabrir-manual | M3 | V1 | Implementado e verificado | TestAssumeClosePreservesCycle, TestAssignmentHTTPReplay, TestTransferSectorRevokesSourceAccess; features/assignment |
| 03-transferir-atendente | M3 | V1 | Implementado e verificado | TestAssumeClosePreservesCycle, TestAssignmentHTTPReplay, TestTransferSectorRevokesSourceAccess; features/assignment |
| 03-transferir-setor | M3 | V1 | Implementado e verificado | TestAssumeClosePreservesCycle, TestAssignmentHTTPReplay, TestTransferSectorRevokesSourceAccess; features/assignment |
| 03-retirar-responsavel | M3 | V1 | Implementado e verificado | TestAssumeClosePreservesCycle, TestAssignmentHTTPReplay, TestTransferSectorRevokesSourceAccess; features/assignment |
| 03-notas | M3 | V1 | Implementado e verificado | TestAnnotationsPrivateAndPersonal, TestAnnotationReadIntentVersion (attendance); features/annotations |
| 03-busca-nome | M3 | V1 | Implementado e verificado | TestQueueHTTP, TestDirectoryPGHTTP; features/queue/queue-integration.test.tsx, queue-controls.test.tsx |
| 03-busca-conteudo | M3 | V1 | Parcial, com lacuna concreta | índice GIN m3_message_search medido em 50 mil mensagens sintéticas: Bitmap Index Scan, 0,06 ms, acha com e sem acento (internal/searchplan). A consulta composta da fila (EXISTS por conversa, filtros de setor) não foi medida e pode não usar o índice |
| 03-busca-protocolo | M3 | V1 | Implementado e verificado | TestQueueHTTP, TestDirectoryPGHTTP; features/queue/queue-integration.test.tsx, queue-controls.test.tsx |
| 03-filtro-estado | M3 | V1 | Implementado e verificado | TestQueueHTTP, TestDirectoryPGHTTP; features/queue/queue-integration.test.tsx, queue-controls.test.tsx |
| 03-filtro-caixa | M3 | V1 | Implementado e verificado | TestQueueHTTP, TestDirectoryPGHTTP; features/queue/queue-integration.test.tsx, queue-controls.test.tsx |
| 03-ordenacao | M3 | V1 | Implementado e verificado | TestQueueHTTP, TestDirectoryPGHTTP; features/queue/queue-integration.test.tsx, queue-controls.test.tsx |
| 03-lida | M3 | V1 | Implementado e verificado | TestAnnotationsPrivateAndPersonal, TestAnnotationReadIntentVersion (attendance); features/annotations |
| 03-colisao | M3 | V1 | Implementado e verificado | TestCollisionHTTPAndSessions; features/collision/use-collision.test.tsx |
| 03-reabrir-mensagem | M3 | V1 simples | Implementado e verificado | TestInboundReopensWithinInboxWindow, TestInboundOutsideWindowOpensNewProtocol |
| 03-etiquetas | M3 | V1 simples | Implementado e verificado | migração 00014 (annotation_tags, priority normal/urgent), TestAnnotationsHTTP; features/annotations |
| 03-prioridade | M3 | V1 simples | Implementado e verificado | migração 00014 (annotation_tags, priority normal/urgent), TestAnnotationsHTTP; features/annotations |
| 03-filtro-etiqueta | M3 | V1 simples | Implementado e verificado | TestQueueSearchPermissionsAndPersonalRead (filtro TagID) |
| 04-emojis | M3 | V1 | Implementado e verificado | composer-tools.tsx (emojis) + composer-tools.test.tsx |
| 04-rascunho | M3 | V1 | Implementado e verificado | features/conversation/productivity.test.tsx, message-composer.test.tsx |
| 04-notif-ui | M3 | V1 | Implementado e verificado | TestPreferencesHTTPAndIsolation; features/notifications/notifications.test.tsx |
| 04-notif-som | M3 | V1 | Implementado e verificado | TestPreferencesHTTPAndIsolation; features/notifications/notifications.test.tsx |
| 04-notif-navegador | M3 | V1 | Parcial, com lacuna concreta | lógica coberta em Vitest; Notification API e permissão reais do navegador não exercidas nesta rodada |
| 04-formatacao | M3 | V1 simples | Implementado e verificado | composer-tools.tsx (botões Negrito/Itálico na sintaxe do WhatsApp) + composer-tools.test.tsx; sem editor rico |
| 04-atalhos | M3 | V1 simples | Implementado e verificado | Vitest: Ctrl+Enter envia (productivity.test.tsx), Ctrl+Alt+J próxima conversa, Ctrl+Alt+F buscar e Ctrl+Alt+N nota, sem disparar dentro de campo (conversation-workspace.test.tsx). Playwright de teclado não executado |
| 04-prefs-notif | M3 | V1 simples | Implementado e verificado | TestPreferencesHTTPAndIsolation; features/notifications/notifications.test.tsx |
| 05-equipes | M3 | V1 | Implementado e verificado | TestInboxesSessionAndMembership, TestOperatorDirectoryFunctionIsRestricted, sectors tests, TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation |
| 05-varios-setores | M3 | V1 | Implementado e verificado | TestInboxesSessionAndMembership, TestOperatorDirectoryFunctionIsRestricted, sectors tests, TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation |
| 05-acesso-caixa | M3 | V1 | Implementado e verificado | TestInboxesSessionAndMembership, TestOperatorDirectoryFunctionIsRestricted, sectors tests, TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation |
| 07-criar-receber | M3 | V1 | Implementado e verificado | TestDirectoryPGHTTP, TestContactDocuments |
| 07-criar-manual | M3 | V1 | Implementado e verificado | TestDirectoryPGHTTP, TestContactDocuments |
| 07-nome-telefone | M3 | V1 | Implementado e verificado | TestDirectoryPGHTTP, TestContactDocuments |
| 07-editar | M3 | V1 | Implementado e verificado | TestDirectoryPGHTTP, TestContactDocuments |
| 07-historico | M3 | V1 | Implementado e verificado | TestHistoryEvents / history-events.test.tsx, TestDirectoryPGHTTP; features/conversation/conversation-sidebar.test.tsx |
| 07-bloquear | M3 | V1 | Implementado e verificado | TestBlockedContactQuarantinesInboundAndStopsPendingDispatch (pipeline/m3_contact_block_test.go) |
| 07-mesmo-numero | M3 | V1 | Implementado e verificado | TestSameNumberInTwoProvidersStaysSeparate (contactdirectory/httpapi/same_number_test.go): mesmo telefone em dois provedores gera dois contatos, leitura cruzada recusada, lista de cada provedor só com o seu; contacts_phone_idx não é único |
| 07-email | M3 | V1 simples | Implementado e verificado | TestSameNumberInTwoProvidersStaysSeparate grava, lê e edita o e-mail do contato; contacts-page.tsx |
| 07-avatar | M3 | V1 simples | Não implementado | campo avatar_reference opaco existe e nada o preenche: a Cloud API não entrega foto de perfil e não há armazenamento/serviço de avatar. Renderizar uma URL arbitrária vazaria o IP do atendente; precisa de decisão de fonte e de proxy seguro antes de exibir |
| 07-notas | M3 | V1 simples | Implementado e verificado | mesmo teste grava, lê e edita as notas do contato, separadas das notas da conversa |
| 07-busca | M3 | V1 simples | Implementado e verificado | TestNormalizeAndMask, TestValidationErrorOmitsDocument, TestContactDocuments |
| 07-cpf | M3 | V1 simples | Implementado e verificado | TestNormalizeAndMask, TestValidationErrorOmitsDocument, TestContactDocuments |
| 12-acesso-setor | M3 | V1 | Implementado e verificado | TestInboxesSessionAndMembership, TestOperatorDirectoryFunctionIsRestricted, sectors tests, TestDirectoryDeniesCrossTenantRevokedAndParameterEscalation |
| 12-mascara-cpf | M3 | V1 | Implementado e verificado | TestNormalizeAndMask, TestValidationErrorOmitsDocument, TestContactDocuments |
| 13-lateral | M3 | V1 | Implementado e verificado | TestHistoryEvents / history-events.test.tsx, TestDirectoryPGHTTP; features/conversation/conversation-sidebar.test.tsx |
| 13-atalhos | M3 | V1 simples | Implementado e verificado | mesmos atalhos de 04-atalhos na área Conversas (conversation-workspace.test.tsx) |
| 13-notif-navegador | M3 | V1 | Parcial, com lacuna concreta | lógica coberta em Vitest; Notification API e permissão reais do navegador não exercidas nesta rodada |
| 14-fulltext | M3 | V1 | Parcial, com lacuna concreta | mesma medição de 03-busca-conteudo; pg_trgm em nome/telefone/protocolo existe quando a extensão está instalada, sem medição |
| 02-receber-imagens | M4 | V1 | Implementado, aguardando homologação externa | Inbound* (TestInboundImageWebhookDownloadIdempotent, TestInboundDocumentTextDownload, TestInboundAudioMP4Download) com stand-in; sem Meta real (D2-03) |
| 02-enviar-imagens | M4 | V1 | Implementado, aguardando homologação externa | TestOutboundMediaImageAndDocument, TestOutboundMediaOfficialLoopbackCarriesBytes; sem Meta real |
| 02-receber-documentos | M4 | V1 | Implementado, aguardando homologação externa | Inbound* (TestInboundImageWebhookDownloadIdempotent, TestInboundDocumentTextDownload, TestInboundAudioMP4Download) com stand-in; sem Meta real (D2-03) |
| 02-enviar-documentos | M4 | V1 | Implementado, aguardando homologação externa | TestOutboundMediaImageAndDocument, TestOutboundMediaOfficialLoopbackCarriesBytes; sem Meta real |
| 02-receber-audio | M4 | V1 | Implementado, aguardando homologação externa | Inbound* (TestInboundImageWebhookDownloadIdempotent, TestInboundDocumentTextDownload, TestInboundAudioMP4Download) com stand-in; sem Meta real (D2-03) |
| 02-enviar-audio | M4 | V1 | Implementado, aguardando homologação externa | TestTranscodeRecorded*WithFFmpeg (ffmpeg 4.2.7 do host), TestSendMediaConvertedAudio*; sem envio real à Meta |
| 02-gravar-audio | M4 | V1 | Implementado, aguardando homologação externa | TestTranscodeRecorded*WithFFmpeg (ffmpeg 4.2.7 do host), TestSendMediaConvertedAudio*; sem envio real à Meta |
| 02-receber-video | M4 | V1 | Implementado, aguardando homologação externa | decode_media_test.go (vídeo wamid.video com legenda); stand-in, sem Meta real |
| 02-receber-localizacao | M4 | V1 | Implementado, aguardando homologação externa | TestInboundReactionLocationAndVCard com stand-in; sem Meta real |
| 02-citacao | M4 | V1 | Implementado e verificado | quoteTarget ignora o UUID zero (model.ts) + quote-target.test.ts; citação no envio: TestSendMediaCitationOverlapsInboundCommit; E2E m4b-m6 de 05/11/2026 |
| 02-janela | M4 | V1 | Implementado e verificado | features/channelpolicy/channel-policy.test.tsx; WindowIndicator em conversation-workspace.tsx |
| 02-biblioteca-templates | M4 | V1 | Implementado, aguardando homologação externa | TestDispatchTemplateReachesIdentifiedDriver, TestPolicyPostgres, TestListTemplatesDoesNotTrustCacheWithoutDatabase; catálogo simulado, sem Meta real |
| 02-sync-templates | M4 | V1 | Implementado, aguardando homologação externa | TestDispatchTemplateReachesIdentifiedDriver, TestPolicyPostgres, TestListTemplatesDoesNotTrustCacheWithoutDatabase; catálogo simulado, sem Meta real |
| 02-envio-template | M4 | V1 | Implementado, aguardando homologação externa | TestDispatchTemplateReachesIdentifiedDriver, TestPolicyPostgres, TestListTemplatesDoesNotTrustCacheWithoutDatabase; catálogo simulado, sem Meta real |
| 02-download-midia | M4 | V1 | Implementado, aguardando homologação externa | Inbound* (TestInboundImageWebhookDownloadIdempotent, TestInboundDocumentTextDownload, TestInboundAudioMP4Download) com stand-in; sem Meta real (D2-03) |
| 02-consentimento | M4 | V1 | Implementado, aguardando homologação externa | TestDispatchTemplateReachesIdentifiedDriver, TestPolicyPostgres, TestListTemplatesDoesNotTrustCacheWithoutDatabase; catálogo simulado, sem Meta real |
| 02-vcard | M4 | V1 simples | Implementado, aguardando homologação externa | TestInboundReactionLocationAndVCard com stand-in; sem Meta real |
| 02-figurinhas | M4 | V1 simples | Implementado, aguardando homologação externa | decode_media.go decodifica sticker; media-message.tsx exibe; sem Meta real |
| 02-reacoes | M4 | V1 simples | Implementado, aguardando homologação externa | TestInboundReactionLocationAndVCard com stand-in; sem Meta real |
| 02-interativos | M4 | V1 simples | Implementado, aguardando homologação externa | decode de interactive e fallback numerado em triage (TestUnambiguousNumberedChoice); sem Meta real |
| 02-saude-numero | M4 | V1 simples | Não implementado | nenhum código de qualidade/saúde do número encontrado |
| 02-limites-numero | M4 | V1 simples | Não implementado | nenhum código de tier/limite de mensagens do número encontrado |
| 04-compositor | M4 | V1 | Implementado e verificado | E2E no Chromium 140 em 05/11/2026: m4a (texto, assinatura, resposta pronta, anexo validado) e m4b-m6 (documento, localização citada, áudio gravado convertido no servidor); message-composer*.test.tsx; composer-tools.test.tsx |
| 04-drag-colar | M4 | V1 | Implementado e verificado | message-composer.media.test.tsx ("anexa, arrasta e cola um arquivo"): fireEvent.paste e drop passam pela mesma validação de anexo |
| 04-preview | M4 | V1 | Implementado e verificado | features/attachments/attachments.test.tsx, media-panel.test.tsx; attachment-preview.tsx |
| 04-download-seguro | M4 | V1 | Implementado e verificado | TestPersistentAuthorizedAttachmentLifecycle; features/attachments/attachments.test.tsx |
| 04-respostas-prontas | M4 | V1 | Implementado e verificado | TestPersistentHTTPAndPolicy, TestClosedVariables; features/cannedreplies/slash-replies.test.tsx, canned-rules.test.tsx |
| 04-atalho-barra | M4 | V1 | Implementado e verificado | TestPersistentHTTPAndPolicy, TestClosedVariables; features/cannedreplies/slash-replies.test.tsx, canned-rules.test.tsx |
| 04-assinatura | M4 | V1 simples | Implementado e verificado | TestHumanSignatureOnceAndTemplateOptOut |
| 04-variaveis | M4 | V1 simples | Implementado e verificado | TestPersistentHTTPAndPolicy, TestClosedVariables; features/cannedreplies/slash-replies.test.tsx, canned-rules.test.tsx |
| 07-iniciar-template | M4 | V1 | Implementado, aguardando homologação externa | TestDispatchTemplateReachesIdentifiedDriver, TestPolicyPostgres, TestListTemplatesDoesNotTrustCacheWithoutDatabase; catálogo simulado, sem Meta real |
| 14-download-midia | M4 | V1 | Implementado e verificado | TestInboundMediaFailuresStayOnTheMessage, TestInboundAttachmentBudget, TestInboundImageWebhookDownloadIdempotent |
| 14-s3 | M4 | V1 | Implementado, aguardando homologação externa | TestS3SDKWithExplicitlySimulatedTransport; storage S3 real não usado |
| 14-url-assinada | M4 | V1 | Implementado, aguardando homologação externa | TestS3SDKWithExplicitlySimulatedTransport; storage S3 real não usado |
| 14-upload | M4 | V1 | Implementado e verificado | TestTypeAndNameValidation (attachments); HTML nomeado foto.jpg recusado com 422 no E2E M4A de 04/11 |
| 14-ffmpeg | M4 | V1 | Implementado e verificado | convert.go: tempo, duração e tamanho + limites de processo por prlimit (CPU 30 s, espaço de endereço 2 GiB, arquivo 16 MiB, 64 descritores), provados em TestFFmpegChildInheritsResourceLimits; as conversões reais com ffmpeg 4.2.7 passam pelo caminho limitado. Não é cgroup; host sem prlimit roda só com tempo/duração/tamanho |
| 14-uso-meta | M4 | V1 simples | Não implementado | nenhuma contabilização de categoria/cobrança reportada pela Meta encontrada |
| 03-triagem | M5 | V1 simples | Implementado e verificado | TestMenuLimitsAndEmptyDecision, TestUnambiguousNumberedChoice, TestPostgresAndHTTP (triage); acceptance |
| 03-adiar | M5 | V1 simples | Implementado e verificado | TestLifecyclePostgres (lifecycle); TestAutomationInboundClearsSnoozeInMessageTransaction |
| 04-presenca | M5 | V1 | Implementado e verificado | TestPresencePostgres (presence); features/queue |
| 05-ativar-desativar | M5 | V1 | Implementado e verificado | TestDisabledUserLosesSessions (admin_test.go) + TestReleaseDisabledReturnsConversationsToTheQueue (pipeline/disabled_test.go), chamado em Automation.RunTenant |
| 05-responsavel-unico | M5 | V1 | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-atribuicao-manual | M5 | V1 | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-atribuicao-auto | M5 | V1 | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-round-robin | M5 | V1 | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-disponibilidade | M5 | V1 | Implementado e verificado | TestPresencePostgres (presence); features/queue |
| 05-prioridade-espera | M5 | V1 | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-reatribuir-desativado | M5 | V1 | Implementado e verificado | TestReleaseDisabledReturnsConversationsToTheQueue: conversa volta à fila com evento offline_unassigned e intervalo de fila; repetição não duplica. Distribuição automática posterior reutiliza a fila |
| 05-supervisor-filas | M5 | V1 | Implementado e verificado | TestQueueSupervisorSeesAllSectorsAndNoOtherTenant (service/queue_supervisor_test.go): supervisor sem vínculo de setor vê a fila, atendente sem vínculo não, supervisor de outro provedor não |
| 05-supervisor-altera | M5 | V1 | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-limite-vagas | M5 | V1 simples | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-apos-transferencia | M5 | V1 simples | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 05-desatribuir-offline | M5 | V1 simples | Implementado e verificado | TestPresencePostgres (presence); features/queue |
| 05-ocupacao | M5 | V1 simples | Implementado e verificado | TestDistributionPostgres (distribution); TestAssignmentHTTPReplay |
| 06-horario | M5 | V1 | Implementado e verificado | TestValidation, TestBoundariesAndDST, TestPostgresAndHTTP (businesshours); business-hours.test.tsx |
| 06-fora-expediente | M5 | V1 | Implementado e verificado | triage/decide.go usa a decisão de businesshours; triage/integration_test.go; TestBoundariesAndDST |
| 06-setor-padrao | M5 | V1 | Implementado e verificado | TestMenuLimitsAndEmptyDecision, TestUnambiguousNumberedChoice, TestPostgresAndHTTP (triage); acceptance |
| 06-atribuicao-chegar | M5 | V1 | Implementado e verificado | TestMenuLimitsAndEmptyDecision, TestUnambiguousNumberedChoice, TestPostgresAndHTTP (triage); acceptance |
| 06-menu | M5 | V1 | Implementado e verificado | TestMenuLimitsAndEmptyDecision, TestUnambiguousNumberedChoice, TestPostgresAndHTTP (triage); acceptance |
| 06-inatividade | M5 | V1 | Implementado e verificado | TestLifecyclePostgres (lifecycle); TestAutomationInboundClearsSnoozeInMessageTransaction |
| 06-retorno-adiada | M5 | V1 | Implementado e verificado | TestLifecyclePostgres (lifecycle); TestAutomationInboundClearsSnoozeInMessageTransaction |
| 06-plantao | M5 | V1 simples | Implementado e verificado | TestValidation, TestBoundariesAndDST, TestPostgresAndHTTP (businesshours); business-hours.test.tsx |
| 06-boas-vindas | M5 | V1 simples | Implementado e verificado | triage/integration_test.go: boas-vindas enviada uma vez e não repetida no reinício (withWelcome); o texto do plano sobre "pendente" está desatualizado |
| 06-ninguem-disponivel | M5 | V1 simples | Implementado e verificado | triage/integration_test.go linhas 319-326: texto "Ninguém disponível." decidido e ausente quando há vaga; fora-expediente nas linhas 331-338 |
| 06-gancho | M5 | V1 simples | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 06-etiqueta-menu | M5 | V1 simples | Implementado e verificado | TestMenuLimitsAndEmptyDecision, TestUnambiguousNumberedChoice, TestPostgresAndHTTP (triage); acceptance |
| 06-gatilhos | M5 | V1 simples | Implementado e verificado | interpretado como eventos internos fixos: conversation_events + realtime + webhooks (outwebhook tests) + gancho do bot; sem editor de regras |
| 08-historico-bot | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-resposta-bot | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-handoff | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-interruptor | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-aviso-assistente | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-humano-assume | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-timeout-bot | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-autorizacao-acao | M5 | V1 | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-entregar-ia | M5 | V1 simples | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 08-aprovacao | M5 | V1 simples | Parcial, com lacuna concreta | não implementado de propósito: não existe ação sensível para aprovar enquanto a verificação do cliente (D2-02) e o adaptador real do ERP (D2-01) faltam, e TestSensitiveStaysHiddenWithoutVerification mantém a ação oculta. Um mecanismo de aprovação sem consumidor seria código morto; construir junto com a primeira ação sensível |
| 11-gancho | M5 | V1 simples | Implementado e verificado | TestBotHookPostgres, TestParseToken (bothook); bot-hook-notice.test.tsx |
| 01-evolution | M6 | V1 simples | Implementado, aguardando homologação externa | Flag desligada; TestSupplierStaysUnwired; docs/contracts/m6-evolution.md (D2-04) |
| 07-id-erp | M6 | V1 | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 07-vinculo-erp | M6 | V1 | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-referencia-erp | M6 | V1 | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-contexto-painel | M6 | V1 | Implementado e verificado | embed tests (origem, URL interna recusada, payload sem corpo); E2E M4B-M6 de 04/11 |
| 08-registro-acao | M6 | V1 | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-permissao-erp | M6 | V1 | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-contrato-minimo | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-conector-erp | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-identificar | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-verificacao-cliente | M6 | V1 simples | Parcial, com lacuna concreta | aprovação humana de ação sensível: sem código encontrado em erp/; D2-02 ausente, ação sensível oculta (TestSensitiveStaysHiddenWithoutVerification) |
| 08-resumo | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-faturas | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 08-segunda-via | M6 | V1 simples | Implementado, aguardando homologação externa | TestFakeSummaryAndInvoicesHonorContract; segunda via enfileirada no E2E de 04/11 com fake; Pix, artefato real e timeout ambíguo real não exercitados |
| 08-abrir-cadastro | M6 | V1 simples | Não implementado | sem link para o cadastro no ERP; depende da URL do ambiente do fornecedor (D2-01) |
| 08-iframe | M6 | V1 simples | Implementado e verificado | embed tests (origem, URL interna recusada, payload sem corpo); E2E M4B-M6 de 04/11 |
| 08-protocolo-erp | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 11-api-conversas | M6 | V1 | Implementado e verificado | TestPublicConversationTextAssignAndState, TestPublicTemplate* |
| 11-api-contatos | M6 | V1 | Implementado e verificado | TestUpdateContactKeepsDocumentOut; PATCH /v1/contacts |
| 11-api-template | M6 | V1 | Implementado e verificado | TestPublicConversationTextAssignAndState, TestPublicTemplate* |
| 11-tokens | M6 | V1 | Implementado e verificado | TestTokenScopeTenantAndRateLimit, TestTokenRevogado, TestLimite429, TestCookieNaoSubstituiBearer |
| 11-rate-limit | M6 | V1 | Implementado e verificado | TestTokenScopeTenantAndRateLimit, TestTokenRevogado, TestLimite429, TestCookieNaoSubstituiBearer |
| 11-openapi | M6 | V1 | Implementado e verificado | TestOpenAPIMatchesCommittedDocument, TestOpenAPIDocumentsPublicWrites |
| 11-webhook-conversa | M6 | V1 | Implementado e verificado | outwebhook: TestStableIDAndSignature, TestRetryKeepsIDAndDoesNotDropTheMessage, TestDeadAfterTheAttemptBudget, TestConcurrentDeliveryPostsOnce |
| 11-webhook-mensagem | M6 | V1 | Implementado e verificado | outwebhook: TestStableIDAndSignature, TestRetryKeepsIDAndDoesNotDropTheMessage, TestDeadAfterTheAttemptBudget, TestConcurrentDeliveryPostsOnce |
| 11-api-cadastros | M6 | V1 simples | Implementado e verificado | TestOpenAPIMatchesRegisteredRoutes (leitura pública) |
| 11-dashboard-app | M6 | V1 simples | Implementado e verificado | embed tests (origem, URL interna recusada, payload sem corpo); E2E M4B-M6 de 04/11 |
| 11-conector-erp | M6 | V1 simples | Implementado, aguardando homologação externa | TestERPPostgres, TestFakeIsDeterministicAndLabeled, TestSupplierStaysUnwired: fake identificado, adaptador real ausente (D2-01) |
| 14-circuit-breaker | M6 | V1 | Implementado e verificado | TestBreakerOpensAfterThreeFailuresAndReopensOnFailedProbe, TestCircuitOpensPerConnection, TestGuardTimeoutDoesNotWaitForTheDelay; decisão em integracoes/erp-existente.md |
| 14-observabilidade | M6 | V1 | Implementado e verificado | opsignals/metrics, opsalert; /metrics privado verificado no E2E M7 de 04/11 |
| 09-painel-rt | M7 | V1 | Implementado e verificado | reports.livePicture (fila, em atendimento, online, maior espera): TestCompute e query_test.go linhas 62-77 (inclui corte por setor); attendance-report.test.tsx modo live; pages/dashboard-page.tsx |
| 09-sem-responsavel | M7 | V1 | Implementado e verificado | TestUrgentQueueDoesNotRaiseTheWaitAlert; queue-wait-alert.test.tsx |
| 09-total-periodo | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-estados | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-primeira-resposta | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-tempo-encerramento | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-espera-fila | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-volume-setor | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-volume-atendente | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-volume-etiqueta | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-mapa-calor | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-agrupamento | M7 | V1 | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-painel-saas | M7 | V1 | Implementado e verificado | TestUsageHidesMessageBodyAndRejectsProviderRoles (platformusage) |
| 09-volume-mensagens | M7 | V1 simples | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-volume-caixa | M7 | V1 simples | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-csv | M7 | V1 simples | Implementado e verificado | reports: TestPersistedCycleMatchesTheClockRules, TestOperationalTimeSubtractsWaitingOnce, TestTransferSplitsTimeAndKeepsBothSectors, TestTagsDoNotEnterThePrimaryTotal, TestMedianIgnoresMissingHumanResponse; attendance-report.test.tsx |
| 09-alerta-espera | M7 | V1 simples | Implementado e verificado | TestUrgentQueueDoesNotRaiseTheWaitAlert; queue-wait-alert.test.tsx |
| 12-retencao | M7 | V1 simples | Parcial, com lacuna concreta | retenção registra e recusa: TestPurgeRefusesAndDeletesNothing; expurgo desligado até D2-06 |
| 12-exportacao-contato | M7 | V1 simples | Implementado e verificado | TestPrivacyExportAndDeleteKeepsProtocol, TestPrivacyDeleteRollsBackWhenObjectRemains, TestHTTPPrivacyExportAndDeletion |
| 12-historico-demandas | M7 | V1 simples | Implementado e verificado | TestExportDemandsKeepsProtocolAndHidesOtherSectors |
| 12-logo | M7 | V1 simples | Implementado e verificado | branding: TestNormalizeRejectsActiveContentMisleadingTypeAndLimits, TestHTTPRolesMFAOriginAndImageHeaders; E2E M7 de 04/11 |
| 12-limites-plano | M7 | V1 simples | Implementado e verificado | TestPlanLimitsBlockNewRowsAndKeepHistory |
| 12-superadmin | M7 | V1 | Implementado e verificado | TestAdminSeesOwnTenantAudit, TestUsageHidesMessageBodyAndRejectsProviderRoles; platform/httpapi/panel_test.go |
| 12-criar-suspender | M7 | V1 | Implementado e verificado | platform/db/tenant_status_gate_test.go; panel_test.go |
| 14-retencao | M7 | V1 simples | Parcial, com lacuna concreta | retenção registra e recusa: TestPurgeRefusesAndDeletesNothing; expurgo desligado até D2-06 |
| 14-suspensao | M7 | V1 | Implementado e verificado | platform/db/tenant_status_gate_test.go; panel_test.go |
| 14-backup | M8 | V1 | Implementado, aguardando homologação externa | restoredrill: TestDrillRestoresDumpAndReapplies, TestRecoveredRingDecryptsAfterSourceIsGone, TestRestoreObjectsAfterSourceIsGone passam em descartável; backup de produção, RPO e RTO não medidos (D2-07) |
| 14-carga | M8 | V1 | Parcial, com lacuna concreta | ensaio local existe (loadlocal) e se recusa como capacidade; k6 depende do perfil D2-07 |
| 14-metricas-alertas | M8 | V1 | Implementado e verificado | opsignals/metrics, opsalert; /metrics privado verificado no E2E M7 de 04/11 |
