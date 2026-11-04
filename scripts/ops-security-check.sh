#!/bin/bash
# Reexecuta os contratos operacionais já cobertos por teste.
# Não aponta para banco real, não faz deploy e não liga expurgo.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ -n "${CHATISP_DATABASE_URL:-}" ]; then
	echo "revisão recusada: CHATISP_DATABASE_URL está definida" >&2
	exit 2
fi
unset CHATISP_TEST_DATABASE_URL || true

echo "RLS"
go -C backend test -count=1 -timeout 240s -run 'TestTenantIsolationAndCompositeForeignKey|TestPoliciesFailClosedWithoutTenantSetting' ./internal/platform/db/

echo "CSRF"
go -C backend test -count=1 -timeout 240s -run 'TestPanelRejectsBadOriginCSRFAndRevokedSession' ./internal/platform/httpapi/

echo "webhook"
go -C backend test -count=1 -timeout 240s -run 'TestWebhook_VerificationAndPersist|TestExpansion_MultiTenant' ./internal/attendance/pipeline/

echo "SSRF"
go -C backend test -count=1 -timeout 180s -run 'TestDNSRebindingAndRedirectDoNotConnect|TestPrivateGrantDoesNotCrossScope' ./internal/platform/urlguard/
go -C backend test -count=1 -timeout 180s -run 'TestGuardedPosterBlocksPrivateTargets|TestAllowDestinationRefusesPrivateOutsideTest' ./internal/outwebhook/

echo "mídia ativa"
go -C backend test -count=1 -timeout 120s -run 'TestTypeAndNameValidation' ./internal/attachments/
go -C backend test -count=1 -timeout 120s -run 'TestNormalizeRejectsActiveContentMisleadingTypeAndLimits' ./internal/branding/

echo "revisão operacional local concluída; isto não homologa Meta, ERP nem Evolution"
