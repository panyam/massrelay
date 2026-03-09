package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// RequestLogger logs HTTP requests with method, path, status, duration, and client IP.
// Skips logging for specified paths (e.g. /health for noisy liveness probes).
func RequestLogger(skipPaths ...string) func(http.Handler) http.Handler {
	skip := make(map[string]bool, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			duration := time.Since(start)

			slog.Info("HTTP request", "component", "http",
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration", duration.Round(time.Millisecond).String(), "ip", ClientIP(r))
		})
	}
}
