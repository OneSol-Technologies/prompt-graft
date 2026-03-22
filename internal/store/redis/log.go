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
    TS             int64  `json:"ts"`
    Role           string `json:"role"`
    ContentType    string `json:"contentType"`
    PromptSnippet  string `json:"promptSnippet"`
    Prompt         string `json:"prompt"`
    VariantID      string `json:"variantId"`
    ConversationID string `json:"conversationId"`
    ResponseSnippet string `json:"responseSnippet"`
    SessionID      string `json:"sessionId"`
}

func (s *Store) LogRequest(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, promptSnippet, prompt string, body []byte) error {
    entry := logEntry{
        TS:            time.Now().Unix(),
        Role:          "request",
        ContentType:   contentType,
        PromptSnippet: promptSnippet,
        Prompt:        prompt,
        VariantID:     variantID,
        ConversationID: conversationID,
        SessionID:     sessionID,
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

func (s *Store) LogResponse(ctx context.Context, keyHash, sessionID, variantID, conversationID string, body []byte) error {
    snippet := snippetBytes(body, 400)
    entry := logEntry{
        TS:             time.Now().Unix(),
        Role:           "response",
        VariantID:      variantID,
        ConversationID: conversationID,
        ResponseSnippet: snippet,
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
