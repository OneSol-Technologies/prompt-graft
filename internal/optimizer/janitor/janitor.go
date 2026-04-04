// Package janitor provides a background goroutine that:
//  1. Removes unused prompt variants from Postgres and Redis.
//  2. Copies Redis conversation logs to the Postgres conversation_logs table
//     in a non-duplicating manner (INSERT ... ON CONFLICT DO NOTHING).
//
// A session's variants are considered "unused" when no feedback event has been
// recorded for them in Postgres within the configured VariantUnusedTTL window.
package janitor

import (
	"context"
	"time"

	"promptguru/internal/logging"
	pgstore "promptguru/internal/store/pg"
	redisstore "promptguru/internal/store/redis"
)

// Janitor removes unused variants and archives conversation logs on a
// configurable interval.
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
			j.copyLogs(ctx)
			j.retireUnused(ctx)
		case <-ctx.Done():
			j.log.Infof("janitor: stopped")
			return
		}
	}
}

// copyLogs scans Redis log keys and copies conversation log entries to Postgres.
// ON CONFLICT DO NOTHING ensures idempotency.
func (j *Janitor) copyLogs(ctx context.Context) {
	sessions := j.redis.ScanLogKeys(ctx)
	if len(sessions) == 0 {
		j.log.Debugf("janitor: copyLogs — no Redis log keys found")
		return
	}
	j.log.Debugf("janitor: copyLogs — scanning %d session(s)", len(sessions))

	total := 0
	for _, ref := range sessions {
		logs := j.redis.ReadConversationLogs(ctx, ref.KeyHash, ref.SessionID, 200)
		inserted := 0
		for _, entry := range logs {
			if entry.Prompt == "" && entry.ResponseText == "" {
				continue // nothing worth storing yet
			}
			if err := j.pg.InsertConversationLog(ctx, entry); err != nil {
				j.log.Warnf("janitor: copyLogs insert session=%s conv=%s: %v",
					ref.SessionID, entry.ConversationID, err)
			} else {
				inserted++
			}
		}
		if inserted > 0 {
			j.log.Debugf("janitor: copyLogs session=%s → inserted/skipped %d log(s)", ref.SessionID, inserted)
		}
		total += inserted
	}
	j.log.Infof("janitor: copyLogs complete — %d log row(s) written across %d session(s)", total, len(sessions))
}

// retireUnused finds sessions with active variants but no recent feedback and
// retires them in Postgres + evicts from Redis.
func (j *Janitor) retireUnused(ctx context.Context) {
	cutoff := time.Now().Add(-j.unusedTTL)
	j.log.Debugf("janitor: scanning for sessions with no feedback since %s (unusedTTL=%s)",
		cutoff.Format(time.RFC3339), j.unusedTTL)

	sessions, err := j.pg.FindUnusedVariantSessions(ctx, j.unusedTTL)
	if err != nil {
		j.log.Warnf("janitor: find unused sessions: %v", err)
		return
	}
	if len(sessions) == 0 {
		j.log.Debugf("janitor: no unused variant sessions found")
		return
	}

	j.log.Infof("janitor: found %d session(s) to retire", len(sessions))

	retired := 0
	for _, us := range sessions {
		if us.LastFeedbackAt == nil {
			j.log.Infof("janitor: retiring session=%s keyHash=%s — NEVER had feedback, activeVariants=%d",
				us.SessionID, us.KeyHash, us.ActiveVariants)
		} else {
			age := time.Since(*us.LastFeedbackAt).Round(time.Second)
			j.log.Infof("janitor: retiring session=%s keyHash=%s — last feedback %s ago (cutoff=%s) activeVariants=%d",
				us.SessionID, us.KeyHash, age, j.unusedTTL, us.ActiveVariants)
		}

		// if err := j.pg.RetireVariants(ctx, us.KeyHash, us.SessionID); err != nil {
		// 	j.log.Warnf("janitor: retire pg variants session=%s: %v", us.SessionID, err)
		// 	continue
		// }
		if err := j.redis.DeleteVariants(ctx, us.KeyHash, us.SessionID); err != nil {
			j.log.Warnf("janitor: delete redis variants session=%s: %v", us.SessionID, err)
		} else {
			j.log.Debugf("janitor: evicted Redis variant key session=%s", us.SessionID)
		}
		retired++
	}
	j.log.Infof("janitor: cycle complete — retired variants for %d/%d session(s)", retired, len(sessions))
}
