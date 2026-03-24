package proxy

import (
	"net/http"

	"promptguru/internal/config"
	"promptguru/internal/logging"
	"promptguru/internal/provider"
	"promptguru/internal/provider/generic"
	"promptguru/internal/provider/openaichat"
	"promptguru/internal/provider/replicate"
	"promptguru/internal/store"
)

// NewServer builds the proxy HTTP server with provider registry and forwarder wiring.
func NewServer(cfg *config.Config, st store.Store, log *logging.Logger) *http.Server {
	reg := provider.NewRegistry(generic.New())
	rep := replicate.New()
	for _, host := range rep.Hosts() {
		reg.Register(host, rep)
	}

	styleReg := provider.NewStyleRegistry(generic.New())
	styleReg.Register("replicate", rep)
	styleReg.Register("openai-chat", openaichat.New())
	styleReg.Register("generic", generic.New())

	fwd := NewForwarder(cfg.RequestTimeout, cfg.DefaultUpstreamHost, cfg.DefaultUpstreamScheme, log)
	handler := NewHandler(cfg, st, reg, styleReg, fwd, log)

	return &http.Server{
		Addr:    cfg.ProxyAddr,
		Handler: handler,
	}
}
