package routes

import (
    "encoding/json"
    "net/http"
    "strings"

    "promptguru/pkg/hash"
)

type feedbackRequest struct {
    Rating int `json:"rating"`
}

type okResponse struct {
    OK bool `json:"ok"`
}

func (h *Handler) handleFeedback(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    sessionID := strings.TrimPrefix(r.URL.Path, "/feedback/")
    if sessionID == "" {
        http.Error(w, "missing session", http.StatusBadRequest)
        return
    }

    var req feedbackRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid body", http.StatusBadRequest)
        return
    }

    keyHash := hash.APIKey(h.cfg.APIKeySalt, r.Header.Get("Authorization"))
    variantID := r.Header.Get("X-PG-Variant")
    conversationID := r.Header.Get("X-PG-Conversation-Id")

    if conversationID == "" {
        info, err := h.store.GetSessionInfo(r.Context(), keyHash, sessionID)
        if err == nil && info != nil {
            conversationID = info.ConversationID
            if variantID == "" {
                variantID = info.VariantID
            }
        }
    }

    if err := h.store.RecordFeedback(r.Context(), keyHash, sessionID, conversationID, variantID, req.Rating); err != nil {
        http.Error(w, "failed", http.StatusInternalServerError)
        return
    }

    writeJSON(w, okResponse{OK: true})
}
