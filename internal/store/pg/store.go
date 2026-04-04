package pg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"promptguru/internal/logging"
	"promptguru/internal/store"
)

// Store implements the durable subset of store.Store against Postgres.
// It is not a full store.Store; it is composed by layered.Store.
type Store struct {
	client *Client
	log    *logging.Logger
}

// UnusedSession is returned by FindUnusedVariantSessions and includes
// diagnostic fields so the janitor can log exactly why it's retiring.
type UnusedSession struct {
	store.SessionRef
	ActiveVariants int        // number of non-retired variants
	LastFeedbackAt *time.Time // nil if this session has never had feedback
}

// NewStore returns a Store backed by the given Client.
func NewStore(client *Client, log *logging.Logger) *Store {
	return &Store{client: client, log: log}
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
	s.log.Debugf("pg: GetActiveVariants session=%s keyHash=%s", sessionID, keyHash)
	rows, err := s.client.pool.Query(ctx,
		`SELECT id, system_prompt, weight, active_until
		 FROM variants
		 WHERE key_hash = $1 AND session_id = $2 AND retired_at IS NULL
		 ORDER BY id`,
		keyHash, sessionID,
	)
	if err != nil {
		s.log.Warnf("pg: GetActiveVariants query error session=%s: %v", sessionID, err)
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
		s.log.Debugf("pg: GetActiveVariants session=%s → no active variants (all retired or never set)", sessionID)
		return nil, nil
	}
	s.log.Debugf("pg: GetActiveVariants session=%s → found %d variant(s) activeUntil=%s",
		sessionID, len(variants), activeUntil.Format(time.RFC3339))
	return &store.VariantSet{
		Variants:    variants,
		ActiveUntil: activeUntil.Unix(),
	}, nil
}

// RetireVariants marks all active variants for a session as retired.
func (s *Store) RetireVariants(ctx context.Context, keyHash, sessionID string) error {
	s.log.Debugf("pg: RetireVariants session=%s keyHash=%s", sessionID, keyHash)
	tag, err := s.client.pool.Exec(ctx,
		`UPDATE variants SET retired_at = now()
		 WHERE key_hash = $1 AND session_id = $2 AND retired_at IS NULL`,
		keyHash, sessionID,
	)
	if err != nil {
		s.log.Warnf("pg: RetireVariants session=%s error: %v", sessionID, err)
		return err
	}
	s.log.Infof("pg: RetireVariants session=%s retired %d row(s)", sessionID, tag.RowsAffected())
	return nil
}

