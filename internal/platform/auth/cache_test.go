package auth

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/apierr"
)

type countingLookup struct {
	calls int
	p     *Principal
	err   error
}

func (l *countingLookup) lookup(ctx context.Context, db *gorm.DB, hash string) (*Principal, error) {
	l.calls++
	return l.p, l.err
}

func newTestCache(t *testing.T, ttl time.Duration) (*Cache, *countingLookup) {
	t.Helper()
	c := NewCache(nil, ttl)
	l := &countingLookup{p: &Principal{OrgID: "org", PrincipalID: "user", APIKeyID: "key", Scope: ScopeAPI}}
	c.lookup = l.lookup
	return c, l
}

func TestCacheHitAvoidsLookup(t *testing.T) {
	c, l := newTestCache(t, time.Minute)
	ctx := context.Background()
	for range 5 {
		if _, err := c.Authenticate(ctx, "wa-test"); err != nil {
			t.Fatal(err)
		}
	}
	if l.calls != 1 {
		t.Fatalf("lookup calls = %d, want 1", l.calls)
	}
}

func TestCacheExpiryRequeries(t *testing.T) {
	c, l := newTestCache(t, 20*time.Millisecond)
	ctx := context.Background()
	if _, err := c.Authenticate(ctx, "wa-test"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := c.Authenticate(ctx, "wa-test"); err != nil {
		t.Fatal(err)
	}
	if l.calls != 2 {
		t.Fatalf("lookup calls = %d, want 2 after expiry", l.calls)
	}
}

func TestCacheDoesNotCacheFailures(t *testing.T) {
	c := NewCache(nil, time.Minute)
	l := &countingLookup{err: apierr.Unauthorized("invalid api key")}
	c.lookup = l.lookup
	ctx := context.Background()
	for range 3 {
		if _, err := c.Authenticate(ctx, "wa-bad"); err == nil {
			t.Fatal("expected error")
		}
	}
	if l.calls != 3 {
		t.Fatalf("lookup calls = %d, want 3 (failures are never cached)", l.calls)
	}
}

func TestCacheInvalidate(t *testing.T) {
	c, l := newTestCache(t, time.Minute)
	ctx := context.Background()
	if _, err := c.Authenticate(ctx, "wa-test"); err != nil {
		t.Fatal(err)
	}
	c.Invalidate("wa-test")
	if _, err := c.Authenticate(ctx, "wa-test"); err != nil {
		t.Fatal(err)
	}
	if l.calls != 2 {
		t.Fatalf("lookup calls = %d, want 2 after invalidate", l.calls)
	}
}

func TestCacheDisabledPassesThrough(t *testing.T) {
	c, l := newTestCache(t, 0)
	ctx := context.Background()
	for range 3 {
		if _, err := c.Authenticate(ctx, "wa-test"); err != nil {
			t.Fatal(err)
		}
	}
	if l.calls != 3 {
		t.Fatalf("lookup calls = %d, want 3 with ttl=0", l.calls)
	}
}
