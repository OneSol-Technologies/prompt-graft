package middleware

import "net/http"

// CORS wraps a handler and adds permissive CORS headers suitable for local
// development and self-hosted studio use.  For production, restrict
// AllowedOrigins to your actual frontend domain.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers",
			"Authorization, Content-Type, Prefer, "+
				"X-PG-Session, X-PG-Upstream-Url, X-PG-Upstream-Host, X-PG-Upstream-Scheme, "+
				"X-PG-Api-Style, X-PG-Conversation-Id, X-PG-Variant-Id")
		w.Header().Set("Access-Control-Expose-Headers",
			"X-PG-Session-Id, X-PG-Conversation-Id, X-PG-Variant-Id")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
