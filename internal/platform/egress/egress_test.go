package egress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Loopback must be dialable when allowed (local webhook receivers) and
// refused when not (MCP URLs from untrusted configuration).
func TestGuardedClientLoopbackPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	allow := GuardedClient(5*time.Second, true)
	resp, err := allow.Get(srv.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("allowLoopback client must reach loopback: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	deny := GuardedClient(5*time.Second, false)
	_, err = deny.Get(srv.URL)
	if err == nil {
		t.Fatal("loopback must be refused when allowLoopback=false")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

// A public URL whose hostname is fine must still work through the guard
// (cannot dial out in unit tests; assert the resolution path succeeds or
// fails for network reasons, never with a policy refusal).
func TestGuardedClientPolicyOnlyBlocksPrivate(t *testing.T) {
	c := GuardedClient(2*time.Second, false)
	_, err := c.Get("http://192.0.2.1:1/") // TEST-NET-1 literal, not private
	if err != nil && strings.Contains(err.Error(), "refusing") {
		t.Errorf("TEST-NET literal must not be policy-blocked: %v", err)
	}
	_, err = c.Get("http://10.0.0.1:1/")
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("RFC1918 literal must be policy-blocked, got: %v", err)
	}
	_, err = c.Get("http://169.254.169.254/latest/meta-data/")
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("cloud metadata IP must be policy-blocked, got: %v", err)
	}
	for _, target := range []string{
		"http://100.64.0.1:1/",  // CGNAT / k8s CNI
		"http://198.18.0.5:1/",  // benchmarking range
		"http://240.1.2.3:1/",   // reserved for future use
		"http://0.0.0.5:1/",     // "this network"
		"http://192.88.99.1:1/", // deprecated 6to4 relay
	} {
		if _, err = c.Get(target); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("reserved range %s must be policy-blocked, got: %v", target, err)
		}
	}
}
