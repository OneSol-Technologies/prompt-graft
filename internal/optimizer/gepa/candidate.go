package gepa

import "sync"

type Candidate struct {
    ID         string
    Prompt     string
    ParentID   string
    Scores     []float64
    AggScore   float64
    Generation int
    ASI        string
}

type CandidatePool struct {
    candidates []*Candidate
    mu         sync.RWMutex
}

func (p *CandidatePool) Add(c *Candidate) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.candidates = append(p.candidates, c)
}

func (p *CandidatePool) All() []*Candidate {
    p.mu.RLock()
    defer p.mu.RUnlock()
    out := make([]*Candidate, len(p.candidates))
    copy(out, p.candidates)
    return out
}

func (p *CandidatePool) Best() *Candidate {
    p.mu.RLock()
    defer p.mu.RUnlock()
    var best *Candidate
    for _, c := range p.candidates {
        if best == nil || c.AggScore > best.AggScore {
            best = c
        }
    }
    return best
}
