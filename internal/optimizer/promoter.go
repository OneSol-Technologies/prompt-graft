package optimizer

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "time"

    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/optimizer/gepa"
    "promptguru/internal/store"
)

type Promoter struct {
    store store.Store
    cfg   *config.Config
    log   *logging.Logger
}

func NewPromoter(st store.Store, cfg *config.Config, log *logging.Logger) *Promoter {
    return &Promoter{store: st, cfg: cfg, log: log}
}

func (p *Promoter) Promote(ctx context.Context, keyHash, sessionID string, result *gepa.Result) error {
    if result == nil || len(result.Candidates) == 0 {
        return nil
    }
    variants := make([]store.Variant, 0, len(result.Candidates))
    weight := 1.0 / float64(len(result.Candidates))
    for _, c := range result.Candidates {
        variants = append(variants, store.Variant{
            ID:           hashPrompt(c.Prompt),
            SystemPrompt: c.Prompt,
            Weight:       weight,
        })
    }
    if err := p.store.SetVariants(ctx, keyHash, sessionID, variants, p.cfg.MaxVariantAge); err != nil {
        return err
    }
    best := store.BestPrompt{
        Prompt:     result.Best.Prompt,
        Score:      result.Best.AggScore,
        PromotedAt: time.Now().Unix(),
    }
    _ = p.store.UpdateBestPrompt(ctx, keyHash, sessionID, best)
    _ = p.store.AppendHistory(ctx, keyHash, sessionID, store.HistoryEntry{
        Prompt:     result.Best.Prompt,
        Score:      result.Best.AggScore,
        PromotedAt: time.Now().Unix(),
    })
    _ = p.store.MarkSessionOptimized(ctx, keyHash, sessionID)
    return nil
}

func hashPrompt(prompt string) string {
    sum := sha256.Sum256([]byte(prompt))
    return hex.EncodeToString(sum[:8])
}
