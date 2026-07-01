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
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/feedback/", h.handleFeedback)
	mux.HandleFunc("/sessions/", h.handleSessions)
	mux.HandleFunc("/variants/", h.handleVariants)
	mux.HandleFunc("/", h.handleStatic)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"service": "api", "status": "ok"})
}

func (h *Handler) handleStatic(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "studio-chat.html")
}
