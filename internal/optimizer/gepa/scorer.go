package gepa

import (
    "context"
    "strings"
)

type ScoringFn func(ctx context.Context, prompt string, dataset Dataset) ([]float64, error)

func FeedbackScorer(dataset Dataset) ScoringFn {
    return func(ctx context.Context, prompt string, data Dataset) ([]float64, error) {
        scores := make([]float64, 0, len(data))
        for _, d := range data {
            if d.Rating != 0 {
                scores = append(scores, (d.Rating+1.0)/2.0)
                continue
            }
            scores = append(scores, similarityScore(prompt, d.Input))
        }
        return scores, nil
    }
}

func ExactMatchScorer(expectedOutput string) ScoringFn {
    return func(ctx context.Context, prompt string, data Dataset) ([]float64, error) {
        scores := make([]float64, 0, len(data))
        for _, d := range data {
            if strings.Contains(d.Output, expectedOutput) {
                scores = append(scores, 1.0)
            } else {
                scores = append(scores, 0.0)
            }
        }
        return scores, nil
    }
}

func similarityScore(prompt, input string) float64 {
    if prompt == "" || input == "" {
        return 0.5
    }
    promptTokens := tokenSet(prompt)
    inputTokens := tokenSet(input)
    if len(promptTokens) == 0 || len(inputTokens) == 0 {
        return 0.5
    }
    overlap := 0
    for tok := range promptTokens {
        if _, ok := inputTokens[tok]; ok {
            overlap++
        }
    }
    denom := len(promptTokens)
    if denom == 0 {
        return 0.5
    }
    score := float64(overlap) / float64(denom)
    if score < 0.1 {
        return 0.1
    }
    if score > 1 {
        return 1
    }
    return score
}

func tokenSet(s string) map[string]struct{} {
    parts := strings.Fields(strings.ToLower(s))
    out := make(map[string]struct{}, len(parts))
    for _, p := range parts {
        out[p] = struct{}{}
    }
    return out
}
