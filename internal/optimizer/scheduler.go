package optimizer

import (
    "context"
    "math/rand"
    "strings"
    "time"

    "promptguru/internal/config"
    "promptguru/internal/logging"
    "promptguru/internal/optimizer/gepa"
    "promptguru/internal/store"
)

type Scheduler struct {
    store    store.Store
    gepa     *gepa.Optimizer
    promoter *Promoter
    cfg      *config.Config
    log      *logging.Logger
}

func NewScheduler(st store.Store, opt *gepa.Optimizer, promoter *Promoter, cfg *config.Config, log *logging.Logger) *Scheduler {
    return &Scheduler{store: st, gepa: opt, promoter: promoter, cfg: cfg, log: log}
}

func (s *Scheduler) Run(ctx context.Context) {
    s.log.Infof("optimizer: started optimizeEvery=%s minSamples=%d", s.cfg.OptimizeEvery, s.cfg.MinSamples)
    ticker := time.NewTicker(s.cfg.OptimizeEvery)
    defer ticker.Stop()

    for {
        s.runOnce(ctx)
        select {
        case <-ctx.Done():
            s.log.Infof("optimizer: stopping")
            return
        case <-ticker.C:
        }
    }
}

func (s *Scheduler) runOnce(ctx context.Context) {
    s.log.Debugf("optimizer: tick minSamples=%d", s.cfg.MinSamples)
    refs, err := s.store.ReadySessions(ctx, s.cfg.MinSamples, s.cfg.OptimizeEvery)
    if err != nil {
        s.log.Warnf("ready sessions scan failed: %v", err)
        return
    }
    if len(refs) == 0 {
        s.log.Debugf("optimizer: no sessions ready yet (need at least %d rated requests per session)", s.cfg.MinSamples)
        return
    }
    s.log.Debugf("optimizer: ready sessions=%d", len(refs))

    for _, ref := range refs {
        s.log.Debugf("optimizer: session=%s keyHash=%s", ref.SessionID, ref.KeyHash)

        variantSet, _ := s.store.GetVariant(ctx, ref.KeyHash, ref.SessionID)
        hasVariants := variantSet != nil && len(variantSet.Variants) > 0
        s.log.Debugf("optimizer: hasVariants=%t variantCount=%d", hasVariants, variantCount(variantSet))

        var dataset gepa.Dataset
        if hasVariants {
            s.log.Debugf("optimizer: building dataset from variants")
            dataset = buildVariantDataset(ctx, s.store, ref.KeyHash, ref.SessionID, variantSet)
            s.log.Debugf("optimizer: variant dataset size=%d", len(dataset))
            if len(dataset) == 0 {
                s.log.Debugf("optimizer: no dataset entries, skipping")
                continue
            }

            if template, err := s.store.GetSessionPrompt(ctx, ref.KeyHash, ref.SessionID); template == "" && err == nil {
                s.log.Debugf("optimizer: no session prompt stored, inferring from samples")
                if prompts, err := loadSamplePrompts(ctx, s.store, ref.KeyHash, ref.SessionID); err == nil {
                    inferred := deriveTemplate(prompts)
                    s.log.Debugf("optimizer: inferred template=%q", inferred)
                    if inferred != "" {
                        _ = s.store.SetSessionPrompt(ctx, ref.KeyHash, ref.SessionID, inferred, s.cfg.MaxVariantAge)
                    }
                }
            }
        } else {
            s.log.Debugf("optimizer: building dataset from prompts")
            dataset, err = s.store.LoadDataset(ctx, ref.KeyHash, ref.SessionID)
            if err != nil || len(dataset) == 0 {
                s.log.Debugf("optimizer: dataset load failed or empty err=%v", err)
                continue
            }

            prompts := collectPrompts(dataset)
            s.log.Debugf("optimizer: prompt count=%d", len(prompts))
            template := deriveTemplate(prompts)
            s.log.Debugf("optimizer: inferred template=%q", template)
            if template != "" {
                _ = s.store.SetSessionPrompt(ctx, ref.KeyHash, ref.SessionID, template, s.cfg.MaxVariantAge)
            }
        }

        seed := ""
        if template, _ := s.store.GetSessionPrompt(ctx, ref.KeyHash, ref.SessionID); template != "" {
            seed = template
        }
        if seed == "" && len(dataset) > 0 {
            seed = dataset[0].Input
        }
        if seed == "" {
            seed = "You are a helpful assistant."
        }
        s.log.Debugf("optimizer: seed=%q", seed)

        result, err := s.gepa.Run(ctx, seed, dataset)
        if err != nil {
            s.log.Warnf("gepa run failed: %v", err)
            continue
        }
        s.log.Debugf("optimizer: gepa iterations=%d candidates=%d", result.Iterations, len(result.Candidates))
        for _, c := range result.Candidates {
            s.log.Debugf("optimizer: candidate id=%s score=%.4f", c.ID, c.AggScore)
        }

        if err := s.promoter.Promote(ctx, ref.KeyHash, ref.SessionID, result); err != nil {
            s.log.Warnf("promote failed: %v", err)
        } else {
            s.log.Debugf("optimizer: promote complete")
        }
    }
}

