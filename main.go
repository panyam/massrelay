package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"

	relaytelem "github.com/panyam/massrelay/otel"
	"github.com/panyam/massrelay/web/server"
)

func main() {
	port := flag.Int("port", 8787, "Port to listen on")
	flag.Parse()

	// Initialize OpenTelemetry (configured via env vars, no-op if unconfigured)
	ctx := context.Background()
	promHandler, otelShutdown := relaytelem.Setup(ctx)
	defer otelShutdown(ctx)

	app := server.NewRelayApp()
	if err := app.Init(); err != nil {
		log.Fatalf("Failed to initialize relay: %v", err)
	}

	// If Prometheus is enabled, serve /metrics on the same port
	mux := http.NewServeMux()
	mux.Handle("/", app)
	if promHandler != nil {
		mux.Handle("/metrics", promHandler)
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Relay server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
