package gepa

import (
    "context"
    crand "crypto/rand"
    "encoding/hex"
    "math"
    mrand "math/rand"
    "time"
)

type Config struct {
    RolloutBudget int
    MinibatchSize int
    ParetoFraction float64
    MinDelta float64
    CrossoverEnabled bool
    CrossoverFrequency float64
    TopN int
    Seed int64
    LLM LLMClient
    Scorer ScoringFn
}

type Result struct {
    Candidates []*Candidate
    Best *Candidate
    Iterations int
    History []*Candidate
}

type Optimizer struct {
    cfg Config
}

func New(cfg Config) *Optimizer {
    if cfg.RolloutBudget == 0 { cfg.RolloutBudget = 50 }
    if cfg.MinibatchSize == 0 { cfg.MinibatchSize = 5 }
    if cfg.ParetoFraction == 0 { cfg.ParetoFraction = 0.3 }
    if cfg.MinDelta == 0 { cfg.MinDelta = 0.01 }
    if cfg.TopN == 0 { cfg.TopN = 3 }
    if !cfg.CrossoverEnabled { cfg.CrossoverFrequency = 0 }
    return &Optimizer{cfg: cfg}
}

func (o *Optimizer) Run(ctx context.Context, seedPrompt string, dataset Dataset) (*Result, error) {
    if len(dataset) == 0 {
        return &Result{}, nil
    }
    paretoSet, feedbackSet := dataset.Split(o.cfg.ParetoFraction, o.seed())
    if len(paretoSet) == 0 {
        paretoSet = dataset
    }
    scorer := o.cfg.Scorer
    if scorer == nil {
        scorer = FeedbackScorer(dataset)
    }

    baseScores, _ := scorer(ctx, seedPrompt, paretoSet)
    base := &Candidate{
        ID: genID(),
        Prompt: seedPrompt,
        Scores: baseScores,
        AggScore: mean(baseScores),
        Generation: 0,
    }

    pool := &CandidatePool{}
    pool.Add(base)

    reflector := NewReflector(o.cfg.LLM)
    weights := map[string]float64{}
    history := []*Candidate{base}

    rng := mrand.New(mrand.NewSource(o.seed()))
    iterations := 0

    for i := 0; i < o.cfg.RolloutBudget; i++ {
        frontier := ParetoFrontier(pool.All())
        parent := SampleFromFrontier(frontier, weights, rng)
        if parent == nil {
            break
        }
        minibatch := feedbackSet
        if len(feedbackSet) > 0 {
            minibatch = feedbackSet.Minibatch(o.cfg.MinibatchSize, rng)
        } else {
            minibatch = paretoSet.Minibatch(o.cfg.MinibatchSize, rng)
        }
        if len(minibatch) == 0 {
            break
        }

        parentScores, _ := scorer(ctx, parent.Prompt, minibatch)
        parentScore := mean(parentScores)
        reflection, _ := reflector.Reflect(ctx, parent, minibatch, parentScores)

        childPrompt := parent.Prompt
        if o.cfg.CrossoverEnabled && rng.Float64() < o.cfg.CrossoverFrequency && len(frontier) >= 2 {
            other := frontier[rng.Intn(len(frontier))]
            childPrompt, _ = reflector.Crossover(ctx, parent, other)
        } else {
            childPrompt, _ = reflector.Mutate(ctx, parent, reflection)
        }

        childScores, _ := scorer(ctx, childPrompt, minibatch)
        childScore := mean(childScores)
        if childScore <= parentScore+o.cfg.MinDelta {
            continue
        }

        paretoScores, _ := scorer(ctx, childPrompt, paretoSet)
        child := &Candidate{
            ID: genID(),
            Prompt: childPrompt,
            ParentID: parent.ID,
            Scores: paretoScores,
            AggScore: mean(paretoScores),
            Generation: i + 1,
            ASI: reflection.ASI,
        }
        pool.Add(child)
        weights[parent.ID] += 1
        history = append(history, child)
        iterations++
    }

    best := pool.Best()
    candidates := topN(pool.All(), o.cfg.TopN)

    return &Result{
        Candidates: candidates,
        Best: best,
        Iterations: iterations,
        History: history,
    }, nil
}

func (o *Optimizer) seed() int64 {
    if o.cfg.Seed != 0 {
        return o.cfg.Seed
    }
    return time.Now().UnixNano()
}

func mean(vals []float64) float64 {
    if len(vals) == 0 {
        return 0
    }
    sum := 0.0
    for _, v := range vals {
        sum += v
    }
    return sum / float64(len(vals))
}

func topN(all []*Candidate, n int) []*Candidate {
    if n <= 0 || len(all) == 0 {
        return nil
    }
    if n > len(all) {
        n = len(all)
    }
    out := make([]*Candidate, 0, n)
    picked := make([]bool, len(all))
    for len(out) < n {
        best := -1
        bestScore := math.Inf(-1)
        for i, c := range all {
            if picked[i] {
                continue
            }
            if c.AggScore > bestScore {
                best = i
                bestScore = c.AggScore
            }
        }
        if best == -1 {
            break
        }
        picked[best] = true
        out = append(out, all[best])
    }
    return out
}

func genID() string {
    b := make([]byte, 6)
    _, _ = crand.Read(b)
    return hex.EncodeToString(b)
}
