# Revisão de segurança operacional do piloto (M8)

Recorte de `M8-revisao-seguranca`. A reexecução é local, com PostgreSQL embutido e sem credencial externa. Passar estes testes não homologa Meta, ERP nem Evolution e não fecha o piloto.

O comando, na raiz do repositório:

```
./scripts/ops-security-check.sh
```

Ele recusa `CHATISP_DATABASE_URL` e remove `CHATISP_TEST_DATABASE_URL` do processo, para não abrir um banco apontado pelo ambiente. Não faz deploy e não executa expurgo.

| Contrato | Testes | O que a prova cobre |
| --- | --- | --- |
| RLS e vazamento entre tenants | `TestTenantIsolationAndCompositeForeignKey`, `TestPoliciesFailClosedWithoutTenantSetting` em `backend/internal/platform/db` | Papel sem contexto não lê a linha de outro provedor; a FK composta recusa o vínculo cruzado |
| CSRF e Origin | `TestPanelRejectsBadOriginCSRFAndRevokedSession` em `backend/internal/platform/httpapi` | Origem ausente ou estranha e sessão revogada não gravam no painel |
| Webhook | `TestWebhook_VerificationAndPersist`, `TestExpansion_MultiTenant` em `backend/internal/attendance/pipeline` | Verificação antes de persistir; envelope com mais de um provedor não escolhe tenant por omissão |
| SSRF de URL de saída | `TestDNSRebindingAndRedirectDoNotConnect`, `TestPrivateGrantDoesNotCrossScope` em `backend/internal/platform/urlguard`; `TestGuardedPosterBlocksPrivateTargets`, `TestAllowDestinationRefusesPrivateOutsideTest` em `backend/internal/outwebhook` | Destino privado, redirecionamento e rebinding não abrem conexão fora do escopo |
| Mídia ativa | `TestTypeAndNameValidation` em `backend/internal/attachments`; `TestNormalizeRejectsActiveContentMisleadingTypeAndLimits` em `backend/internal/branding` | HTML, SVG ativo e nome de arquivo inseguro não viram anexo nem logo aceitos |

O resultado de cada execução fica em `docs/CONTINUACAO-CURSOR.md`. Uma execução antiga não substitui a desta frente.

## Pendências que esta revisão não cobre

- Credencial real da Meta, contrato real de ERP e Evolution habilitada.
- Cache com Ristretto e invalidação de permissão. Não há uso de Ristretto no módulo.
- ffmpeg e o teto de conversão de áudio. A outra frente trata mídia.
- Backup de produção e k6. O ensaio de restore e o ensaio local de carga são outros documentos.
