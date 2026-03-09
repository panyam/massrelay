package middleware

import (
	"net"
	"net/http"
	"strings"
)

// TrustedProxies controls which reverse proxies are trusted to set
// X-Forwarded-For and X-Real-IP headers. Without this, any client can
// spoof their IP to bypass per-IP rate limiting.
//
// Usage:
//
//	// Trust Caddy on localhost and Docker bridge network
//	SetTrustedProxies([]string{"127.0.0.1/32", "172.17.0.0/16", "::1/128"})
//
//	// Trust all proxies (NOT recommended for direct-internet exposure)
//	SetTrustedProxies(nil)  // or don't call at all — this is the default
var trustedProxyCIDRs []*net.IPNet

// SetTrustedProxies configures the CIDR ranges of trusted reverse proxies.
// When set, X-Forwarded-For is only honored if the request comes from a
// trusted proxy. When nil/empty, X-Forwarded-For is always trusted
// (backwards-compatible default, suitable for behind-proxy deployments).
func SetTrustedProxies(cidrs []string) {
	trustedProxyCIDRs = nil
	for _, cidr := range cidrs {
		// Support bare IPs like "127.0.0.1" by appending /32 or /128
		if !strings.Contains(cidr, "/") {
			if strings.Contains(cidr, ":") {
				cidr += "/128"
			} else {
				cidr += "/32"
			}
		}
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			trustedProxyCIDRs = append(trustedProxyCIDRs, network)
		}
	}
}

// ClientIP extracts the real client IP from the request.
//
// If trusted proxies are configured, X-Forwarded-For is only honored when
// the direct connection (RemoteAddr) comes from a trusted proxy CIDR.
// Otherwise, the direct RemoteAddr is used.
//
// If no trusted proxies are configured (default), X-Forwarded-For is
// always trusted (backwards-compatible for deployments that are always
// behind a proxy).
func ClientIP(r *http.Request) string {
	directIP := extractIP(r.RemoteAddr)

	// Check X-Forwarded-For only if we trust the direct connection
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if isTrustedProxy(directIP) {
			// Use the leftmost (client-originated) IP
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}

	// Check X-Real-IP as fallback (single-hop proxies like nginx/Caddy)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if isTrustedProxy(directIP) {
			return strings.TrimSpace(xri)
		}
	}

	return directIP
}

// isTrustedProxy checks if the given IP is from a trusted proxy.
// If no trusted proxies are configured, all are trusted (backwards-compat).
func isTrustedProxy(ip string) bool {
	if len(trustedProxyCIDRs) == 0 {
		return true // no restrictions configured
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range trustedProxyCIDRs {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

// extractIP extracts the IP address from a RemoteAddr string (host:port or [host]:port).
func extractIP(addr string) string {
	// Handle IPv6 [::1]:port
	if strings.HasPrefix(addr, "[") {
		if i := strings.LastIndex(addr, "]"); i >= 0 {
			return addr[1:i]
		}
	}
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}
