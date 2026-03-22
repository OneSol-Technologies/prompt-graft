package proxy

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "net/http"
    "sort"
    "strings"
    "time"

    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/provider"
    "promptguru/internal/store"
    "promptguru/pkg/body"
    "promptguru/pkg/hash"
)

type Handler struct {
    cfg      *config.Config
    store    store.Store
    registry *provider.Registry
    forwarder *Forwarder
    log      *logging.Logger
}

// NewHandler wires the proxy handler with config, store, provider registry, forwarder, and logger.
func NewHandler(cfg *config.Config, st store.Store, reg *provider.Registry, fwd *Forwarder, log *logging.Logger) *Handler {
    return &Handler{cfg: cfg, store: st, registry: reg, forwarder: fwd, log: log}
}

// ServeHTTP handles a single proxy request: buffer/stream body, optionally inject variant prompt, forward upstream, stream response, and log.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), h.cfg.RequestTimeout)
    defer cancel()

    h.log.Debugf("incoming request method=%s path=%s host=%s", r.Method, r.URL.Path, r.Host)
    h.log.Debugf("incoming headers=%s", safeHeaderDump(r.Header))

    upstreamHost := r.Header.Get("X-PG-Upstream-Host")
    lookupHost := upstreamHost
    if lookupHost == "" {
        lookupHost = h.cfg.DefaultUpstreamHost
    }
    if lookupHost == "" {
        lookupHost = r.Host
    }

    adapter := h.registry.Lookup(lookupHost)
    keyHash := hash.APIKey(h.cfg.APIKeySalt, r.Header.Get("Authorization"))
    sessionID := r.Header.Get("X-PG-Session")
    if sessionID == "" {
        sessionID = newSessionID()
    }

    buffered, stream, err := body.StreamOrBuffer(r.Header.Get("Content-Type"), r.Body, h.cfg.MaxBufferBytes)
    if err != nil {
        h.log.Warnf("buffer body failed: %v", err)
    }

    h.log.Debugf("buffered_len=%d streamed=%t", len(buffered), stream != nil)
    if len(buffered) > 0 {
        h.log.Debugf("incoming body=%s", string(buffered))
    }

    var variantID string
    injectedBody := buffered
    var promptSnippet string
    var prompt string

    if len(buffered) > 0 {
        extracted, err := adapter.ExtractSystemPrompt(r.Header.Get("Content-Type"), buffered)
        if err != nil {
            h.log.Warnf("extract prompt failed: %v", err)
        }
        if extracted != "" {
            prompt = extracted
            promptSnippet = snippetString(extracted, 400)
        }
    }

    h.log.Debugf("session_id=%s prompt_snippet=%q", sessionID, promptSnippet)

    varCtx, varCancel := context.WithTimeout(ctx, h.cfg.RedisTimeout)
    if h.store != nil {
        if variant, err := h.store.GetVariant(varCtx, keyHash, sessionID); err == nil && variant != nil {
            h.log.Debugf("variant set found count=%d", len(variant.Variants))
            if len(buffered) > 0 && prompt != "" {
                chosen := pickWeightedRandom(variant.Variants)
                if chosen.ID != "" {
                    template, _ := h.store.GetSessionPrompt(varCtx, keyHash, sessionID)
                    newPrompt := chosen.SystemPrompt
                    if template != "" && strings.Contains(prompt, template) {
                        newPrompt = strings.Replace(prompt, template, chosen.SystemPrompt, 1)
                    }
                    if newBody, err := adapter.InjectSystemPrompt(r.Header.Get("Content-Type"), buffered, newPrompt); err == nil {
                        injectedBody = newBody
                        variantID = chosen.ID
                        prompt = newPrompt
                        promptSnippet = snippetString(newPrompt, 400)
                    } else {
                        h.log.Warnf("inject prompt failed: %v", err)
                    }
                }
            }
        } else if err != nil {
            h.log.Debugf("variant lookup error: %v", err)
        }
    } else {
        h.log.Debugf("store disabled; skipping variant lookup")
    }
    varCancel()

    conversationID := conversationID(prompt)
    h.log.Debugf("variant_id=%s conversation_id=%s", variantID, conversationID)
    if len(injectedBody) > 0 {
        h.log.Debugf("outgoing body=%s", string(injectedBody))
    }

    if h.store != nil {
        go h.store.LogRequest(context.Background(), keyHash, sessionID, variantID, conversationID, r.Header.Get("Content-Type"), promptSnippet, prompt, buffered)
    }

    var reqBody io.Reader = bytes.NewReader(injectedBody)
    if stream != nil {
        reqBody = stream
    }

    h.log.Debugf("forwarding to upstream url=%s host=%s scheme=%s", r.Header.Get("X-PG-Upstream-Url"), upstreamHost, r.Header.Get("X-PG-Upstream-Scheme"))

    resp, err := h.forwarder.Forward(ctx, r, reqBody)
    if err != nil {
        h.log.Errorf("forward error: %v", err)
        http.Error(w, "upstream error", http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()

    h.log.Debugf("upstream response status=%d headers=%s", resp.StatusCode, safeHeaderDump(resp.Header))

    copyHeaders(w.Header(), resp.Header)
    w.Header().Set("X-PG-Session-Id", sessionID)
    w.Header().Set("X-PG-Conversation-Id", conversationID)
    if variantID != "" {
        w.Header().Set("X-PG-Variant-Id", variantID)
    }
    w.WriteHeader(resp.StatusCode)

    var resBuf bytes.Buffer
    n, _ := io.Copy(w, io.TeeReader(resp.Body, &resBuf))
    h.log.Debugf("response bytes sent=%d", n)
    if resBuf.Len() > 0 {
        h.log.Debugf("response body=%s", resBuf.String())
    }

    if h.store != nil {
        go h.store.LogResponse(context.Background(), keyHash, sessionID, variantID, conversationID, resBuf.Bytes())
    }
}

// pickWeightedRandom selects a variant using weights; falls back to the first variant if weights are zero.
func pickWeightedRandom(variants []store.Variant) store.Variant {
    if len(variants) == 0 {
        return store.Variant{}
    }
    total := 0.0
    for _, v := range variants {
        total += v.Weight
    }
    if total <= 0 {
        return variants[0]
    }
    r := time.Now().UnixNano() % int64(total*1000)
    cumulative := 0.0
    for _, v := range variants {
        cumulative += v.Weight * 1000
        if float64(r) <= cumulative {
            return v
        }
    }
    return variants[len(variants)-1]
}

// snippetString returns at most max characters of the input string.
func snippetString(s string, max int) string {
    if len(s) <= max {
        return s
    }
    return s[:max]
}

// conversationID hashes the first 5000 characters of the prompt to identify a conversation.
func conversationID(prompt string) string {
    if prompt == "" {
        return ""
    }
    if len(prompt) > 5000 {
        prompt = prompt[:5000]
    }
    sum := sha256.Sum256([]byte(prompt))
    return hex.EncodeToString(sum[:8])
}

// safeHeaderDump returns a single-line header dump with Authorization redacted.
func safeHeaderDump(h http.Header) string {
    if h == nil {
        return ""
    }
    copy := http.Header{}
    for k, vals := range h {
        if strings.EqualFold(k, "Authorization") {
            copy[k] = []string{"REDACTED"}
            continue
        }
        copy[k] = vals
    }
    keys := make([]string, 0, len(copy))
    for k := range copy {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    var b strings.Builder
    for _, k := range keys {
        b.WriteString(k)
        b.WriteString(": ")
        b.WriteString(strings.Join(copy[k], ","))
        b.WriteString("; ")
    }
    return b.String()
}
