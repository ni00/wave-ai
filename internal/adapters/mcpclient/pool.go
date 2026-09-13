package mcpclient

import (
	"crypto/sha256"
	"sync"
	"time"
)

type poolKey struct {
	Scope, URL string
	Credential [32]byte
}
type poolEntry struct {
	client *Client
	used   time.Time
}

// Pool is process-local and bounded. Credentials are revalidated by the caller
// before lookup; rotation selects a new client, and tenants never share sessions.
type Pool struct {
	mu      sync.Mutex
	entries map[poolKey]poolEntry
}

func (p *Pool) Get(scope, server, url, bearer string) *Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = map[poolKey]poolEntry{}
	}
	now := time.Now()
	for key, entry := range p.entries {
		if now.Sub(entry.used) > 15*time.Minute {
			entry.client.Close()
			delete(p.entries, key)
		}
	}
	key := poolKey{Scope: scope, URL: url, Credential: sha256.Sum256([]byte(bearer))}
	if entry, ok := p.entries[key]; ok {
		entry.used = now
		p.entries[key] = entry
		return entry.client
	}
	if len(p.entries) >= 128 {
		var oldest poolKey
		var stamp time.Time
		for k, e := range p.entries {
			if stamp.IsZero() || e.used.Before(stamp) {
				oldest, stamp = k, e.used
			}
		}
		p.entries[oldest].client.Close()
		delete(p.entries, oldest)
	}
	client := New(server, url, bearer)
	p.entries[key] = poolEntry{client: client, used: now}
	return client
}
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.entries {
		e.client.Close()
		delete(p.entries, k)
	}
}
