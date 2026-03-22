package redis

import (
    "context"
    "encoding/json"
    "time"

    goredis "github.com/redis/go-redis/v9"

    "promptguru/internal/store"
)

func (s *Store) GetVariant(ctx context.Context, keyHash, sessionID string) (*store.VariantSet, error) {
    raw, err := s.redis().Get(ctx, store.KeyVariants(keyHash, sessionID)).Bytes()
    if err != nil {
        return nil, err
    }
    var vs store.VariantSet
    if err := json.Unmarshal(raw, &vs); err != nil {
        return nil, err
    }
    if vs.ActiveUntil > 0 && time.Now().Unix() > vs.ActiveUntil {
        return nil, goredis.Nil
    }
    return &vs, nil
}

func (s *Store) SetVariants(ctx context.Context, keyHash, sessionID string, variants []store.Variant, ttl time.Duration) error {
    vs := store.VariantSet{
        Variants:    variants,
        ActiveUntil: time.Now().Add(ttl).Unix(),
    }
    raw, _ := json.Marshal(vs)
    key := store.KeyVariants(keyHash, sessionID)
    return s.redis().Set(ctx, key, raw, ttl).Err()
}

func (s *Store) GetSessionPrompt(ctx context.Context, keyHash, sessionID string) (string, error) {
    return s.redis().Get(ctx, store.KeySessionPrompt(keyHash, sessionID)).Result()
}

func (s *Store) SetSessionPrompt(ctx context.Context, keyHash, sessionID, prompt string, ttl time.Duration) error {
    if prompt == "" {
        return nil
    }
    return s.redis().Set(ctx, store.KeySessionPrompt(keyHash, sessionID), prompt, ttl).Err()
}
