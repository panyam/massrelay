package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP_DirectConnection(t *testing.T) {
	// No proxy headers, no trusted proxies configured
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "203.0.113.50:12345"

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %q", got)
	}
}

func TestClientIP_XForwardedFor_NoTrustedProxies(t *testing.T) {
	// Default: trust all (backwards-compat)
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %q", got)
	}
}

func TestClientIP_XForwardedFor_MultipleIPs(t *testing.T) {
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 198.51.100.1, 10.0.0.1")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected leftmost IP 203.0.113.50, got %q", got)
	}
}

func TestClientIP_XForwardedFor_TrustedProxy(t *testing.T) {
	// Only trust localhost
	SetTrustedProxies([]string{"127.0.0.1", "::1"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50 from trusted proxy, got %q", got)
	}
}

func TestClientIP_XForwardedFor_UntrustedProxy(t *testing.T) {
	// Only trust localhost
	SetTrustedProxies([]string{"127.0.0.1"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "198.51.100.99:12345" // NOT a trusted proxy
	req.Header.Set("X-Forwarded-For", "10.0.0.1") // spoofed

	got := ClientIP(req)
	if got != "198.51.100.99" {
		t.Errorf("expected direct IP 198.51.100.99 (untrusted proxy), got %q", got)
	}
}

func TestClientIP_XRealIP_TrustedProxy(t *testing.T) {
	SetTrustedProxies([]string{"10.0.0.0/8"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "203.0.113.50")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50 from X-Real-IP, got %q", got)
	}
}

func TestClientIP_XRealIP_UntrustedProxy(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "198.51.100.99:12345"
	req.Header.Set("X-Real-IP", "10.0.0.1") // spoofed

	got := ClientIP(req)
	if got != "198.51.100.99" {
		t.Errorf("expected direct IP (untrusted proxy), got %q", got)
	}
}

func TestClientIP_XForwardedFor_TakesPrecedenceOverXRealIP(t *testing.T) {
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.Header.Set("X-Real-IP", "198.51.100.1")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected X-Forwarded-For to take precedence, got %q", got)
	}
}

func TestClientIP_IPv6_RemoteAddr(t *testing.T) {
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "[2001:db8::1]:12345"

	got := ClientIP(req)
	if got != "2001:db8::1" {
		t.Errorf("expected 2001:db8::1, got %q", got)
	}
}

func TestClientIP_IPv6_Localhost_TrustedProxy(t *testing.T) {
	SetTrustedProxies([]string{"::1"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "[::1]:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %q", got)
	}
}

func TestClientIP_DockerBridgeNetwork(t *testing.T) {
	// Docker bridge is 172.17.0.0/16
	SetTrustedProxies([]string{"172.17.0.0/16"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "172.17.0.2:12345" // Caddy container
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected client IP from Docker proxy, got %q", got)
	}
}

func TestClientIP_DockerBridge_OutsideRange(t *testing.T) {
	SetTrustedProxies([]string{"172.17.0.0/16"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "172.18.0.5:12345" // different Docker network
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	got := ClientIP(req)
	if got != "172.18.0.5" {
		t.Errorf("expected direct IP (proxy not in trusted range), got %q", got)
	}
}

func TestClientIP_MultipleTrustedCIDRs(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8", "172.17.0.0/16"})

	tests := []struct {
		remoteAddr string
		xff        string
		expected   string
	}{
		{"127.0.0.1:1234", "203.0.113.1", "203.0.113.1"},
		{"10.0.0.5:1234", "203.0.113.2", "203.0.113.2"},
		{"172.17.0.3:1234", "203.0.113.3", "203.0.113.3"},
		{"192.168.1.1:1234", "spoofed", "192.168.1.1"}, // not trusted
	}

	for _, tc := range tests {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = tc.remoteAddr
		req.Header.Set("X-Forwarded-For", tc.xff)

		got := ClientIP(req)
		if got != tc.expected {
			t.Errorf("remoteAddr=%s xff=%s: expected %q, got %q",
				tc.remoteAddr, tc.xff, tc.expected, got)
		}
	}
}

func TestClientIP_WhitespaceInXFF(t *testing.T) {
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "  203.0.113.50 , 198.51.100.1 ")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected trimmed IP, got %q", got)
	}
}

func TestClientIP_EmptyXFF(t *testing.T) {
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "203.0.113.50:12345"
	req.Header.Set("X-Forwarded-For", "")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected direct IP for empty XFF, got %q", got)
	}
}

func TestClientIP_BareIPWithoutPort(t *testing.T) {
	SetTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "203.0.113.50" // unusual but possible

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %q", got)
	}
}

// TestClientIP_RateLimitBypass verifies that a direct client cannot
// spoof their IP via X-Forwarded-For when trusted proxies are configured.
func TestClientIP_RateLimitBypass(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1"})

	// Simulate an attacker connecting directly and sending XFF
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "198.51.100.99:12345" // attacker's real IP
	req.Header.Set("X-Forwarded-For", "1.2.3.4") // attempted spoof

	got := ClientIP(req)
	if got != "198.51.100.99" {
		t.Fatalf("SECURITY: attacker spoofed IP to %q, should be 198.51.100.99", got)
	}
}

// TestSetTrustedProxies_BareIPs verifies that bare IPs (no CIDR suffix)
// are handled correctly.
func TestSetTrustedProxies_BareIPs(t *testing.T) {
	SetTrustedProxies([]string{"10.0.0.1", "::1"})

	// IPv4
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	if got := ClientIP(req); got != "203.0.113.50" {
		t.Errorf("bare IPv4: expected 203.0.113.50, got %q", got)
	}

	// IPv6
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.RemoteAddr = "[::1]:12345"
	req2.Header.Set("X-Forwarded-For", "203.0.113.60")
	if got := ClientIP(req2); got != "203.0.113.60" {
		t.Errorf("bare IPv6: expected 203.0.113.60, got %q", got)
	}
}

// TestSetTrustedProxies_InvalidCIDR verifies invalid CIDRs are silently skipped.
func TestSetTrustedProxies_InvalidCIDR(t *testing.T) {
	SetTrustedProxies([]string{"not-a-cidr", "127.0.0.1"})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	got := ClientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected valid CIDR to still work, got %q", got)
	}
}

