package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/panyam/oneauth/keys"
	mw "github.com/panyam/servicekit/middleware"

	"github.com/panyam/massrelay/web/middleware"
)

func TestPerSubjectRateLimit_DifferentSubjects(t *testing.T) {
	ks := keys.NewInMemoryKeyStore()
	secret := "test-secret-for-subject-rate"
	ks.RegisterKey("host-a", []byte(secret), "HS256")

	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	subRL := mw.NewRateLimiter(mw.RateLimitConfig{
		PerKeyPerSec:  1,
		PerKeyBurst:   1,
		KeyLimiterTTL: 5 * time.Minute,
	})

	guard := middleware.NewGuard(nil, nil, auth, subRL, nil)

	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// User A — first request should pass
	tokA := mintTestToken(t, secret, "user-A", "host-a")
	reqA1 := httptest.NewRequest("GET", "/ws", nil)
	reqA1.Header.Set("Authorization", "Bearer "+tokA)
	rrA1 := httptest.NewRecorder()
	handler.ServeHTTP(rrA1, reqA1)
	if rrA1.Code != http.StatusOK {
		t.Errorf("user-A first request: expected 200, got %d: %s", rrA1.Code, rrA1.Body.String())
	}

	// User A — second request should be rate limited
	reqA2 := httptest.NewRequest("GET", "/ws", nil)
	reqA2.Header.Set("Authorization", "Bearer "+tokA)
	rrA2 := httptest.NewRecorder()
	handler.ServeHTTP(rrA2, reqA2)
	if rrA2.Code != http.StatusTooManyRequests {
		t.Errorf("user-A second request: expected 429, got %d", rrA2.Code)
	}

	// User B — should get independent rate bucket, so first request passes
	tokB := mintTestToken(t, secret, "user-B", "host-a")
	reqB1 := httptest.NewRequest("GET", "/ws", nil)
	reqB1.Header.Set("Authorization", "Bearer "+tokB)
	rrB1 := httptest.NewRecorder()
	handler.ServeHTTP(rrB1, reqB1)
	if rrB1.Code != http.StatusOK {
		t.Errorf("user-B first request: expected 200, got %d: %s", rrB1.Code, rrB1.Body.String())
	}
}

func TestPerSubjectRateLimit_SameSubject(t *testing.T) {
	ks := keys.NewInMemoryKeyStore()
	secret := "test-secret-same-sub"
	ks.RegisterKey("host-a", []byte(secret), "HS256")

	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	subRL := mw.NewRateLimiter(mw.RateLimitConfig{
		PerKeyPerSec:  1,
		PerKeyBurst:   1,
		KeyLimiterTTL: 5 * time.Minute,
	})

	guard := middleware.NewGuard(nil, nil, auth, subRL, nil)

	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tok := mintTestToken(t, secret, "user-42", "host-a")

	// First request from IP A — should pass
	req1 := httptest.NewRequest("GET", "/ws", nil)
	req1.RemoteAddr = "1.1.1.1:1234"
	req1.Header.Set("Authorization", "Bearer "+tok)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d: %s", rr1.Code, rr1.Body.String())
	}

	// Second request from DIFFERENT IP but same subject — should be rate limited
	req2 := httptest.NewRequest("GET", "/ws", nil)
	req2.RemoteAddr = "2.2.2.2:5678"
	req2.Header.Set("Authorization", "Bearer "+tok)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("same subject different IP: expected 429, got %d", rr2.Code)
	}
}

func TestPerSubjectRateLimit_UnauthenticatedPassthrough(t *testing.T) {
	ks := keys.NewInMemoryKeyStore()
	ks.RegisterKey("host-a", []byte("secret"), "HS256")

	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: false, // optional auth
	})

	subRL := mw.NewRateLimiter(mw.RateLimitConfig{
		PerKeyPerSec:  1,
		PerKeyBurst:   1,
		KeyLimiterTTL: 5 * time.Minute,
	})

	guard := middleware.NewGuard(nil, nil, auth, subRL, nil)

	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Unauthenticated request — no subject, key is empty string
	// Both requests share the empty-key bucket, so second gets limited
	req1 := httptest.NewRequest("GET", "/ws", nil)
	req1.RemoteAddr = "1.1.1.1:1234"
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("first unauth request: expected 200, got %d", rr1.Code)
	}

	req2 := httptest.NewRequest("GET", "/ws", nil)
	req2.RemoteAddr = "2.2.2.2:5678"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	// Unauthenticated requests share the empty-key bucket
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second unauth request sharing empty key: expected 429, got %d", rr2.Code)
	}
}

func TestPerSubjectRateLimit_IntegrationWithGuard(t *testing.T) {
	ks := keys.NewInMemoryKeyStore()
	secret := "integration-test-secret"
	ks.RegisterKey("host-a", []byte(secret), "HS256")

	// Origin checker that allows our test origin
	originChecker := mw.NewOriginChecker([]string{"excaliframe.com"})

	// IP rate limiter (very high so it doesn't interfere)
	ipRL := mw.NewRateLimiter(mw.RateLimitConfig{
		PerKeyPerSec:  1000,
		PerKeyBurst:   1000,
		KeyLimiterTTL: 5 * time.Minute,
	})

	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	// Subject rate limiter (tight)
	subRL := mw.NewRateLimiter(mw.RateLimitConfig{
		PerKeyPerSec:  1,
		PerKeyBurst:   1,
		KeyLimiterTTL: 5 * time.Minute,
	})

	connLimiter := mw.NewConnLimiter(100)

	guard := middleware.NewGuard(originChecker, ipRL, auth, subRL, connLimiter)

	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tok := mintTestToken(t, secret, "user-99", "host-a")

	// First request through full chain — should pass
	req1 := httptest.NewRequest("GET", "/ws", nil)
	req1.Header.Set("Upgrade", "websocket")
	req1.Header.Set("Origin", "https://excaliframe.com")
	req1.Header.Set("Authorization", "Bearer "+tok)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d: %s", rr1.Code, rr1.Body.String())
	}

	// Second request — subject rate limited
	req2 := httptest.NewRequest("GET", "/ws", nil)
	req2.Header.Set("Upgrade", "websocket")
	req2.Header.Set("Origin", "https://excaliframe.com")
	req2.Header.Set("Authorization", "Bearer "+tok)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rr2.Code)
	}

	// Bad origin — blocked before auth
	req3 := httptest.NewRequest("GET", "/ws", nil)
	req3.Header.Set("Upgrade", "websocket")
	req3.Header.Set("Origin", "https://evil.com")
	req3.Header.Set("Authorization", "Bearer "+tok)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusForbidden {
		t.Errorf("bad origin: expected 403, got %d", rr3.Code)
	}
}

// mintTestToken creates a signed JWT for testing.
func mintTestToken(t *testing.T, secret, subject, clientID string) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       subject,
		"client_id": clientID,
		"type":      "access",
		"iss":       "test-issuer",
		"iat":       now.Unix(),
		"exp":       now.Add(time.Hour).Unix(),
	})
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}
