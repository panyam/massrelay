package otel

import (
	"log/slog"
	"os"
)

// SetupLogging configures structured logging via log/slog.
// All relay code uses slog.Info/Warn/Error instead of log.Printf.
// Output is JSON to stdout, which Loki ingests via the Docker log driver.
func SetupLogging() {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}
