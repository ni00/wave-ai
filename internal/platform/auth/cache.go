package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
)

// Cache is a positive-only TTL cache in front of the api_keys lookup so the
// hot /v1 path avoids one database query per request.
//
// Revocation semantics: Invalidate drops an entry in this process immediately;
// other replicas converge within one TTL window. Pick the TTL to bound the
// acceptable revocation delay (the default lives in the service wiring).
type Cache struct {
	db  *gorm.DB
	ttl time.Duration
	// lookup resolves a key hash; swappable in tests, lookupKey in production.
	lookup func(ctx context.Context, db *gorm.DB, hash string) (*Principal, error)
	mu     sync.RWMutex
	m      map[string]cacheEntry
}

type cacheEntry struct {
	p        *Principal
	deadline time.Time
}

// NewCache returns a cache in front of db; ttl <= 0 disables caching and every
// call falls through to the database.
func NewCache(db *gorm.DB, ttl time.Duration) *Cache {
	return &Cache{db: db, ttl: ttl, lookup: lookupKey, m: make(map[string]cacheEntry)}
}

// Authenticate resolves the bearer key, consulting the cache first. The
// returned Principal is shared between callers and must be treated as
// read-only.
func (c *Cache) Authenticate(ctx context.Context, bearer string) (*Principal, error) {
	h := hashKey(bearer)
	if c.ttl > 0 {
		c.mu.RLock()
		e, ok := c.m[h]
		c.mu.RUnlock()
		if ok && time.Now().Before(e.deadline) {
			return e.p, nil
		}
	}
	p, err := c.lookup(ctx, c.db, h)
	if err != nil {
		return nil, err
	}
	if c.ttl > 0 {
		deadline := time.Now().Add(c.ttl)
		c.mu.Lock()
		c.m[h] = cacheEntry{p: p, deadline: deadline}
		c.mu.Unlock()
	}
	return p, nil
}

// Invalidate drops one key (identified by its plaintext form) from the cache.
func (c *Cache) Invalidate(bearer string) {
	h := hashKey(bearer)
	c.mu.Lock()
	delete(c.m, h)
	c.mu.Unlock()
}

// InvalidateAll clears every cached entry.
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	c.m = make(map[string]cacheEntry)
	c.mu.Unlock()
}

func lookupKey(ctx context.Context, db *gorm.DB, hash string) (*Principal, error) {
	var key APIKey
	err := db.WithContext(ctx).Where(clause.Eq{Column: "key_hash", Value: hash}).Take(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apierr.Unauthorized("invalid api key")
	}
	if err != nil {
		return nil, apierr.InternalErr(err, "authenticate failed")
	}
	if key.RevokedAt != nil {
		return nil, apierr.Unauthorized("api key revoked")
	}
	return &Principal{OrgID: key.OrgID, PrincipalID: key.PrincipalID, APIKeyID: key.ID, Scope: key.Scope}, nil
}