// FindUnusedVariantSessions returns sessions that have active variants but
// no feedback events in the last unusedTTL window, with diagnostic metadata.
func (s *Store) FindUnusedVariantSessions(ctx context.Context, unusedTTL time.Duration) ([]UnusedSession, error) {
	cutoff := time.Now().Add(-unusedTTL)
	rows, err := s.client.pool.Query(ctx,
		`SELECT
		     v.key_hash,
		     v.session_id,
		     COUNT(v.id)                             AS active_variants,
		     MAX(fe.created_at)                      AS last_feedback_at
		 FROM variants v
		 LEFT JOIN feedback_events fe
		     ON fe.key_hash   = v.key_hash
		    AND fe.session_id = v.session_id
		 WHERE v.retired_at IS NULL
		 GROUP BY v.key_hash, v.session_id
		 HAVING MAX(fe.created_at) IS NULL
		     OR MAX(fe.created_at) < $1`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []UnusedSession
	for rows.Next() {
		var us UnusedSession
		var lastFeedback *time.Time
		if err := rows.Scan(&us.KeyHash, &us.SessionID, &us.ActiveVariants, &lastFeedback); err != nil {
			return nil, err
		}
		us.LastFeedbackAt = lastFeedback
		sessions = append(sessions, us)
	}
	return sessions, rows.Err()
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

// ---------------------------------------------------------------------------
// Conversation logs
// ---------------------------------------------------------------------------

// InsertConversationLog inserts a conversation log row.
// The UNIQUE constraint on (key_hash, session_id, conversation_id) means
// duplicate inserts are silently ignored (ON CONFLICT DO NOTHING).
func (s *Store) InsertConversationLog(ctx context.Context, entry store.ConversationLog) error {
	_, err := s.client.pool.Exec(ctx,
		`INSERT INTO conversation_logs (key_hash, session_id, conversation_id, variant_id, prompt, response_text)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (key_hash, session_id, conversation_id) DO NOTHING`,
		entry.KeyHash, entry.SessionID, entry.ConversationID,
		nilIfEmpty(entry.VariantID), nilIfEmpty(entry.Prompt), nilIfEmpty(entry.ResponseText),
	)
	return err
}

// ---------------------------------------------------------------------------
// Session metadata / optimizer state
// ---------------------------------------------------------------------------

// MarkSessionOptimized upserts the last_optimized timestamp for a session.
func (s *Store) MarkSessionOptimized(ctx context.Context, keyHash, sessionID string) error {
	_, err := s.client.pool.Exec(ctx,
		`INSERT INTO session_metadata (key_hash, session_id, last_optimized)
		 VALUES ($1, $2, now())
		 ON CONFLICT (key_hash, session_id) DO UPDATE SET last_optimized = now()`,
		keyHash, sessionID,
	)
	return err
}

// ReadySessions returns sessions that have at least minSamples feedback events
// joined to conversation_logs (i.e. input+output is present) and have not been
// optimized within optimizeEvery.
func (s *Store) ReadySessions(ctx context.Context, minSamples int, optimizeEvery time.Duration) ([]store.SessionRef, error) {
	cutoff := time.Now().Add(-optimizeEvery * 10) // add buffer to avoid clock skew issues
	rows, err := s.client.pool.Query(ctx,
		`SELECT fe.key_hash, fe.session_id
		 FROM feedback_events fe
		 JOIN conversation_logs cl
		   ON cl.key_hash        = fe.key_hash
		  AND cl.session_id      = fe.session_id
		  AND cl.conversation_id = fe.conversation_id
		  AND cl.prompt          IS NOT NULL
		  AND cl.response_text   IS NOT NULL
		 LEFT JOIN session_metadata sm
		   ON sm.key_hash   = fe.key_hash
		  AND sm.session_id = fe.session_id
		 WHERE sm.last_optimized IS NULL OR sm.last_optimized < $1
		 GROUP BY fe.key_hash, fe.session_id
		 HAVING COUNT(DISTINCT fe.conversation_id) >= $2`,
		cutoff, minSamples,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []store.SessionRef
	for rows.Next() {
		var ref store.SessionRef
		if err := rows.Scan(&ref.KeyHash, &ref.SessionID); err != nil {
			return nil, err
		}
		results = append(results, ref)
	}
	return results, rows.Err()
}

// LoadConversationSamples returns feedback-linked conversation samples from
// Postgres.  Rows without both prompt and response_text are excluded.
func (s *Store) LoadConversationSamples(ctx context.Context, keyHash, sessionID string, perVariant int) ([]store.ConversationFeedback, error) {
	rows, err := s.client.pool.Query(ctx,
		`SELECT cl.conversation_id,
		        COALESCE(cl.variant_id, '')                                  AS variant_id,
		        cl.prompt,
		        cl.response_text,
		        COALESCE(SUM(CASE WHEN fe.rating > 0 THEN 1 ELSE 0 END), 0) AS up,
		        COALESCE(SUM(CASE WHEN fe.rating < 0 THEN 1 ELSE 0 END), 0) AS down
		 FROM conversation_logs cl
		 LEFT JOIN feedback_events fe
		   ON fe.key_hash        = cl.key_hash
		  AND fe.session_id      = cl.session_id
		  AND fe.conversation_id = cl.conversation_id
		 WHERE cl.key_hash    = $1
		   AND cl.session_id  = $2
		   AND cl.prompt        IS NOT NULL
		   AND cl.response_text IS NOT NULL
		   AND fe.times_used = 0 -- only consider feedback that hasn't been used for optimization yet
		 GROUP BY cl.conversation_id, cl.variant_id, cl.prompt, cl.response_text
		 ORDER BY cl.conversation_id`,
		keyHash, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	variantBuckets := map[string][]store.ConversationFeedback{}
	for rows.Next() {
		var cf store.ConversationFeedback
		var up, down int64
		if err := rows.Scan(&cf.ConversationID, &cf.VariantID, &cf.Prompt, &cf.Response, &up, &down); err != nil {
			return nil, err
		}
		if up > down {
			cf.Score = 1
		} else if down > up {
			cf.Score = -1
		}
		variantBuckets[cf.VariantID] = append(variantBuckets[cf.VariantID], cf)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	selected := make([]store.ConversationFeedback, 0)
	for _, list := range variantBuckets {
		selected = append(selected, selectBalancedPG(list, perVariant)...)
	}
	return selected, nil
}

func selectBalancedPG(list []store.ConversationFeedback, n int) []store.ConversationFeedback {
	if n <= 0 || len(list) == 0 {
		return nil
	}
	pos := []store.ConversationFeedback{}
	neg := []store.ConversationFeedback{}
	neu := []store.ConversationFeedback{}
	for _, item := range list {
		switch item.Score {
		case 1:
			pos = append(pos, item)
		case -1:
			neg = append(neg, item)
		default:
			neu = append(neu, item)
		}
	}
	out := make([]store.ConversationFeedback, 0, n)
	buckets := [][]store.ConversationFeedback{pos, neg, neu}
	for len(out) < n {
		progressed := false
		for i := range buckets {
			if len(buckets[i]) == 0 {
				continue
			}
			out = append(out, buckets[i][0])
			buckets[i] = buckets[i][1:]
			progressed = true
			if len(out) >= n {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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

func (s *Store) MarkFeedbackUsed(ctx context.Context, keyHash string, sessionID string, conversationIDs []string) error {
	if len(conversationIDs) == 0 {
		return nil
	}
	_, err := s.client.pool.Exec(ctx,
		`UPDATE feedback_events SET times_used = times_used + 1
		 WHERE key_hash = $1 AND session_id = $2 AND conversation_id = ANY($3)`,
		keyHash, sessionID, conversationIDs,
	)
	return err
}
