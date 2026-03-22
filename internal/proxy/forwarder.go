package proxy

import (
    "context"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"

    "promptguru/internal/logging"
)

type Forwarder struct {
    client        *http.Client
    defaultHost   string
    defaultScheme string
    log           *logging.Logger
}

// NewForwarder builds an upstream forwarder with default host/scheme and logging.
func NewForwarder(timeout time.Duration, defaultHost, defaultScheme string, log *logging.Logger) *Forwarder {
    return &Forwarder{
        client:        &http.Client{Timeout: timeout},
        defaultHost:   defaultHost,
        defaultScheme: defaultScheme,
        log:           log,
    }
}

// Forward sends the incoming request to the upstream endpoint and returns the upstream response.
func (f *Forwarder) Forward(ctx context.Context, r *http.Request, body io.Reader) (*http.Response, error) {
    upstreamURL := strings.TrimSpace(r.Header.Get("X-PG-Upstream-Url"))
    upstreamHost := r.Header.Get("X-PG-Upstream-Host")
    scheme := r.Header.Get("X-PG-Upstream-Scheme")

    if upstreamURL != "" {
        // If a full URL is provided, use it as-is.
        target, err := url.Parse(upstreamURL)
        if err != nil {
            return nil, err
        }
        if target.Scheme == "" {
            target.Scheme = f.defaultScheme
        }
        req, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
        if err != nil {
            return nil, err
        }
        copyHeaders(req.Header, r.Header)
        req.Host = target.Host
        if f.log != nil {
            f.log.Debugf("forwarder request method=%s url=%s headers=%s", r.Method, target.String(), safeHeaderDump(req.Header))
        }
        return f.client.Do(req)
    }

    if upstreamHost == "" {
        upstreamHost = f.defaultHost
    }
    if scheme == "" {
        scheme = f.defaultScheme
    }
    if upstreamHost == "" {
        upstreamHost = r.Host
    }

    target := &url.URL{
        Scheme:   scheme,
        Host:     upstreamHost,
        Path:     r.URL.Path,
        RawQuery: r.URL.RawQuery,
    }

    req, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
    if err != nil {
        return nil, err
    }

    copyHeaders(req.Header, r.Header)
    req.Host = upstreamHost

    if f.log != nil {
        f.log.Debugf("forwarder request method=%s url=%s headers=%s", r.Method, target.String(), safeHeaderDump(req.Header))
    }

    return f.client.Do(req)
}

// copyHeaders copies all headers except Host from src to dst.
func copyHeaders(dst, src http.Header) {
    for k, vals := range src {
        if strings.EqualFold(k, "Host") {
            continue
        }
        dst.Del(k)
        for _, v := range vals {
            dst.Add(k, v)
        }
    }
}
