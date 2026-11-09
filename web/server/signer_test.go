package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/panyam/massrelay/web/middleware"
)

// signerRelay runs a relay whose auth comes from signer, the way an embedding
// app builds one, and returns its ws:// base.
func signerRelay(t *testing.T, signer *middleware.RelaySigner) string {
	t.Helper()
	app := NewRelayApp()
	app.Guard = middleware.NewGuard(nil, nil, signer.Authenticator(middleware.RelayAuthConfig{
		Required: true, TokenQueryParam: "token",
	}), nil, nil)
	if err := app.Init(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialSigned(t *testing.T, base, session, token string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(base+"/ws/v1/"+session+"/sync?token="+token, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial (status %d): %v", status, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// sendAction writes action, a CollabAction in protojson, in servicekit's
// data envelope.
func sendAction(t *testing.T, conn *websocket.Conn, action string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "data", "data": json.RawMessage(action)}); err != nil {
		t.Fatal(err)
	}
}

// readEvent returns the next CollabEvent's protojson, skipping envelope frames
// that carry no data.
func readEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var env struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		if env.Type == "data" {
			return env.Data
		}
	}
}

func TestSignerTokenOverWebSocketRespectsRooms(t *testing.T) {
	signer, err := middleware.NewRelaySigner(middleware.RelaySignerConfig{ClientID: "app", Issuer: "app-issuer"})
	if err != nil {
		t.Fatal(err)
	}
	base := signerRelay(t, signer)
	tok, _, err := signer.Mint("user-1", []string{"room-a"})
	if err != nil {
		t.Fatal(err)
	}

	other := dialSigned(t, base, "room-b", tok)
	sendAction(t, other, `{"join":{"sessionId":"room-b"}}`)
	if evt := readEvent(t, other); evt["error"] == nil || evt["error"].(map[string]any)["code"] != "ROOM_FORBIDDEN" {
		t.Fatalf("join outside the rooms claim = %v, want ROOM_FORBIDDEN", evt)
	}

	own := dialSigned(t, base, "room-a", tok)
	sendAction(t, own, `{"join":{"sessionId":"room-a"}}`)
	if evt := readEvent(t, own); evt["roomJoined"] == nil {
		t.Fatalf("join inside the rooms claim = %v, want roomJoined", evt)
	}
}

func TestSignerTokenRefreshesThroughReAuth(t *testing.T) {
	signer, err := middleware.NewRelaySigner(middleware.RelaySignerConfig{ClientID: "app", TTL: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	base := signerRelay(t, signer)
	first, firstExp, err := signer.Mint("user-1", []string{"room-a"})
	if err != nil {
		t.Fatal(err)
	}

	conn := dialSigned(t, base, "room-a", first)
	sendAction(t, conn, `{"join":{"sessionId":"room-a","protocolVersion":3}}`)
	joined, _ := readEvent(t, conn)["roomJoined"].(map[string]any)
	if got, _ := time.Parse(time.RFC3339, joined["tokenExpiresAt"].(string)); !got.Equal(firstExp) {
		t.Fatalf("roomJoined.tokenExpiresAt = %v, want Mint's %v", joined["tokenExpiresAt"], firstExp)
	}

	time.Sleep(1100 * time.Millisecond) // so the fresh token's exp differs by at least a second
	fresh, freshExp, err := signer.Mint("user-1", []string{"room-a"})
	if err != nil {
		t.Fatal(err)
	}
	sendAction(t, conn, `{"reAuth":{"token":"`+fresh+`"}}`)
	result, _ := readEvent(t, conn)["reAuthResult"].(map[string]any)
	if result["success"] != true {
		t.Fatalf("reAuthResult = %v, want success", result)
	}
	if got, _ := time.Parse(time.RFC3339, result["expiresAt"].(string)); !got.Equal(freshExp) || !freshExp.After(firstExp) {
		t.Fatalf("reAuthResult.expiresAt = %v, want the fresh token's %v (first was %v)", result["expiresAt"], freshExp, firstExp)
	}
}
