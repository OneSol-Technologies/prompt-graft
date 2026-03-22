package api

import (
    "net/http"

    "promptguru/internal/api/routes"
    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/store"
)

func NewServer(cfg *config.Config, st store.Store, log *logging.Logger) *http.Server {
    mux := http.NewServeMux()
    routes.Register(mux, cfg, st, log)
    return &http.Server{
        Addr:    cfg.APIAddr,
        Handler: mux,
    }
}
