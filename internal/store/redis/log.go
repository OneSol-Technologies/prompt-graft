package redis

import (
	"context"
	"encoding/json"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"promptguru/internal/store"
)

type Store struct {
	client *Client
}

func NewStore(client *Client) *Store {
	return &Store{client: client}
}

type logEntry struct {
	TS              int64  `json:"ts"`
	Role            string `json:"role"`
	ContentType     string `json:"contentType"`
	PromptSnippet   string `json:"promptSnippet"`
	Prompt          string `json:"prompt"`
	PromptOriginal  string `json:"promptOriginal"`
	VariantID       string `json:"variantId"`
	ConversationID  string `json:"conversationId"`
	ResponseText    string `json:"responseText"`
	ResponseSnippet string `json:"responseSnippet"`
	SessionID       string `json:"sessionId"`
}

func (s *Store) LogRequest(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, promptSnippet, prompt, promptOriginal string, body []byte) error {
	entry := logEntry{
		TS:             time.Now().Unix(),
		Role:           "request",
		ContentType:    contentType,
		PromptSnippet:  promptSnippet,
		Prompt:         prompt,
		PromptOriginal: promptOriginal,
		VariantID:      variantID,
		ConversationID: conversationID,
		SessionID:      sessionID,
	}
	raw, _ := json.Marshal(entry)
	key := store.KeyLog(keyHash, sessionID)
	pipe := s.client.rdb.Pipeline()
	pipe.LPush(ctx, key, raw)
	pipe.LTrim(ctx, key, 0, 99)
	pipe.Expire(ctx, key, 30*24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) LogResponse(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, responseText string, body []byte) error {
	snippet := snippetBytes(body, 400)
	entry := logEntry{
		TS:              time.Now().Unix(),
		Role:            "response",
		ContentType:     contentType,
		VariantID:       variantID,
		ConversationID:  conversationID,
		ResponseText:    responseText,
		ResponseSnippet: snippet,
		SessionID:       sessionID,
	}
	raw, _ := json.Marshal(entry)
	key := store.KeyLog(keyHash, sessionID)
	pipe := s.client.rdb.Pipeline()
	pipe.LPush(ctx, key, raw)
	pipe.LTrim(ctx, key, 0, 99)
	pipe.Expire(ctx, key, 30*24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

// ReadConversationLogs reads up to maxEntries log entries for a session from
// Redis, pairs request+response by conversationID, and returns the combined
// ConversationLog slice.  Entries without a conversationID are skipped.
func (s *Store) ReadConversationLogs(ctx context.Context, keyHash, sessionID string, maxEntries int) []store.ConversationLog {
	key := store.KeyLog(keyHash, sessionID)
	raws, err := s.client.rdb.LRange(ctx, key, 0, int64(maxEntries-1)).Result()
	if err != nil {
		return nil
	}

	type partial struct {
		prompt       string
		responseText string
		variantID    string
	}
	m := map[string]*partial{}
	for _, raw := range raws {
		var entry logEntry
		if json.Unmarshal([]byte(raw), &entry) != nil || entry.ConversationID == "" {
			continue
		}
		p := m[entry.ConversationID]
		if p == nil {
			p = &partial{variantID: entry.VariantID}
			m[entry.ConversationID] = p
		}
		if entry.Role == "request" && p.prompt == "" {
			if entry.PromptOriginal != "" {
				p.prompt = entry.PromptOriginal
			} else if entry.Prompt != "" {
				p.prompt = entry.Prompt
			} else {
				p.prompt = entry.PromptSnippet
			}
		}
		if entry.Role == "response" && p.responseText == "" {
			if entry.ResponseText != "" {
				p.responseText = entry.ResponseText
			} else {
				p.responseText = entry.ResponseSnippet
			}
		}
		if p.variantID == "" && entry.VariantID != "" {
			p.variantID = entry.VariantID
		}
	}

	result := make([]store.ConversationLog, 0, len(m))
	for convID, p := range m {
		result = append(result, store.ConversationLog{
			KeyHash:        keyHash,
			SessionID:      sessionID,
			ConversationID: convID,
			VariantID:      p.variantID,
			Prompt:         p.prompt,
			ResponseText:   p.responseText,
		})
	}
	return result
}

// ScanLogKeys scans all pg:log:* keys in Redis and returns the corresponding
// SessionRefs.  Used by the janitor to enumerate sessions for log copying.
func (s *Store) ScanLogKeys(ctx context.Context) []store.SessionRef {
	var cursor uint64
	var results []store.SessionRef
	for {
		keys, next, err := s.client.rdb.Scan(ctx, cursor, "pg:log:*:*", 100).Result()
		if err != nil {
			break
		}
		for _, key := range keys {
			// key format: pg:log:<keyHash>:<sessionID>
			parts := splitLogKey(key)
			if parts != nil {
				results = append(results, *parts)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return results
}

func splitLogKey(key string) *store.SessionRef {
	// prefix is "pg:log:" (7 chars), then keyHash:sessionID
	const prefix = "pg:log:"
	if len(key) <= len(prefix) {
		return nil
	}
	rest := key[len(prefix):]
	// keyHash and sessionID are both fixed-length hex-ish strings separated by ":"
	// Find first ":" in rest.
	for i, c := range rest {
		if c == ':' {
			return &store.SessionRef{KeyHash: rest[:i], SessionID: rest[i+1:]}
		}
	}
	return nil
}

func snippetBytes(body []byte, max int) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) > max {
		body = body[:max]
	}
	return string(body)
}

var _ store.Store = (*Store)(nil)

func (s *Store) redis() *goredis.Client {
	return s.client.rdb
}
