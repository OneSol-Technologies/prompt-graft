package gepa

import (
    "math/rand"
)

// DataPoint is one training example.
type DataPoint struct {
    Input     string
    Output    string
    Rating    float64
    ASI       string
    VariantID string
}

type Dataset []DataPoint

func (d Dataset) Split(paretoFraction float64, seed int64) (Dataset, Dataset) {
    if paretoFraction <= 0 || paretoFraction >= 1 || len(d) == 0 {
        return d, nil
    }
    rng := rand.New(rand.NewSource(seed))
    shuffled := make(Dataset, len(d))
    copy(shuffled, d)
    rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

    cutoff := int(float64(len(shuffled)) * paretoFraction)
    if cutoff < 1 {
        cutoff = 1
    }
    return shuffled[:cutoff], shuffled[cutoff:]
}

func (d Dataset) Minibatch(n int, rng *rand.Rand) Dataset {
    if n <= 0 || len(d) == 0 {
        return nil
    }
    if n >= len(d) {
        return d
    }
    indices := rng.Perm(len(d))[:n]
    batch := make(Dataset, 0, n)
    for _, idx := range indices {
        batch = append(batch, d[idx])
    }
    return batch
}
