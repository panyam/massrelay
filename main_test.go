package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
