package redis

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"promptguru/internal/optimizer/gepa"
	"promptguru/internal/store"
)

func (s *Store) RecordFeedback(ctx context.Context, keyHash, sessionID, conversationID, variantID string, rating int) error {
	if conversationID == "" {
		return nil
	}
	key := store.KeyConversationFeedback(keyHash, sessionID, conversationID)
	pipe := s.redis().Pipeline()
	if rating > 0 {
		pipe.HIncrBy(ctx, key, "up", 1)
	} else if rating < 0 {
		pipe.HIncrBy(ctx, key, "down", 1)
	}
	pipe.HSet(ctx, key, "variantId", variantID)
	pipe.HSet(ctx, key, "lastUpdated", time.Now().Unix())
	pipe.Expire(ctx, key, 90*24*time.Hour)

	skey := store.KeySessionFeedback(keyHash, sessionID)
	pipe.HIncrBy(ctx, skey, "totalRated", 1)
	if rating > 0 {
		pipe.HIncrBy(ctx, skey, "up", 1)
	} else if rating < 0 {
		pipe.HIncrBy(ctx, skey, "down", 1)
	}
	pipe.HSet(ctx, skey, "lastUpdated", time.Now().Unix())

	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) GetSessionInfo(ctx context.Context, keyHash, sessionID string) (*store.SessionInfo, error) {
	logKey := store.KeyLog(keyHash, sessionID)
	entries, _ := s.redis().LRange(ctx, logKey, 0, 20).Result()

	var variantID string
	var promptSnippet string
	var conversationID string
	for _, raw := range entries {
		var entry logEntry
		if json.Unmarshal([]byte(raw), &entry) != nil {
			continue
		}
		if entry.Role == "request" && promptSnippet == "" {
			promptSnippet = entry.PromptSnippet
			conversationID = entry.ConversationID
		}
		if entry.VariantID != "" && variantID == "" {
			variantID = entry.VariantID
		}
	}

	summary := store.FeedbackSummary{}
	if variantID != "" {
		summary, _ = s.GetVariantFeedback(ctx, keyHash, sessionID, variantID)
	}

	return &store.SessionInfo{
		SessionID:       sessionID,
		VariantID:       variantID,
		ConversationID:  conversationID,
		PromptSnippet:   promptSnippet,
		FeedbackSummary: summary,
	}, nil
}

func (s *Store) GetVariantsInfo(ctx context.Context, keyHash, sessionID string) (*store.VariantsInfo, error) {
	info := &store.VariantsInfo{SessionID: sessionID}
	if raw, err := s.redis().Get(ctx, store.KeyVariants(keyHash, sessionID)).Bytes(); err == nil {
		var vs store.VariantSet
		if json.Unmarshal(raw, &vs) == nil {
			info.Variants = vs.Variants
		}
	}

	if raw, err := s.redis().Get(ctx, store.KeyBestPrompt(keyHash, sessionID)).Bytes(); err == nil {
		var best store.BestPrompt
		if json.Unmarshal(raw, &best) == nil {
			info.BestPrompt = &best
		}
	}

	entries, _ := s.redis().LRange(ctx, store.KeyHistory(keyHash, sessionID), 0, 49).Result()
	for _, raw := range entries {
		var entry store.HistoryEntry
		if json.Unmarshal([]byte(raw), &entry) == nil {
			info.History = append(info.History, entry)
		}
	}

	return info, nil
}

func (s *Store) GetVariantFeedback(ctx context.Context, keyHash, sessionID, variantID string) (store.FeedbackSummary, error) {
	if variantID == "" {
		return store.FeedbackSummary{}, nil
	}
	fkey := store.KeyFeedback(keyHash, sessionID, variantID)
	vals, err := s.redis().HGetAll(ctx, fkey).Result()
	if err != nil {
		return store.FeedbackSummary{}, err
	}
	return store.FeedbackSummary{
		Up:   parseInt64(vals["up"]),
		Down: parseInt64(vals["down"]),
	}, nil
}

