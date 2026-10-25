package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/panyam/oneauth/keys"

	"github.com/panyam/massrelay/web/middleware"
)

// registerHS256 stores an HS256 signing key for clientID in the key store.
func registerHS256(ks *keys.InMemoryKeyStore, clientID, secret string) {
	ks.PutKey(context.Background(), &keys.PutKeyRequest{
		Record: &keys.KeyRecord{
			ClientID:  clientID,
			Key:       []byte(secret),
			Algorithm: "HS256",
		},
	})
}

// testKeyStore sets up an InMemoryKeyStore with a test secret.
func testKeyStore(clientID, secret string) *keys.InMemoryKeyStore {
	ks := keys.NewInMemoryKeyStore()
	registerHS256(ks, clientID, secret)
	return ks
}

// mintToken creates a signed JWT with the given claims and secret.
func mintToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}

// validClaims returns a standard set of valid JWT claims for testing.
func validClaims(clientID string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"sub":          "user-42",
		"client_id":    clientID,
		"type":         "access",
		"scopes":       []string{"read", "write"},
		"max_rooms":    float64(10),
		"max_msg_rate": 50.0,
		"iss":          "test-issuer",
		"iat":          now.Unix(),
		"exp":          now.Add(time.Hour).Unix(),
	}
}

// okHandler is a simple 200 handler for testing.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetRelayClaimsFromContext(r.Context())
	if claims != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"subject":   claims.Subject,
			"client_id": claims.ClientID,
			"scopes":    claims.Scopes,
		})
	} else {
		w.WriteHeader(http.StatusOK)
	}
})

const testClientID = "host-alpha"
const testSecret = "alpha-secret-key-for-testing"

func TestValidToken(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["subject"] != "user-42" {
		t.Errorf("expected subject=user-42, got %v", resp["subject"])
	}
}

func TestExpiredToken(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
	})

	claims := validClaims(testClientID)
	claims["exp"] = time.Now().Add(-time.Hour).Unix() // expired

	tok := mintToken(t, testSecret, claims)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestWrongSigningKey(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
	})

	tok := mintToken(t, "wrong-secret-key", validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMalformedToken(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestWrongIssuer(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "expected-issuer",
	})

	claims := validClaims(testClientID)
	claims["iss"] = "wrong-issuer"

	tok := mintToken(t, testSecret, claims)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestTokenFromHeader(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestTokenFromQueryParam(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws?token="+tok, nil)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHeaderPrecedenceOverQueryParam(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore:        ks,
		Required:        true,
		Issuer:          "test-issuer",
		TokenQueryParam: "token",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws?token=invalid-token", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (header should win), got %d", rr.Code)
	}
}

func TestRequiredNoToken(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestOptionalNoToken(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: false,
	})

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	claims := middleware.GetRelayClaimsFromContext(req.Context())
	if claims != nil {
		t.Error("expected nil claims for unauthenticated request")
	}
}

func TestDenyList(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))

	// Deny the subject
	auth.DenySub("user-42")

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for denied subject, got %d", rr.Code)
	}
}

func TestAllowAfterDeny(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))

	auth.DenySub("user-42")
	auth.AllowSub("user-42")

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 after allow, got %d", rr.Code)
	}
}

func TestNilAuthenticatorPassthrough(t *testing.T) {
	var auth *middleware.RelayAuthenticator // nil

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 passthrough, got %d", rr.Code)
	}
}

func TestClaimsRoundtrip(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)

	if resp["subject"] != "user-42" {
		t.Errorf("subject: want user-42, got %v", resp["subject"])
	}
	if resp["client_id"] != "host-alpha" {
		t.Errorf("client_id: want host-alpha, got %v", resp["client_id"])
	}
}

func TestOnAuthenticatedCallback(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)

	var called bool
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
		OnAuthenticated: func(r *http.Request, claims *middleware.RelayClaims) {
			called = true
		},
	})

	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if !called {
		t.Error("OnAuthenticated callback was not called")
	}
}

func TestOnRejectedCallback(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)

	var called bool
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
		OnRejected: func(r *http.Request, err error) {
			called = true
		},
	})

	// Denied subject triggers OnRejected
	auth.DenySub("user-42")
	tok := mintToken(t, testSecret, validClaims(testClientID))
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	auth.Middleware(okHandler).ServeHTTP(rr, req)

	if !called {
		t.Error("OnRejected callback was not called for denied subject")
	}
}

