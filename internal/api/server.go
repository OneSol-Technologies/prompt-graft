package api

import (
    "net/http"
    "strings"

    "promptguru/internal/api/routes"
    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/middleware"
    "promptguru/internal/store"
)

func NewServer(cfg *config.Config, st store.Store, log *logging.Logger) *http.Server {
    mux := http.NewServeMux()
    routes.Register(mux, cfg, st, log)
    handler := middleware.CORS(authWrap(mux, cfg))
    return &http.Server{
        Addr:    cfg.APIAddr,
        Handler: handler,
    }
}

func authWrap(next http.Handler, cfg *config.Config) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        path := r.URL.Path

        // Health is always public.
        if path == "/health" {
            next.ServeHTTP(w, r)
            return
        }

        // Static files (/, /studio-chat.html) protected by BasicAuth if configured.
        if path == "/" || path == "/studio-chat.html" {
            if cfg.StudioPassword != "" {
                user, pass, ok := r.BasicAuth()
                if !ok || user != "admin" || pass != cfg.StudioPassword {
                    w.Header().Set("WWW-Authenticate", `Basic realm="Prompt Graft"`)
                    w.WriteHeader(http.StatusUnauthorized)
                    w.Write([]byte("Unauthorized"))
                    return
                }
            }
            next.ServeHTTP(w, r)
            return
        }

        // Everything else protected by Bearer token if configured.
        if cfg.AuthToken != "" && !validBearerToken(r.Header.Get("Authorization"), cfg.AuthToken) {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusUnauthorized)
            w.Write([]byte(`{"error":"unauthorized"}`))
            return
        }

        next.ServeHTTP(w, r)
    })
}

func validBearerToken(header, expected string) bool {
    if !strings.HasPrefix(header, "Bearer ") {
        return false
    }
    return strings.TrimSpace(header[len("Bearer "):]) == expected
}