// TestClientIP_IntegrationWithRateLimit verifies the full middleware chain:
// trusted proxy → ClientIP → rate limiter uses the correct IP.
func TestClientIP_IntegrationWithRateLimit(t *testing.T) {
	SetTrustedProxies([]string{"10.0.0.1"})

	// Create a rate limiter with very low per-IP limit
	rl := NewRateLimiter(RateLimitConfig{
		PerIPPerSec: 1,
		PerIPBurst:  1,
	})

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request from real client IP (via trusted proxy) — should pass
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.RemoteAddr = "10.0.0.1:12345"
	req1.Header.Set("X-Forwarded-For", "203.0.113.50")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("first request should pass, got %d", rr1.Code)
	}

	// Second request from same real IP — should be rate limited
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.RemoteAddr = "10.0.0.1:12345"
	req2.Header.Set("X-Forwarded-For", "203.0.113.50")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request should be rate limited, got %d", rr2.Code)
	}

	// Third request with DIFFERENT real IP — should pass (different rate bucket)
	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.RemoteAddr = "10.0.0.1:12345"
	req3.Header.Set("X-Forwarded-For", "203.0.113.51")
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Errorf("different IP should pass, got %d", rr3.Code)
	}
}

// TestClientIP_IntegrationSpoofAttempt verifies that a direct attacker
// cannot bypass rate limiting by spoofing XFF headers.
func TestClientIP_IntegrationSpoofAttempt(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1"})

	rl := NewRateLimiter(RateLimitConfig{
		PerIPPerSec: 1,
		PerIPBurst:  1,
	})

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request from attacker (direct, not through proxy)
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.RemoteAddr = "198.51.100.99:12345"
	req1.Header.Set("X-Forwarded-For", "1.1.1.1") // spoofed
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("first request should pass, got %d", rr1.Code)
	}

	// Second request from same attacker, different spoofed IP
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.RemoteAddr = "198.51.100.99:12345"
	req2.Header.Set("X-Forwarded-For", "2.2.2.2") // different spoofed IP
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("SECURITY: attacker bypassed rate limit by spoofing XFF (got %d)", rr2.Code)
	}
}