func TestMultiTenantDifferentKeys(t *testing.T) {
	ks := keys.NewInMemoryKeyStore()
	secretA := "secret-for-host-alpha"
	secretB := "secret-for-host-beta"
	registerHS256(ks, "host-alpha", secretA)
	registerHS256(ks, "host-beta", secretB)

	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{
		KeyStore: ks,
		Required: true,
		Issuer:   "test-issuer",
	})

	// Host alpha token
	claimsA := validClaims("host-alpha")
	tokA := mintToken(t, secretA, claimsA)

	reqA := httptest.NewRequest(http.MethodGet, "/ws", nil)
	reqA.Header.Set("Authorization", "Bearer "+tokA)
	rrA := httptest.NewRecorder()
	auth.Middleware(okHandler).ServeHTTP(rrA, reqA)

	if rrA.Code != http.StatusOK {
		t.Errorf("host-alpha: expected 200, got %d: %s", rrA.Code, rrA.Body.String())
	}

	// Host beta token
	claimsB := validClaims("host-beta")
	claimsB["client_id"] = "host-beta"
	claimsB["sub"] = "user-99"
	tokB := mintToken(t, secretB, claimsB)

	reqB := httptest.NewRequest(http.MethodGet, "/ws", nil)
	reqB.Header.Set("Authorization", "Bearer "+tokB)
	rrB := httptest.NewRecorder()
	auth.Middleware(okHandler).ServeHTTP(rrB, reqB)

	if rrB.Code != http.StatusOK {
		t.Errorf("host-beta: expected 200, got %d: %s", rrB.Code, rrB.Body.String())
	}

	// Cross-key: alpha token signed with beta's secret should fail
	tokCross := mintToken(t, secretB, claimsA) // alpha claims, beta secret
	reqCross := httptest.NewRequest(http.MethodGet, "/ws", nil)
	reqCross.Header.Set("Authorization", "Bearer "+tokCross)
	rrCross := httptest.NewRecorder()
	auth.Middleware(okHandler).ServeHTTP(rrCross, reqCross)

	if rrCross.Code != http.StatusUnauthorized {
		t.Errorf("cross-key: expected 401, got %d", rrCross.Code)
	}
}

func TestRoomsClaim(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{KeyStore: ks, Required: true})

	cases := []struct {
		name  string
		claim any
		want  []string
	}{
		{"absent", nil, nil},
		{"string", "design:abc", []string{"design:abc"}},
		{"array", []string{"design:abc", "review:*"}, []string{"design:abc", "review:*"}},
	}
	for _, c := range cases {
		claims := validClaims(testClientID)
		if c.claim != nil {
			claims["rooms"] = c.claim
		}
		var got *middleware.RelayClaims
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = middleware.GetRelayClaimsFromContext(r.Context())
		})
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.Header.Set("Authorization", "Bearer "+mintToken(t, testSecret, claims))
		auth.Middleware(h).ServeHTTP(httptest.NewRecorder(), req)

		if got == nil {
			t.Fatalf("%s: no claims in context", c.name)
		}
		if !reflect.DeepEqual(got.Rooms, c.want) {
			t.Errorf("%s: Rooms = %#v, want %#v", c.name, got.Rooms, c.want)
		}
	}
}

// exp and iat are standard claims, which oneauth leaves out of the custom
// claims, so RelayClaims reads them from the token, from either place it
// can arrive.
func TestRelayClaimsCarryTokenTimes(t *testing.T) {
	ks := testKeyStore(testClientID, testSecret)
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{KeyStore: ks, Required: true, TokenQueryParam: "token"})
	exp := time.Now().Add(42 * time.Minute).Truncate(time.Second)
	iat := time.Now().Add(-time.Minute).Truncate(time.Second)
	claims := validClaims(testClientID)
	claims["exp"] = exp.Unix()
	claims["iat"] = iat.Unix()
	delete(claims, "iss")
	tok := mintToken(t, testSecret, claims)

	for name, req := range map[string]*http.Request{
		"header": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/ws", nil)
			r.Header.Set("Authorization", "Bearer "+tok)
			return r
		}(),
		"query": httptest.NewRequest(http.MethodGet, "/ws?token="+tok, nil),
	} {
		var got *middleware.RelayClaims
		auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = middleware.GetRelayClaimsFromContext(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), req)
		if got == nil {
			t.Fatalf("%s: no claims", name)
		}
		if !got.ExpiresAt.Equal(exp) || !got.IssuedAt.Equal(iat) {
			t.Errorf("%s: ExpiresAt=%v IssuedAt=%v, want %v and %v", name, got.ExpiresAt, got.IssuedAt, exp, iat)
		}
	}
}
