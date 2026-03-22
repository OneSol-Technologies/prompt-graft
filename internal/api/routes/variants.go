package routes

import (
    "net/http"
    "strings"

    "promptguru/pkg/hash"
)

func (h *Handler) handleVariants(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    sessionID := strings.TrimPrefix(r.URL.Path, "/variants/")
    if sessionID == "" {
        http.Error(w, "missing session", http.StatusBadRequest)
        return
    }
    keyHash := hash.APIKey(h.cfg.APIKeySalt, r.Header.Get("Authorization"))
    info, err := h.store.GetVariantsInfo(r.Context(), keyHash, sessionID)
    if err != nil {
        http.Error(w, "not found", http.StatusNotFound)
        return
    }
    writeJSON(w, info)
}
