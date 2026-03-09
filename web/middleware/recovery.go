package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery returns middleware that recovers from panics in downstream handlers.
// On panic, it logs the error and stack trace, then returns 500 to the client.
// Without this, a single bad request can crash the entire server.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				slog.Error("Panic recovered", "component", "recovery",
					"error", err, "method", r.Method, "path", r.URL.Path,
					"ip", ClientIP(r), "stack", string(stack))
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