func (s *Store) GetSessionFeedback(ctx context.Context, keyHash, sessionID string) (store.FeedbackSummary, error) {
	skey := store.KeySessionFeedback(keyHash, sessionID)
	vals, err := s.redis().HGetAll(ctx, skey).Result()
	if err != nil {
		return store.FeedbackSummary{}, err
	}
	return store.FeedbackSummary{
		Up:   parseInt64(vals["up"]),
		Down: parseInt64(vals["down"]),
	}, nil
}

func (s *Store) ReadySessions(ctx context.Context, minSamples int, optimizeEvery time.Duration) ([]store.SessionRef, error) {
	var cursor uint64
	results := make([]store.SessionRef, 0)
	pattern := "pg:session_feedback:*"
	for {
		keys, next, err := s.redis().Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return results, err
		}
		for _, key := range keys {
			parts := strings.Split(key, ":")
			if len(parts) != 4 {
				continue
			}
			keyHash := parts[2]
			sessionID := parts[3]
			vals, err := s.redis().HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			totalRated := parseInt64(vals["totalRated"])
			lastOptimized := parseInt64(vals["lastOptimized"])
			if int(totalRated) < minSamples {
				continue
			}
			if lastOptimized > 0 && time.Since(time.Unix(lastOptimized, 0)) < optimizeEvery {
				continue
			}
			results = append(results, store.SessionRef{KeyHash: keyHash, SessionID: sessionID})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return results, nil
}

func (s *Store) LoadDataset(ctx context.Context, keyHash, sessionID string) (gepa.Dataset, error) {
	return s.datasetFromSession(ctx, keyHash, sessionID)
}

func (s *Store) datasetFromSession(ctx context.Context, keyHash, sessionID string) (gepa.Dataset, error) {
	entries, err := s.redis().LRange(ctx, store.KeyLog(keyHash, sessionID), 0, 50).Result()
	if err != nil {
		return nil, err
	}
	reqPrompt := ""
	resp := ""
	variantID := ""
	conversationID := ""
	dataset := make(gepa.Dataset, 0)

	for _, raw := range entries {
		var entry logEntry
		if json.Unmarshal([]byte(raw), &entry) != nil {
			continue
		}
		if entry.Role == "request" {
			reqPrompt = entry.PromptOriginal
			if reqPrompt == "" {
				if entry.Prompt != "" {
					reqPrompt = entry.Prompt
				} else {
					reqPrompt = entry.PromptSnippet
				}
			}
			variantID = entry.VariantID
			conversationID = entry.ConversationID
		}
		if entry.Role == "response" {
			if entry.ResponseText != "" {
				resp = entry.ResponseText
			} else {
				resp = entry.ResponseSnippet
			}
			rating := 0.0
			if variantID != "" {
				summary, _ := s.GetVariantFeedback(ctx, keyHash, sessionID, variantID)
				if summary.Up > summary.Down {
					rating = 1
				} else if summary.Down > summary.Up {
					rating = -1
				}
			}
			if reqPrompt != "" || resp != "" {
				dataset = append(dataset, gepa.DataPoint{
					Input:     reqPrompt,
					Output:    resp,
					Rating:    rating,
					VariantID: variantID,
					ASI:       conversationID,
				})
			}
			reqPrompt = ""
			resp = ""
			variantID = ""
			conversationID = ""
		}
	}

	return dataset, nil
}

func (s *Store) UpdateBestPrompt(ctx context.Context, keyHash, sessionID string, best store.BestPrompt) error {
	raw, _ := json.Marshal(best)
	key := store.KeyBestPrompt(keyHash, sessionID)
	return s.redis().Set(ctx, key, raw, 0).Err()
}

func (s *Store) AppendHistory(ctx context.Context, keyHash, sessionID string, entry store.HistoryEntry) error {
	raw, _ := json.Marshal(entry)
	key := store.KeyHistory(keyHash, sessionID)
	pipe := s.redis().Pipeline()
	pipe.LPush(ctx, key, raw)
	pipe.LTrim(ctx, key, 0, 49)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) MarkSessionOptimized(ctx context.Context, keyHash, sessionID string) error {
	key := store.KeySessionFeedback(keyHash, sessionID)
	return s.redis().HSet(ctx, key, "lastOptimized", time.Now().Unix()).Err()
}

func (s *Store) LoadConversationSamples(ctx context.Context, keyHash, sessionID string, perVariant int) ([]store.ConversationFeedback, error) {
	// Build map conversationID -> prompt/response/variant from logs.
	logs, _ := s.redis().LRange(ctx, store.KeyLog(keyHash, sessionID), 0, 200).Result()
	convoMap := make(map[string]*store.ConversationFeedback)
	for _, raw := range logs {
		var entry logEntry
		if json.Unmarshal([]byte(raw), &entry) != nil {
			continue
		}
		if entry.ConversationID == "" {
			continue
		}
		cf := convoMap[entry.ConversationID]
		if cf == nil {
			cf = &store.ConversationFeedback{ConversationID: entry.ConversationID, VariantID: entry.VariantID}
			convoMap[entry.ConversationID] = cf
		}
		if entry.Role == "request" && cf.Prompt == "" {
			if entry.PromptOriginal != "" {
				cf.Prompt = entry.PromptOriginal
			} else if entry.Prompt != "" {
				cf.Prompt = entry.Prompt
			} else {
				cf.Prompt = entry.PromptSnippet
			}
			if cf.VariantID == "" {
				cf.VariantID = entry.VariantID
			}
		}
		if entry.Role == "response" && cf.Response == "" {
			if entry.ResponseText != "" {
				cf.Response = entry.ResponseText
			} else {
				cf.Response = entry.ResponseSnippet
			}
			if cf.VariantID == "" {
				cf.VariantID = entry.VariantID
			}
		}
	}

	// Scan conversation feedback keys.
	var cursor uint64
	variantBuckets := map[string][]store.ConversationFeedback{}
	pattern := store.KeyConversationFeedback(keyHash, sessionID, "*")
	for {
		keys, next, err := s.redis().Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			vals, err := s.redis().HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			convID := strings.TrimPrefix(key, "pg:conversation_feedback:"+keyHash+":"+sessionID+":")
			cf := convoMap[convID]
			if cf == nil {
				continue
			}
			up := parseInt64(vals["up"])
			down := parseInt64(vals["down"])
			score := 0
			if up > down {
				score = 1
			} else if down > up {
				score = -1
			}
			cf.Score = score
			if cf.VariantID == "" {
				cf.VariantID = vals["variantId"]
			}
			variantID := cf.VariantID
			variantBuckets[variantID] = append(variantBuckets[variantID], *cf)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	// Balanced selection per variant.
	selected := make([]store.ConversationFeedback, 0)
	for _, list := range variantBuckets {
		selected = append(selected, selectBalanced(list, perVariant)...)
	}

	// Stable ordering for determinism.
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].ConversationID < selected[j].ConversationID
	})
	return selected, nil
}

