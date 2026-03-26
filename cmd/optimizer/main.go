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
	"promptguru/internal/optimizer/janitor"
	layeredstore "promptguru/internal/store/layered"
	pgstore "promptguru/internal/store/pg"
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
	rstore := redisstore.NewStore(client)

	var pgClient *pgstore.Client
	var pgStore *pgstore.Store
	if cfg.DatabaseURL != "" {
		pgClient, err = pgstore.New(context.Background(), cfg.DatabaseURL, log)
		if err != nil {
			log.Warnf("postgres init failed (running without durable store): %v", err)
		} else {
			if migrateErr := pgClient.Migrate(context.Background()); migrateErr != nil {
				log.Warnf("postgres migration failed: %v", migrateErr)
			} else {
				pgStore = pgstore.NewStore(pgClient)
				log.Infof("optimizer: postgres connected")
			}
		}
	}

	st := layeredstore.New(rstore, pgStore, log)
	llm := gepa.NewLLMClient(cfg)
	driver := optimizer.NewDriver(st, cfg, llm, log)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		cancel()
	}()

	// Start the janitor only when Postgres is available.
	if pgStore != nil {
		j := janitor.New(pgStore, rstore, cfg.VariantUnusedTTL, cfg.JanitorInterval, log)
		go j.Run(ctx)
	}

	log.Infof("optimizer running (optimizeEvery=%s)", cfg.OptimizeEvery)
	for {
		driver.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.OptimizeEvery):
		}
	}
}
