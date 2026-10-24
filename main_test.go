package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/panyam/massrelay/web/server"
)

func TestRelayHandlerAcceptsWebSocket(t *testing.T) {
	app := server.NewRelayApp()
	if err := app.Init(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", app)
	srv := httptest.NewServer(relayHandler(mux))
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/v1/s/sync", nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("WebSocket upgrade through the relay's handler chain failed (status %d): %v", status, err)
	}
	conn.Close()
}

// When the relay ends a stream (an eviction, or the client's end_send), it
// closes the socket instead of leaving it to the pong timeout.
func TestRelayClosesSocketWhenStreamEnds(t *testing.T) {
	app := server.NewRelayApp()
	if err := app.Init(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", app)
	srv := httptest.NewServer(relayHandler(mux))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/v1/s/sync", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]string{"type": "end_send"}); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err == nil {
			continue // stream_end, before the close
		}
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Fatalf("socket should close normally after the stream ends, got %v (last frame %s)", err, msg)
		}
		return
	}
}
