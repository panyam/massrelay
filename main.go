package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	relaytelem "github.com/panyam/massrelay/otel"
	"github.com/panyam/massrelay/web/middleware"
	"github.com/panyam/massrelay/web/server"
)

func main() {
	port := flag.Int("port", 8787, "Port to listen on")
	flag.Parse()

	// Structured logging via slog (JSON to stdout, Loki ingests via Docker log driver)
	relaytelem.SetupLogging()

	// Initialize OpenTelemetry (configured via env vars, no-op if unconfigured)
	ctx := context.Background()
	promHandler, otelShutdown := relaytelem.Setup(ctx)
	defer otelShutdown(ctx)

	app := server.NewRelayApp()
	if err := app.Init(); err != nil {
		slog.Error("Failed to initialize relay", "error", err)
		os.Exit(1)
	}

	// Build handler chain
	mux := http.NewServeMux()
	mux.Handle("/", app)
	if promHandler != nil {
		mux.Handle("/metrics", promHandler)
	}

	// Middleware chain: recovery → request logging → mux
	handler := middleware.Recovery(middleware.RequestLogger("/health")(mux))

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,  // time to read request headers (slowloris defense)
		IdleTimeout:       120 * time.Second, // keep-alive idle timeout
		MaxHeaderBytes:    1 << 16,           // 64KB max header size
		// Note: ReadTimeout and WriteTimeout are NOT set because WebSocket
		// connections are long-lived. Setting these would kill active WS sessions.
		// The ReadHeaderTimeout protects the initial handshake; once upgraded,
		// the WS connection manages its own deadlines.
	}

	// Graceful shutdown on SIGTERM/SIGINT
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		slog.Info("Relay server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("Shutting down gracefully")

	// Give active connections 10 seconds to finish
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown error", "error", err)
	}
	slog.Info("Server stopped")
}
