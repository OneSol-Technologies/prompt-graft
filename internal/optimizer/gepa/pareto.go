package gepa

import "math/rand"

func ParetoFrontier(pool []*Candidate) []*Candidate {
    frontier := make([]*Candidate, 0)
    for _, c := range pool {
        dominated := false
        for _, other := range pool {
            if other == c {
                continue
            }
            if dominates(other.Scores, c.Scores) {
                dominated = true
                break
            }
        }
        if !dominated {
            frontier = append(frontier, c)
        }
    }
    return frontier
}

func dominates(a, b []float64) bool {
    strictlyBetter := false
    for i := range a {
        if i >= len(b) {
            return false
        }
        if a[i] < b[i] {
            return false
        }
        if a[i] > b[i] {
            strictlyBetter = true
        }
    }
    return strictlyBetter
}

func SampleFromFrontier(frontier []*Candidate, weights map[string]float64, rng *rand.Rand) *Candidate {
    if len(frontier) == 0 {
        return nil
    }
    if len(frontier) == 1 {
        return frontier[0]
    }
    total := 0.0
    for _, c := range frontier {
        total += weights[c.ID] + 1.0
    }
    r := rng.Float64() * total
    cumulative := 0.0
    for _, c := range frontier {
        cumulative += weights[c.ID] + 1.0
        if r <= cumulative {
            return c
        }
    }
    return frontier[len(frontier)-1]
}
