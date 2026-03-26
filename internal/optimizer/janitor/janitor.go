// Package janitor provides a background goroutine that removes unused prompt
// variants from both Postgres and Redis.
//
// A session's variants are considered "unused" when no feedback event has been
// recorded for them in Postgres within the configured VariantUnusedTTL window.
// This prevents stale variants from accumulating indefinitely while ensuring
// that active sessions are never touched.
package janitor

import (
	"context"
	"time"

	"promptguru/internal/logging"
	pgstore "promptguru/internal/store/pg"
	redisstore "promptguru/internal/store/redis"
)

// Janitor removes unused variants on a configurable interval.
type Janitor struct {
	pg        *pgstore.Store
	redis     *redisstore.Store
	unusedTTL time.Duration
	interval  time.Duration
	log       *logging.Logger
}

// New returns a Janitor.
//   - unusedTTL: retire variants for sessions with no feedback in this window.
//   - interval:  how often to run the cleanup scan.
func New(pg *pgstore.Store, redis *redisstore.Store, unusedTTL, interval time.Duration, log *logging.Logger) *Janitor {
	return &Janitor{
		pg:        pg,
		redis:     redis,
		unusedTTL: unusedTTL,
		interval:  interval,
		log:       log,
	}
}

// Run starts the janitor loop, blocking until ctx is cancelled.
func (j *Janitor) Run(ctx context.Context) {
	j.log.Infof("janitor: started (unusedTTL=%s interval=%s)", j.unusedTTL, j.interval)
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			j.runOnce(ctx)
		case <-ctx.Done():
			j.log.Infof("janitor: stopped")
			return
		}
	}
}

func (j *Janitor) runOnce(ctx context.Context) {
	sessions, err := j.pg.FindUnusedVariantSessions(ctx, j.unusedTTL)
	if err != nil {
		j.log.Warnf("janitor: find unused sessions: %v", err)
		return
	}
	if len(sessions) == 0 {
		j.log.Debugf("janitor: no unused variant sessions found")
		return
	}

	retired := 0
	for _, ref := range sessions {
		// Mark retired in Postgres.
		if err := j.pg.RetireVariants(ctx, ref.KeyHash, ref.SessionID); err != nil {
			j.log.Warnf("janitor: retire pg variants session=%s: %v", ref.SessionID, err)
			continue
		}
		// Evict from Redis cache so the next request does not serve stale data.
		if err := j.redis.DeleteVariants(ctx, ref.KeyHash, ref.SessionID); err != nil {
			j.log.Warnf("janitor: delete redis variants session=%s: %v", ref.SessionID, err)
		}
		retired++
	}
	j.log.Infof("janitor: retired variants for %d session(s)", retired)
}
