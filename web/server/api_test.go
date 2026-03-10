package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestApp(t *testing.T) *RelayApp {
	t.Helper()
	app := NewRelayApp()
	if err := app.Init(); err != nil {
		t.Fatalf("app init error: %v", err)
	}
	return app
}

func TestHealthEndpoint(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", body["status"])
	}
}

func TestListRoomsEndpoint_NotExposed(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest("GET", "/api/v1/rooms", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	// List-rooms route is intentionally not registered (security: prevents session enumeration).
	// Handler method is kept for future authenticated admin use.
	if w.Code == http.StatusOK {
		t.Fatalf("expected list-rooms endpoint to not be exposed, got 200")
	}
}

func TestGetRoomEndpoint_NotFound(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest("GET", "/api/v1/rooms/nonexistent", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	app := newTestApp(t)

	t.Run("preflight with origin", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/health", nil)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204 for OPTIONS preflight, got %d", w.Code)
		}
		// No origin checker → reflects any origin
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Fatalf("expected origin reflected, got %q", got)
		}
		if w.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Fatal("expected CORS Allow-Methods header")
		}
	})

	t.Run("GET with origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Fatalf("expected origin reflected, got %q", got)
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("expected Vary: Origin, got %q", got)
		}
	})

	t.Run("GET without origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		// No Origin header → no CORS headers
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no ACAO without Origin, got %q", got)
		}
	})
}

func TestWSEndpoint_NoUpgrade(t *testing.T) {
	app := newTestApp(t)
	// Regular GET without WebSocket upgrade header
	req := httptest.NewRequest("GET", "/ws/v1/test-session/sync", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	// Should fail since no WebSocket upgrade
	if w.Code == http.StatusOK {
		t.Fatal("expected non-200 for non-WS request to WS endpoint")
	}
}
