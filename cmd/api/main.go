package main

import (
    "context"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "promptguru/internal/api"
    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/store"
    redisstore "promptguru/internal/store/redis"
)

func main() {
    cfg := config.Load()
    log := logging.New()

    var st store.Store
    if cfg.RedisURL != "" {
        client, err := redisstore.New(cfg.RedisURL, log)
        if err != nil {
            log.Errorf("redis init failed: %v", err)
        } else {
            st = redisstore.NewStore(client)
        }
    }

    server := api.NewServer(cfg, st, log)

    go func() {
        log.Infof("api listening on %s", cfg.APIAddr)
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Errorf("api server error: %v", err)
        }
    }()

    waitForShutdown(server, log)
}

func waitForShutdown(server *http.Server, log *logging.Logger) {
    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
    <-stop

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = server.Shutdown(ctx)
    log.Infof("api shutdown complete")
}
