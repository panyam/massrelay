package middleware

import "net/http"

// CORS returns middleware that sets CORS headers based on an OriginChecker.
//
// Behavior:
//   - If checker is nil (no allowlist): Access-Control-Allow-Origin: *
//   - If checker is set and the request Origin matches: reflect the origin back
//   - If checker is set and the request Origin doesn't match: no CORS headers
//     (browser will block the response)
//   - OPTIONS preflight requests are handled and return 204
//
// This replaces the naive "Access-Control-Allow-Origin: *" pattern with
// origin validation. The same OriginChecker used for WebSocket origin checks
// is reused here for consistency.
func CORS(checker *OriginChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" {
				if checker == nil || checker.Check(origin) {
					// Reflect the specific origin (required for credentials, more secure than *)
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
				// If origin doesn't match, we don't set any CORS headers —
				// the browser will block the response.
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
