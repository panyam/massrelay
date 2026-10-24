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

func newTestAppWithAdmin(t *testing.T, token string) *RelayApp {
	t.Helper()
	app := NewRelayApp()
	app.AdminToken = token
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

// --- Admin endpoint tests ---

func TestAdminStatus_NoToken(t *testing.T) {
	app := newTestApp(t) // no admin token
	req := httptest.NewRequest("GET", "/admin/status", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	// Admin routes not registered when token is empty
	if w.Code == http.StatusOK {
		t.Fatal("expected admin endpoint to not be registered without token")
	}
}

func TestAdminStatus_Unauthorized(t *testing.T) {
	app := newTestAppWithAdmin(t, "secret-token-123")

	t.Run("no auth header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/admin/status", nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/admin/status", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("no Bearer prefix", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/admin/status", nil)
		req.Header.Set("Authorization", "secret-token-123")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})
}

func TestAdminStatus_Authorized(t *testing.T) {
	app := newTestAppWithAdmin(t, "secret-token-123")
	req := httptest.NewRequest("GET", "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
	if _, ok := body["room_details"]; !ok {
		t.Fatal("expected room_details in response")
	}
	if _, ok := body["uptime_seconds"]; !ok {
		t.Fatal("expected uptime_seconds in response")
	}
}

func TestAdminListRooms_Authorized(t *testing.T) {
	app := newTestAppWithAdmin(t, "secret-token-123")
	req := httptest.NewRequest("GET", "/admin/rooms", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if _, ok := body["rooms"]; !ok {
		t.Fatal("expected rooms key in response")
	}
}

func TestAdminGetRoom_NotFound(t *testing.T) {
	app := newTestAppWithAdmin(t, "secret-token-123")
	req := httptest.NewRequest("GET", "/admin/rooms/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminTimingAttack(t *testing.T) {
	// Verify constant-time comparison is used (functional test, not timing)
	app := newTestAppWithAdmin(t, "correct-token")

	// Short wrong token
	req := httptest.NewRequest("GET", "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer x")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for short token, got %d", w.Code)
	}

	// Long wrong token
	req = httptest.NewRequest("GET", "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for long token, got %d", w.Code)
	}
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

// count429 sends n WebSocket-path requests from one IP at once and counts how
// many the IP rate limiter turned away before the upgrade.
func count429(t *testing.T, app *RelayApp, n int) int {
	t.Helper()
	limited := 0
	for i := 0; i < n; i++ {
		req := httptest.NewRequest("GET", "/ws/v1/test-session/sync", nil)
		req.RemoteAddr = "192.0.2.7:4000"
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	return limited
}

func TestPerIPBurstFollowsRate(t *testing.T) {
	t.Setenv("RELAY_PER_IP_RATE", "6")
	if got := count429(t, newTestApp(t), 6); got != 0 {
		t.Fatalf("6 tabs opening at once at RELAY_PER_IP_RATE=6: %d rejected, want 0", got)
	}
}

func TestPerIPBurstDefaultIsFive(t *testing.T) {
	if got := count429(t, newTestApp(t), 6); got != 1 {
		t.Fatalf("default per-IP burst: %d of 6 rejected, want 1", got)
	}
}

func TestPerIPBurstOverride(t *testing.T) {
	t.Setenv("RELAY_PER_IP_RATE", "1000")
	t.Setenv("RELAY_PER_IP_BURST", "2")
	if got := count429(t, newTestApp(t), 3); got != 1 {
		t.Fatalf("RELAY_PER_IP_BURST=2: %d of 3 rejected, want 1", got)
	}
}
