// Package tenantcache is the process cache for configuration, inbox metadata,
// template catalogs and versioned permission notes.
//
// Messages and conversations are refused. A cached permission is not authority:
// the version has to match a role just read from the database, and a write
// still revalidates. TTL only bounds how long a stale display value can live
// in this process.
package tenantcache

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dgraph-io/ristretto"
)

const (
	// KindConfig is provider configuration, such as the display identity.
	KindConfig = "config"
	// KindInbox is inbox metadata, such as business hours.
	KindInbox = "inbox"
	// KindTemplate is a WABA template catalog. Dispatch does not read it.
	KindTemplate = "template"
	// KindPermission stores a role together with its version.
	KindPermission = "permission"

	defaultTTL    = 30 * time.Second
	permissionTTL = 15 * time.Second
	maxBody       = 64 * 1024
	separator     = "\x1f"
)

// ErrRejected means the key or the value is outside the cache contract.
var ErrRejected = errors.New("cache recusou a entrada")

// Cache is one Ristretto instance. A nil pointer is a miss and a no-op write.
type Cache struct {
	inner *ristretto.Cache
	ttl   time.Duration
}

// New opens an in-process cache. It does not open a network.
func New() (*Cache, error) {
	inner, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e4,
		MaxCost:     1 << 20,
		BufferItems: 64,
	})
	if err != nil {
		return nil, err
	}
	return &Cache{inner: inner, ttl: defaultTTL}, nil
}

// Close releases the cache. It is safe on a nil pointer.
func (c *Cache) Close() {
	if c == nil || c.inner == nil {
		return
	}
	c.inner.Close()
}

// PermissionVersion maps a panel role to the version stored with it.
// An unknown role is not cacheable.
func PermissionVersion(role string) (int64, bool) {
	switch role {
	case "admin":
		return 1, true
	case "supervisor":
		return 2, true
	case "agent":
		return 3, true
	default:
		return 0, false
	}
}

// Set stores value under tenant, kind and id. Permission entries require the
// version returned by PermissionVersion. Other kinds may use version 0.
func (c *Cache) Set(tenant, kind, id string, version int64, value any) error {
	if c == nil || c.inner == nil || value == nil {
		return ErrRejected
	}
	key, err := cacheKey(tenant, kind, id)
	if err != nil {
		return err
	}
	if kind == KindPermission && version <= 0 {
		return ErrRejected
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) == 0 || len(body) > maxBody {
		return ErrRejected
	}
	payload := body
	ttl := c.ttl
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if kind == KindPermission {
		ttl = permissionTTL
		payload, err = json.Marshal(versioned{Version: version, Body: body})
		if err != nil || len(payload) > maxBody {
			return ErrRejected
		}
	}
	if !c.inner.SetWithTTL(key, payload, int64(len(payload)), ttl) {
		return ErrRejected
	}
	c.inner.Wait()
	return nil
}

type versioned struct {
	Version int64           `json:"version"`
	Body    json.RawMessage `json:"body"`
}

// Get copies a cached value into dest. For a permission, version must be the
// one stored with that role; any other version is a miss and drops the entry.
func (c *Cache) Get(tenant, kind, id string, version int64, dest any) bool {
	if c == nil || c.inner == nil || dest == nil {
		return false
	}
	key, err := cacheKey(tenant, kind, id)
	if err != nil {
		return false
	}
	raw, ok := c.inner.Get(key)
	if !ok {
		return false
	}
	body, ok := raw.([]byte)
	if !ok || len(body) == 0 {
		c.inner.Del(key)
		return false
	}
	if kind == KindPermission {
		var stored versioned
		if json.Unmarshal(body, &stored) != nil || stored.Version != version || len(stored.Body) == 0 {
			c.inner.Del(key)
			return false
		}
		body = stored.Body
	}
	if json.Unmarshal(body, dest) != nil {
		c.inner.Del(key)
		return false
	}
	return true
}

// Invalidate drops one entry. A bad key is ignored.
func (c *Cache) Invalidate(tenant, kind, id string) {
	if c == nil || c.inner == nil {
		return
	}
	key, err := cacheKey(tenant, kind, id)
	if err != nil {
		return
	}
	c.inner.Del(key)
}

func cacheKey(tenant, kind, id string) (string, error) {
	if tenant == "" || id == "" || strings.Contains(tenant, separator) || strings.Contains(id, separator) {
		return "", ErrRejected
	}
	switch kind {
	case KindConfig, KindInbox, KindTemplate, KindPermission:
	default:
		return "", ErrRejected
	}
	return tenant + separator + kind + separator + id, nil
}
