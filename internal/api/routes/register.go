package routes

import (
    "net/http"

    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/store"
)

type Handler struct {
    cfg *config.Config
    store store.Store
    log *logging.Logger
}

func Register(mux *http.ServeMux, cfg *config.Config, st store.Store, log *logging.Logger) {
    h := &Handler{cfg: cfg, store: st, log: log}
    mux.HandleFunc("/feedback/", h.handleFeedback)
    mux.HandleFunc("/sessions/", h.handleSessions)
    mux.HandleFunc("/variants/", h.handleVariants)
}
