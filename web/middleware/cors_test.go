package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS_NoChecker_AllowsAll(t *testing.T) {
	handler := CORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://evil.com" {
		t.Errorf("expected origin reflected, got %q", got)
	}
}

func TestCORS_WithChecker_AllowedOrigin(t *testing.T) {
	checker := NewOriginChecker([]string{"excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://excaliframe.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://excaliframe.com" {
		t.Errorf("expected origin reflected, got %q", got)
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
}

func TestCORS_WithChecker_DisallowedOrigin(t *testing.T) {
	checker := NewOriginChecker([]string{"excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header for disallowed origin, got %q", got)
	}
	// Handler should still be called (CORS is advisory, browser enforces)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestCORS_NoOriginHeader(t *testing.T) {
	checker := NewOriginChecker([]string{"excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	// No Origin header (same-origin or non-browser request)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header without Origin, got %q", got)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestCORS_PreflightAllowed(t *testing.T) {
	checker := NewOriginChecker([]string{"excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for preflight")
	}))

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://excaliframe.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://excaliframe.com" {
		t.Errorf("expected origin reflected in preflight, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Allow-Methods header in preflight")
	}
}

func TestCORS_PreflightDisallowed(t *testing.T) {
	checker := NewOriginChecker([]string{"excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for preflight")
	}))

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header for disallowed preflight, got %q", got)
	}
}

func TestCORS_WildcardOrigin(t *testing.T) {
	checker := NewOriginChecker([]string{"*.excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		origin  string
		allowed bool
	}{
		{"https://app.excaliframe.com", true},
		{"https://staging.excaliframe.com", true},
		{"https://excaliframe.com", true},
		{"https://evil.com", false},
		{"https://notexcaliframe.com", false},
	}

	for _, tc := range tests {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", tc.origin)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		got := rr.Header().Get("Access-Control-Allow-Origin")
		if tc.allowed && got != tc.origin {
			t.Errorf("origin %q: expected reflected, got %q", tc.origin, got)
		}
		if !tc.allowed && got != "" {
			t.Errorf("origin %q: expected empty ACAO, got %q", tc.origin, got)
		}
	}
}

func TestCORS_Localhost(t *testing.T) {
	checker := NewOriginChecker([]string{"localhost"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		origin  string
		allowed bool
	}{
		{"http://localhost:3000", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:3000", true},
		{"https://evil.com", false},
	}

	for _, tc := range tests {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", tc.origin)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		got := rr.Header().Get("Access-Control-Allow-Origin")
		if tc.allowed && got != tc.origin {
			t.Errorf("origin %q: expected reflected, got %q", tc.origin, got)
		}
		if !tc.allowed && got != "" {
			t.Errorf("origin %q: expected empty ACAO, got %q", tc.origin, got)
		}
	}
}

func TestCORS_AllowMethodsAndHeaders(t *testing.T) {
	handler := CORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Allow-Methods header")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected Allow-Headers header")
	}
}

func TestCORS_DownstreamHandlerCalled(t *testing.T) {
	called := false
	checker := NewOriginChecker([]string{"excaliframe.com"})
	handler := CORS(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("response body"))
	}))

	// Even with disallowed origin, the handler runs (CORS is advisory)
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("downstream handler should be called even for disallowed origins")
	}
	if rr.Body.String() != "response body" {
		t.Errorf("expected response body, got %q", rr.Body.String())
	}
}