func (s *Store) RollupConversationFeedback(ctx context.Context, keyHash, sessionID string) error {
	var cursor uint64
	pattern := store.KeyConversationFeedback(keyHash, sessionID, "*")
	for {
		keys, next, err := s.redis().Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			vals, err := s.redis().HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			variantID := vals["variantId"]
			up := parseInt64(vals["up"])
			down := parseInt64(vals["down"])
			if variantID == "" {
				skey := store.KeySessionFeedback(keyHash, sessionID)
				if up > 0 {
					s.redis().HIncrBy(ctx, skey, "up", up)
				}
				if down > 0 {
					s.redis().HIncrBy(ctx, skey, "down", down)
				}
			} else {
				vkey := store.KeyFeedback(keyHash, sessionID, variantID)
				if up > 0 {
					s.redis().HIncrBy(ctx, vkey, "up", up)
				}
				if down > 0 {
					s.redis().HIncrBy(ctx, vkey, "down", down)
				}
			}
			s.redis().Del(ctx, key)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}

func parseInt64(raw string) int64 {
	if raw == "" {
		return 0
	}
	val, _ := strconv.ParseInt(raw, 10, 64)
	return val
}

func selectBalanced(list []store.ConversationFeedback, n int) []store.ConversationFeedback {
	if n <= 0 || len(list) == 0 {
		return nil
	}
	pos := make([]store.ConversationFeedback, 0)
	neg := make([]store.ConversationFeedback, 0)
	neu := make([]store.ConversationFeedback, 0)
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

var _ store.Store = (*Store)(nil)
