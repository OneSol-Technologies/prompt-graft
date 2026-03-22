package proxy

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "regexp"
    "strings"
)

// newSessionID generates a short random session id for tracing a client session.
func newSessionID() string {
    b := make([]byte, 12)
    _, _ = rand.Read(b)
    return hex.EncodeToString(b)
}

// GroupID returns a stable 16-char hex hash for a system prompt normalized of dynamic tokens.
func GroupID(systemPrompt string) string {
    normalized := stripDynamic(systemPrompt)
    h := sha256.Sum256([]byte(normalized))
    return hex.EncodeToString(h[:8])
}

var (
    uuidRe     = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
    isoDateRe  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2}Z?)?`)
    emailRe    = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
    urlRe      = regexp.MustCompile(`https?://\S+`)
    digitRunRe = regexp.MustCompile(`\b\d+\b`)
)

// stripDynamic normalizes likely-dynamic substrings so prompts with only variable values map to the same group id.
func stripDynamic(s string) string {
    s = uuidRe.ReplaceAllString(s, "UUID")
    s = isoDateRe.ReplaceAllString(s, "DATE")
    s = emailRe.ReplaceAllString(s, "EMAIL")
    s = urlRe.ReplaceAllString(s, "URL")
    s = digitRunRe.ReplaceAllString(s, "NUM")
    return strings.ToLower(strings.TrimSpace(s))
}
