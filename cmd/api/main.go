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
	layeredstore "promptguru/internal/store/layered"
	pgstore "promptguru/internal/store/pg"
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
			rstore := redisstore.NewStore(client)

			var pgStore *pgstore.Store
			if cfg.DatabaseURL != "" {
				pgClient, pgErr := pgstore.New(context.Background(), cfg.DatabaseURL, log)
				if pgErr != nil {
					log.Warnf("postgres init failed (running without durable store): %v", pgErr)
				} else {
					if migrateErr := pgClient.Migrate(context.Background()); migrateErr != nil {
						log.Warnf("postgres migration failed: %v", migrateErr)
					}
					pgStore = pgstore.NewStore(pgClient)
					log.Infof("api: postgres connected")
				}
			}

			st = layeredstore.New(rstore, pgStore, log)
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
