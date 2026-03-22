package routes

import (
    "encoding/json"
    "net/http"
)

func writeJSON(w http.ResponseWriter, payload any) {
    w.Header().Set("Content-Type", "application/json")
    enc := json.NewEncoder(w)
    _ = enc.Encode(payload)
}
