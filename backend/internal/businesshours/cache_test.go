package businesshours

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"chatisp/internal/platform/tenantcache"
	"chatisp/internal/sectors"
)

func TestGetDoesNotTrustCacheWithoutDatabase(t *testing.T) {
	cache, err := tenantcache.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	tenant := uuid.New()
	inbox := uuid.New()
	if err := cache.Set(tenant.String(), tenantcache.KindInbox, inbox.String(), 9, Configuration{Version: 9, Configured: true, Timezone: "America/Sao_Paulo"}); err != nil {
		t.Fatal(err)
	}
	_, err = (Service{Cache: cache}).Get(context.Background(), sectors.Principal{
		TenantID: tenant, UserID: uuid.New(), MembershipID: uuid.New(), Role: "admin",
	}, inbox)
	if err == nil {
		t.Fatal("metadado da caixa saiu do cache sem banco")
	}
}
