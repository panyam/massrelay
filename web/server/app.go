package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	relaytelem "github.com/panyam/massrelay/otel"
	"github.com/panyam/massrelay/services"
	"github.com/panyam/massrelay/web/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// RelayApp is the HTTP application for the relay server.
// It implements http.Handler so it can be used standalone or embedded
// as a sub-handler in another server's mux:
//
//	// Standalone
//	http.ListenAndServe(":8787", relayApp)
//
//	// Embedded in another mux
//	mux.Handle("/relay/", http.StripPrefix("/relay", relayApp))
type RelayApp struct {
	Service *services.CollabService
	Metrics *relaytelem.Metrics
	Guard   *middleware.Guard
	mux     *http.ServeMux
}

// NewRelayApp creates a new RelayApp.
//
// Environment variables:
//
//	RELAY_LOG_PAYLOADS=N        — log first N chars of content payloads for debugging
//	RELAY_ALLOWED_ORIGINS=...   — comma-separated origin allowlist for WebSocket connections
//	                              (e.g. "excaliframe.com,*.excaliframe.com,localhost")
//	                              Empty = allow all origins.
//	RELAY_MAX_CONNECTIONS=N     — max concurrent WebSocket connections (0 = unlimited, default 500)
//	RELAY_GLOBAL_RATE=N         — max WebSocket connections/sec globally (default 100)
//	RELAY_PER_IP_RATE=N         — max WebSocket connections/sec per IP (default 5)
func NewRelayApp() *RelayApp {
	svc := services.NewCollabService()

	// Payload logging
	if v := os.Getenv("RELAY_LOG_PAYLOADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			svc.LogPayloads = n
		}
	}

	// Origin allowlist
	var originChecker *middleware.OriginChecker
	if v := os.Getenv("RELAY_ALLOWED_ORIGINS"); v != "" {
		origins := strings.Split(v, ",")
		originChecker = middleware.NewOriginChecker(origins)
		slog.Info("Origin allowlist configured", "origins", origins)
	}

	// Max concurrent connections
	var maxConns int64 = 500
	if v := os.Getenv("RELAY_MAX_CONNECTIONS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			maxConns = n
		}
	}
	connLimiter := middleware.NewConnLimiter(maxConns)
	if connLimiter != nil {
		slog.Info("Max concurrent connections", "limit", maxConns)
	}

	// Rate limiting
	rlCfg := middleware.DefaultRateLimitConfig()
	if v := os.Getenv("RELAY_GLOBAL_RATE"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			rlCfg.GlobalPerSec = n
		}
	}
	if v := os.Getenv("RELAY_PER_IP_RATE"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			rlCfg.PerIPPerSec = n
		}
	}
	rateLimiter := middleware.NewRateLimiter(rlCfg)

	// Build Guard
	guard := &middleware.Guard{
		Origin:    originChecker,
		Conn:      connLimiter,
		RateLimit: rateLimiter,
	}

	metrics := relaytelem.NewMetrics(nil) // nil = use global provider

	// Wire rate limit rejections to metrics
	if rateLimiter != nil {
		rateLimiter.OnRejected = func() {
			metrics.RateLimited.Add(context.Background(), 1)
		}
	}

	app := &RelayApp{
		Service: svc,
		Metrics: metrics,
		Guard:   guard,
		mux:     http.NewServeMux(),
	}

	// Wire service callbacks to OTEL metrics
	ctx := context.Background()
	svc.OnRoomCreated = func() { metrics.RoomsActive.Add(ctx, 1) }
	svc.OnRoomRemoved = func() { metrics.RoomsActive.Add(ctx, -1) }
	svc.OnPeerJoined = func() {
		metrics.PeersActive.Add(ctx, 1)
		metrics.JoinsTotal.Add(ctx, 1)
	}
	svc.OnPeerLeft = func() {
		metrics.PeersActive.Add(ctx, -1)
		metrics.LeavesTotal.Add(ctx, 1)
	}
	svc.OnMessageRelay = func(actionType string) {
		metrics.MessagesTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("type", actionType)))
	}

	return app
}

// Init sets up routes.
func (a *RelayApp) Init() error {
	h := NewApiHandler(a)
	h.SetupRoutes(a.mux)
	return nil
}

// ServeHTTP implements http.Handler.
func (a *RelayApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	a.mux.ServeHTTP(w, r)
}
