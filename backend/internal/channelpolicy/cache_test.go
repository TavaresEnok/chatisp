package channelpolicy

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"chatisp/internal/platform/tenantcache"
	"chatisp/internal/sectors"
)

func TestListTemplatesDoesNotTrustCacheWithoutDatabase(t *testing.T) {
	cache, err := tenantcache.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	tenant := uuid.New()
	waba := uuid.New()
	if err := cache.Set(tenant.String(), tenantcache.KindTemplate, waba.String(), 0, []Template{{Name: "reinicio"}}); err != nil {
		t.Fatal(err)
	}
	_, err = (Service{Cache: cache}).ListTemplates(context.Background(), sectors.Principal{
		TenantID: tenant, UserID: uuid.New(), MembershipID: uuid.New(), Role: "admin",
	}, uuid.New())
	if err == nil {
		t.Fatal("catálogo saiu do cache sem banco")
	}
}
