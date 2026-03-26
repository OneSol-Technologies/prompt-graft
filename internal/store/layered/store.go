// Package layered implements a two-tier store that keeps Redis as the hot
// cache and Postgres as the durable backing store.
//
// Read strategy:  Redis first → Postgres fallback → re-warm Redis on hit.
// Write strategy: Redis (authoritative for hot path) + Postgres (durable).
//                 Postgres write failures are logged but never returned to the
//                 caller so that Redis unavailability or Postgres downtime never
//                 blocks the proxy or optimizer.
//
// Methods that are purely ephemeral (logs, session-feedback scan, dataset
// loading) are delegated to Redis only.
package layered

import (
	"context"
	"time"

	"promptguru/internal/logging"
	"promptguru/internal/optimizer/gepa"
	"promptguru/internal/store"
	pgstore "promptguru/internal/store/pg"
)

// Store composes a Redis store.Store and a *pgstore.Store.
type Store struct {
	redis store.Store
	pg    *pgstore.Store
	log   *logging.Logger
}

// New returns a layered Store.  pg may be nil; in that case all Postgres
// operations are silently skipped and the layer degrades to Redis-only.
func New(redis store.Store, pg *pgstore.Store, log *logging.Logger) *Store {
	return &Store{redis: redis, pg: pg, log: log}
}

// ---------------------------------------------------------------------------
// Variants — read: Redis first, Postgres fallback; write: both.
// ---------------------------------------------------------------------------

func (s *Store) GetVariant(ctx context.Context, keyHash, sessionID string) (*store.VariantSet, error) {
	vs, err := s.redis.GetVariant(ctx, keyHash, sessionID)
	if err == nil && vs != nil {
		return vs, nil
	}

	if s.pg == nil {
		return nil, err
	}

	// Cache miss — try Postgres.
	vs, pgErr := s.pg.GetActiveVariants(ctx, keyHash, sessionID)
	if pgErr != nil {
		s.log.Warnf("layered: pg GetActiveVariants: %v", pgErr)
		return nil, err // return original Redis error
	}
	if vs == nil {
		return nil, err
	}

	// Re-warm Redis for the remaining active duration (min 1 minute).
	ttl := time.Until(time.Unix(vs.ActiveUntil, 0))
	if ttl < time.Minute {
		ttl = time.Minute
	}
	if rewarmErr := s.redis.SetVariants(ctx, keyHash, sessionID, vs.Variants, ttl); rewarmErr != nil {
		s.log.Warnf("layered: redis rewarm SetVariants: %v", rewarmErr)
	}
	return vs, nil
}

