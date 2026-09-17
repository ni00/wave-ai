// Package egress guards platform-initiated outbound HTTP (MCP tool
// servers, deployment webhooks) against SSRF to internal networks: the
// URLs come from user-supplied configuration, so the platform must never
// be tricked into dialing private, link-local or loopback ranges.
//
// The guard sits at the dial layer: every hostname is resolved and ALL
// resolved addresses must be public before a connection is attempted.
// Redirects are covered as well because they go through the same dialer.
//
// WAVE_EGRESS_ALLOW_PRIVATE=true relaxes the private-range block for
// development deployments (e.g. an MCP server on the LAN); loopback is
// separately controlled per client via allowLoopback.
package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// reservedBlocks are non-global IPv4 ranges that the standard IsPrivate /
// IsLinkLocal helpers do not cover. They are routable inside real clusters
// (CGNAT is common in k8s CNI layouts) or otherwise unusable as targets:
// policy must refuse them exactly like RFC1918. TEST-NET ranges stay allowed
// because they are unrouteable everywhere and unit tests dial them.
var reservedBlocks = func() []*net.IPNet {
	blocks := []string{
		"0.0.0.0/8",      // "this network"; treated as self-address on some stacks
		"100.64.0.0/10",  // carrier-grade NAT; k8s CNI overlays
		"192.0.0.0/24",   // IETF protocol assignments
		"192.88.99.0/24", // deprecated 6to4 relay
		"198.18.0.0/15",  // network benchmarking
		"240.0.0.0/4",    // reserved for future use
	}
	nets := make([]*net.IPNet, 0, len(blocks))
	for _, block := range blocks {
		_, network, err := net.ParseCIDR(block)
		if err != nil {
			panic(fmt.Sprintf("egress: invalid reserved block %q", block))
		}
		nets = append(nets, network)
	}
	return nets
}()

// AllowPrivate reports whether private-range targets are tolerated
// (development deployments). Read once at startup.
var AllowPrivate = os.Getenv("WAVE_EGRESS_ALLOW_PRIVATE") == "true"

// GuardedClient returns an HTTP client whose dialer refuses non-public
// targets. allowLoopback permits 127.0.0.0/8 and ::1 (local test
// receivers); private ranges (RFC1918, ULA, link-local incl. cloud
// metadata IPs) are always refused unless WAVE_EGRESS_ALLOW_PRIVATE=true.
// Proxying stays off: a proxy would resolve and dial the target itself,
// bypassing the guarded dialer entirely.
func GuardedClient(timeout time.Duration, allowLoopback bool) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           guardedDial(allowLoopback),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

func guardedDial(allowLoopback bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("egress: bad address %q", addr)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("egress: resolve %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("egress: %q resolves to nothing", host)
		}
		for _, ip := range ips {
			if !allowed(ip.IP, allowLoopback) {
				return nil, fmt.Errorf("egress: %q resolves to non-public %s; refusing (set WAVE_EGRESS_ALLOW_PRIVATE=true only for development)", host, ip.IP)
			}
		}
		// dial the first resolved address; TLS ServerName comes from the
		// URL host in the transport, so certificate validation is unaffected
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

func allowed(ip net.IP, allowLoopback bool) bool {
	if ip.IsLoopback() {
		return allowLoopback
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if AllowPrivate {
		return true
	}
	// RFC1918 / ULA / link-local (incl. 169.254.169.254 metadata) plus the
	// extra reserved blocks above: anything but globally routable unicast.
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		for _, block := range reservedBlocks {
			if block.Contains(v4) {
				return false
			}
		}
	}
	return true
}
