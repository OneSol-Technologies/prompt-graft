package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"
    "time"

    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/optimizer"
    "promptguru/internal/optimizer/gepa"
    redisstore "promptguru/internal/store/redis"
)

func main() {
    cfg := config.Load()
    log := logging.New()

    client, err := redisstore.New(cfg.RedisURL, log)
    if err != nil {
        log.Errorf("redis init failed: %v", err)
        return
    }
    st := redisstore.NewStore(client)

    llm := gepa.NewLLMClient(cfg)
    driver := optimizer.NewDriver(st, cfg, llm, log)

    ctx, cancel := context.WithCancel(context.Background())
    go func() {
        stop := make(chan os.Signal, 1)
        signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
        <-stop
        cancel()
    }()

    log.Infof("optimizer running")
    for {
        driver.RunOnce(ctx)
        select {
        case <-ctx.Done():
            return
        case <-time.After(cfg.OptimizeEvery):
        }
    }
}
