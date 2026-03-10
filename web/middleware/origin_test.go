package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginChecker_Check(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		origin  string
		want    bool
	}{
		{"nil checker allows all", nil, "https://evil.com", true},
		{"empty list allows all", []string{}, "https://evil.com", true},
		{"exact match", []string{"excaliframe.com"}, "https://excaliframe.com", true},
		{"exact match with scheme", []string{"https://excaliframe.com"}, "https://excaliframe.com", true},
		{"rejects non-match", []string{"excaliframe.com"}, "https://evil.com", false},
		{"wildcard subdomain", []string{"*.excaliframe.com"}, "https://app.excaliframe.com", true},
		{"wildcard matches apex", []string{"*.excaliframe.com"}, "https://excaliframe.com", true},
		{"wildcard rejects other", []string{"*.excaliframe.com"}, "https://evil.com", false},
		{"localhost matches 127.0.0.1", []string{"localhost"}, "http://127.0.0.1:3000", true},
		{"localhost matches localhost:port", []string{"localhost"}, "http://localhost:8080", true},
		{"localhost rejects remote", []string{"localhost"}, "https://excaliframe.com", false},
		{"multiple origins", []string{"excaliframe.com", "localhost"}, "http://localhost:5173", true},
		{"empty origin rejected", []string{"excaliframe.com"}, "", false},
		{"port ignored", []string{"excaliframe.com"}, "https://excaliframe.com:443", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewOriginChecker(tt.allowed)
			got := c.Check(tt.origin)
			if got != tt.want {
				t.Errorf("Check(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestOriginChecker_Middleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("blocks disallowed WebSocket upgrade", func(t *testing.T) {
		c := NewOriginChecker([]string{"excaliframe.com"})
		h := c.Middleware(ok)

		r := httptest.NewRequest("GET", "/ws/v1/test/sync", nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Origin", "https://evil.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("got %d, want 403", w.Code)
		}
	})

	t.Run("allows matching origin", func(t *testing.T) {
		c := NewOriginChecker([]string{"excaliframe.com"})
		h := c.Middleware(ok)

		r := httptest.NewRequest("GET", "/ws/v1/test/sync", nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Origin", "https://excaliframe.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("got %d, want 200", w.Code)
		}
	})

	t.Run("non-WebSocket requests pass through", func(t *testing.T) {
		c := NewOriginChecker([]string{"excaliframe.com"})
		h := c.Middleware(ok)

		r := httptest.NewRequest("GET", "/health", nil)
		r.Header.Set("Origin", "https://evil.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("got %d, want 200 (non-WS should pass)", w.Code)
		}
	})
}
