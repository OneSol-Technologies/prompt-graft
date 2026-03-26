package pg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"promptguru/internal/store"
)

// Store implements the durable subset of store.Store against Postgres.
// It is not a full store.Store; it is composed by layered.Store.
type Store struct {
	client *Client
}

// NewStore returns a Store backed by the given Client.
func NewStore(client *Client) *Store {
	return &Store{client: client}
}

// ---------------------------------------------------------------------------
// Variants
// ---------------------------------------------------------------------------

// SetVariants persists a full variant set for a session, retiring any
// previously active variants first.
func (s *Store) SetVariants(ctx context.Context, keyHash, sessionID string, variants []store.Variant, activeUntil time.Time) error {
	tx, err := s.client.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Retire all current active variants for this session.
	_, err = tx.Exec(ctx,
		`UPDATE variants SET retired_at = now()
		 WHERE key_hash = $1 AND session_id = $2 AND retired_at IS NULL`,
		keyHash, sessionID,
	)
	if err != nil {
		return err
	}

	// Insert new variants.
	for _, v := range variants {
		_, err = tx.Exec(ctx,
			`INSERT INTO variants (id, key_hash, session_id, system_prompt, weight, active_until)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (key_hash, session_id, id) DO UPDATE
			   SET system_prompt = EXCLUDED.system_prompt,
			       weight        = EXCLUDED.weight,
			       active_until  = EXCLUDED.active_until,
			       retired_at    = NULL`,
			v.ID, keyHash, sessionID, v.SystemPrompt, v.Weight, activeUntil,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetActiveVariants returns the active variant set for a session.
// Returns nil, nil when no active variants exist.
func (s *Store) GetActiveVariants(ctx context.Context, keyHash, sessionID string) (*store.VariantSet, error) {
	rows, err := s.client.pool.Query(ctx,
		`SELECT id, system_prompt, weight, active_until
		 FROM variants
		 WHERE key_hash = $1 AND session_id = $2 AND retired_at IS NULL
		 ORDER BY id`,
		keyHash, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []store.Variant
	var activeUntil time.Time
	for rows.Next() {
		var v store.Variant
		var au time.Time
		if err := rows.Scan(&v.ID, &v.SystemPrompt, &v.Weight, &au); err != nil {
			return nil, err
		}
		variants = append(variants, v)
		if au.After(activeUntil) {
			activeUntil = au
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(variants) == 0 {
		return nil, nil
	}
	return &store.VariantSet{
		Variants:    variants,
		ActiveUntil: activeUntil.Unix(),
	}, nil
}

// RetireVariants marks all active variants for a session as retired.
func (s *Store) RetireVariants(ctx context.Context, keyHash, sessionID string) error {
	_, err := s.client.pool.Exec(ctx,
		`UPDATE variants SET retired_at = now()
		 WHERE key_hash = $1 AND session_id = $2 AND retired_at IS NULL`,
		keyHash, sessionID,
	)
	return err
}

// FindUnusedVariantSessions returns sessions that have active variants but
// no feedback events in the last unusedTTL window.
func (s *Store) FindUnusedVariantSessions(ctx context.Context, unusedTTL time.Duration) ([]store.SessionRef, error) {
	cutoff := time.Now().Add(-unusedTTL)
	rows, err := s.client.pool.Query(ctx,
		`SELECT DISTINCT v.key_hash, v.session_id
		 FROM variants v
		 WHERE v.retired_at IS NULL
		   AND NOT EXISTS (
		       SELECT 1 FROM feedback_events fe
		       WHERE fe.key_hash   = v.key_hash
		         AND fe.session_id = v.session_id
		         AND fe.created_at > $1
		   )`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []store.SessionRef
	for rows.Next() {
		var ref store.SessionRef
		if err := rows.Scan(&ref.KeyHash, &ref.SessionID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// ---------------------------------------------------------------------------
// Feedback
// ---------------------------------------------------------------------------

// RecordFeedback appends a durable feedback event row.
func (s *Store) RecordFeedback(ctx context.Context, keyHash, sessionID, conversationID, variantID string, rating int) error {
	_, err := s.client.pool.Exec(ctx,
		`INSERT INTO feedback_events (key_hash, session_id, conversation_id, variant_id, rating)
		 VALUES ($1, $2, $3, $4, $5)`,
		keyHash, sessionID, conversationID, variantID, rating,
	)
	return err
}

// ---------------------------------------------------------------------------
// Session prompts
// ---------------------------------------------------------------------------

// SetSessionPrompt upserts the inferred prompt template for a session.
func (s *Store) SetSessionPrompt(ctx context.Context, keyHash, sessionID, prompt string) error {
	_, err := s.client.pool.Exec(ctx,
		`INSERT INTO session_prompts (key_hash, session_id, prompt, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (key_hash, session_id) DO UPDATE
		   SET prompt = EXCLUDED.prompt, updated_at = now()`,
		keyHash, sessionID, prompt,
	)
	return err
}

// GetSessionPrompt returns the stored prompt template.
// Returns "", nil when no template exists.
func (s *Store) GetSessionPrompt(ctx context.Context, keyHash, sessionID string) (string, error) {
	var prompt string
	err := s.client.pool.QueryRow(ctx,
		`SELECT prompt FROM session_prompts WHERE key_hash = $1 AND session_id = $2`,
		keyHash, sessionID,
	).Scan(&prompt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return prompt, err
}

// ---------------------------------------------------------------------------
// Best prompt
// ---------------------------------------------------------------------------

// UpdateBestPrompt upserts the current best prompt for a session.
func (s *Store) UpdateBestPrompt(ctx context.Context, keyHash, sessionID string, best store.BestPrompt) error {
	_, err := s.client.pool.Exec(ctx,
		`INSERT INTO best_prompts (key_hash, session_id, prompt, score, promoted_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (key_hash, session_id) DO UPDATE
		   SET prompt = EXCLUDED.prompt, score = EXCLUDED.score, promoted_at = EXCLUDED.promoted_at`,
		keyHash, sessionID, best.Prompt, best.Score, time.Unix(best.PromotedAt, 0),
	)
	return err
}

// GetBestPrompt returns the best prompt for a session.
// Returns nil, nil when none exists.
func (s *Store) GetBestPrompt(ctx context.Context, keyHash, sessionID string) (*store.BestPrompt, error) {
	var b store.BestPrompt
	var promotedAt time.Time
	err := s.client.pool.QueryRow(ctx,
		`SELECT prompt, score, promoted_at FROM best_prompts WHERE key_hash = $1 AND session_id = $2`,
		keyHash, sessionID,
	).Scan(&b.Prompt, &b.Score, &promotedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.PromotedAt = promotedAt.Unix()
	return &b, nil
}

// ---------------------------------------------------------------------------
// History
// ---------------------------------------------------------------------------

// AppendHistory inserts a new history entry for a session.
func (s *Store) AppendHistory(ctx context.Context, keyHash, sessionID string, entry store.HistoryEntry) error {
	var retiredAt *time.Time
	if entry.RetiredAt != 0 {
		t := time.Unix(entry.RetiredAt, 0)
		retiredAt = &t
	}
	_, err := s.client.pool.Exec(ctx,
		`INSERT INTO prompt_history (key_hash, session_id, prompt, score, promoted_at, retired_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		keyHash, sessionID, entry.Prompt, entry.Score, time.Unix(entry.PromotedAt, 0), retiredAt,
	)
	return err
}

// GetHistory returns up to limit history entries for a session, newest first.
func (s *Store) GetHistory(ctx context.Context, keyHash, sessionID string, limit int) ([]store.HistoryEntry, error) {
	rows, err := s.client.pool.Query(ctx,
		`SELECT prompt, score, promoted_at, retired_at
		 FROM prompt_history
		 WHERE key_hash = $1 AND session_id = $2
		 ORDER BY promoted_at DESC
		 LIMIT $3`,
		keyHash, sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []store.HistoryEntry
	for rows.Next() {
		var e store.HistoryEntry
		var promotedAt time.Time
		var retiredAt *time.Time
		if err := rows.Scan(&e.Prompt, &e.Score, &promotedAt, &retiredAt); err != nil {
			return nil, err
		}
		e.PromotedAt = promotedAt.Unix()
		if retiredAt != nil {
			e.RetiredAt = retiredAt.Unix()
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