func (s *Store) SetVariants(ctx context.Context, keyHash, sessionID string, variants []store.Variant, ttl time.Duration) error {
	err := s.redis.SetVariants(ctx, keyHash, sessionID, variants, ttl)

	if s.pg != nil {
		activeUntil := time.Now().Add(ttl)
		if pgErr := s.pg.SetVariants(ctx, keyHash, sessionID, variants, activeUntil); pgErr != nil {
			s.log.Warnf("layered: pg SetVariants: %v", pgErr)
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Session prompts — read: Redis first, Postgres fallback; write: both.
// ---------------------------------------------------------------------------

func (s *Store) GetSessionPrompt(ctx context.Context, keyHash, sessionID string) (string, error) {
	prompt, err := s.redis.GetSessionPrompt(ctx, keyHash, sessionID)
	if err == nil && prompt != "" {
		return prompt, nil
	}

	if s.pg == nil {
		return prompt, err
	}

	prompt, pgErr := s.pg.GetSessionPrompt(ctx, keyHash, sessionID)
	if pgErr != nil {
		s.log.Warnf("layered: pg GetSessionPrompt: %v", pgErr)
		return "", err
	}
	return prompt, nil
}

func (s *Store) SetSessionPrompt(ctx context.Context, keyHash, sessionID, prompt string, ttl time.Duration) error {
	err := s.redis.SetSessionPrompt(ctx, keyHash, sessionID, prompt, ttl)

	if s.pg != nil {
		if pgErr := s.pg.SetSessionPrompt(ctx, keyHash, sessionID, prompt); pgErr != nil {
			s.log.Warnf("layered: pg SetSessionPrompt: %v", pgErr)
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Feedback — write: Redis + Postgres; reads: Redis only.
// ---------------------------------------------------------------------------

func (s *Store) RecordFeedback(ctx context.Context, keyHash, sessionID, conversationID, variantID string, rating int) error {
	err := s.redis.RecordFeedback(ctx, keyHash, sessionID, conversationID, variantID, rating)

	if s.pg != nil && conversationID != "" {
		if pgErr := s.pg.RecordFeedback(ctx, keyHash, sessionID, conversationID, variantID, rating); pgErr != nil {
			s.log.Warnf("layered: pg RecordFeedback: %v", pgErr)
		}
	}
	return err
}

func (s *Store) GetSessionInfo(ctx context.Context, keyHash, sessionID string) (*store.SessionInfo, error) {
	return s.redis.GetSessionInfo(ctx, keyHash, sessionID)
}

func (s *Store) GetVariantsInfo(ctx context.Context, keyHash, sessionID string) (*store.VariantsInfo, error) {
	info, err := s.redis.GetVariantsInfo(ctx, keyHash, sessionID)
	if err != nil {
		return nil, err
	}

	if s.pg == nil {
		return info, nil
	}

	// Fill in any gaps from Postgres (cold Redis after restart).
	if len(info.Variants) == 0 {
		if vs, pgErr := s.pg.GetActiveVariants(ctx, keyHash, sessionID); pgErr == nil && vs != nil {
			info.Variants = vs.Variants
		}
	}
	if info.BestPrompt == nil {
		if best, pgErr := s.pg.GetBestPrompt(ctx, keyHash, sessionID); pgErr == nil && best != nil {
			info.BestPrompt = best
		}
	}
	if len(info.History) == 0 {
		if hist, pgErr := s.pg.GetHistory(ctx, keyHash, sessionID, 50); pgErr == nil {
			info.History = hist
		}
	}
	return info, nil
}

func (s *Store) GetVariantFeedback(ctx context.Context, keyHash, sessionID, variantID string) (store.FeedbackSummary, error) {
	return s.redis.GetVariantFeedback(ctx, keyHash, sessionID, variantID)
}

func (s *Store) GetSessionFeedback(ctx context.Context, keyHash, sessionID string) (store.FeedbackSummary, error) {
	return s.redis.GetSessionFeedback(ctx, keyHash, sessionID)
}

// ---------------------------------------------------------------------------
// Optimizer writes — write: Redis + Postgres.
// ---------------------------------------------------------------------------

func (s *Store) UpdateBestPrompt(ctx context.Context, keyHash, sessionID string, best store.BestPrompt) error {
	err := s.redis.UpdateBestPrompt(ctx, keyHash, sessionID, best)

	if s.pg != nil {
		if pgErr := s.pg.UpdateBestPrompt(ctx, keyHash, sessionID, best); pgErr != nil {
			s.log.Warnf("layered: pg UpdateBestPrompt: %v", pgErr)
		}
	}
	return err
}

func (s *Store) AppendHistory(ctx context.Context, keyHash, sessionID string, entry store.HistoryEntry) error {
	err := s.redis.AppendHistory(ctx, keyHash, sessionID, entry)

	if s.pg != nil {
		if pgErr := s.pg.AppendHistory(ctx, keyHash, sessionID, entry); pgErr != nil {
			s.log.Warnf("layered: pg AppendHistory: %v", pgErr)
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Logging — Redis only (ephemeral rolling window).
// ---------------------------------------------------------------------------

func (s *Store) LogRequest(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, promptSnippet, prompt, promptOriginal string, body []byte) error {
	return s.redis.LogRequest(ctx, keyHash, sessionID, variantID, conversationID, contentType, promptSnippet, prompt, promptOriginal, body)
}

func (s *Store) LogResponse(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, responseText string, body []byte) error {
	return s.redis.LogResponse(ctx, keyHash, sessionID, variantID, conversationID, contentType, responseText, body)
}

// ---------------------------------------------------------------------------
// Optimizer reads — Redis only.
// ---------------------------------------------------------------------------

func (s *Store) ReadySessions(ctx context.Context, minSamples int, optimizeEvery time.Duration) ([]store.SessionRef, error) {
	return s.redis.ReadySessions(ctx, minSamples, optimizeEvery)
}

func (s *Store) LoadDataset(ctx context.Context, keyHash, sessionID string) (gepa.Dataset, error) {
	return s.redis.LoadDataset(ctx, keyHash, sessionID)
}

func (s *Store) LoadConversationSamples(ctx context.Context, keyHash, sessionID string, perVariant int) ([]store.ConversationFeedback, error) {
	return s.redis.LoadConversationSamples(ctx, keyHash, sessionID, perVariant)
}

func (s *Store) RollupConversationFeedback(ctx context.Context, keyHash, sessionID string) error {
	return s.redis.RollupConversationFeedback(ctx, keyHash, sessionID)
}

func (s *Store) MarkSessionOptimized(ctx context.Context, keyHash, sessionID string) error {
	return s.redis.MarkSessionOptimized(ctx, keyHash, sessionID)
}

var _ store.Store = (*Store)(nil)