func variantCount(vs *store.VariantSet) int {
    if vs == nil {
        return 0
    }
    return len(vs.Variants)
}

func buildVariantDataset(ctx context.Context, st store.Store, keyHash, sessionID string, variants *store.VariantSet) gepa.Dataset {
    if variants == nil || len(variants.Variants) == 0 {
        return nil
    }
    dataset := make(gepa.Dataset, 0, len(variants.Variants)+1)

    // Root/default prompt uses session-level feedback.
    if base, err := st.GetSessionPrompt(ctx, keyHash, sessionID); err == nil && base != "" {
        summary, _ := st.GetSessionFeedback(ctx, keyHash, sessionID)
        dataset = append(dataset, gepa.DataPoint{
            Input:     base,
            Rating:    summaryScore(summary),
            VariantID: "default",
        })
    }

    for _, v := range variants.Variants {
        summary, _ := st.GetVariantFeedback(ctx, keyHash, sessionID, v.ID)
        dataset = append(dataset, gepa.DataPoint{
            Input:     v.SystemPrompt,
            Rating:    summaryScore(summary),
            VariantID: v.ID,
        })
    }
    return dataset
}

func summaryScore(summary store.FeedbackSummary) float64 {
    if summary.Up > summary.Down {
        return 1
    }
    if summary.Down > summary.Up {
        return -1
    }
    return 0
}

func loadSamplePrompts(ctx context.Context, st store.Store, keyHash, sessionID string) ([]string, error) {
    dataset, err := st.LoadDataset(ctx, keyHash, sessionID)
    if err != nil {
        return nil, err
    }
    return collectPrompts(dataset), nil
}

func collectPrompts(dataset gepa.Dataset) []string {
    prompts := make([]string, 0, len(dataset))
    for _, d := range dataset {
        if d.Input != "" {
            prompts = append(prompts, d.Input)
        }
    }
    return prompts
}

func deriveTemplate(prompts []string) string {
    if len(prompts) == 0 {
        return ""
    }
    sample := prompts
    if len(sample) > 10 {
        sample = randomSample(sample, 10)
    }
    template := sample[0]
    for i := 1; i < len(sample); i++ {
        template = commonPrefix(template, sample[i])
        if template == "" {
            break
        }
    }
    return template
}

func commonPrefix(a, b string) string {
    max := len(a)
    if len(b) < max {
        max = len(b)
    }
    idx := 0
    for idx < max && a[idx] == b[idx] {
        idx++
    }
    return strings.TrimSpace(a[:idx])
}

func randomSample(values []string, n int) []string {
    if len(values) <= n {
        return values
    }
    out := make([]string, 0, n)
    perm := rand.Perm(len(values))
    for i := 0; i < n; i++ {
        out = append(out, values[perm[i]])
    }
    return out
}
