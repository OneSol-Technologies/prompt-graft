package optimizer

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"promptguru/internal/config"
	"promptguru/internal/logging"
	"promptguru/internal/optimizer/gepa"
	"promptguru/internal/store"
)

type Driver struct {
	store store.Store
	cfg   *config.Config
	llm   gepa.LLMClient
	log   *logging.Logger
}

func NewDriver(st store.Store, cfg *config.Config, llm gepa.LLMClient, log *logging.Logger) *Driver {
	return &Driver{store: st, cfg: cfg, llm: llm, log: log}
}

func (d *Driver) RunOnce(ctx context.Context) {
	refs, err := d.store.ReadySessions(ctx, d.cfg.MinSamples, d.cfg.OptimizeEvery)

	if err != nil {
		d.log.Warnf("ready sessions scan failed: %v", err)
		return
	}
	if len(refs) == 0 {
		d.log.Debugf("optimizer: no sessions ready yet (need at least %d rated requests per session)", d.cfg.MinSamples)
		return
	}
	d.log.Infof("optimizer: %d session(s) ready for optimization", len(refs))

	for _, ref := range refs {
		d.log.Infof("optimizer: processing session=%s keyHash=%s", ref.SessionID, ref.KeyHash)

		samples, err := d.store.LoadConversationSamples(ctx, ref.KeyHash, ref.SessionID, d.cfg.GEPATopN)
		if err != nil {
			d.log.Warnf("load samples failed: %v", err)
			continue
		}
		d.log.Infof("optimizer: session=%s loaded %d sample(s) (perVariant=%d)", ref.SessionID, len(samples), d.cfg.GEPATopN)
		for i, s := range samples {
			prompt := truncateString(s.Prompt, 400)
			response := truncateString(s.Response, 400)
			d.log.Debugf("optimizer: sample[%d] variant=%s score=%d prompt=%q response=%q", i, s.VariantID, s.Score, prompt, response)
		}
		if len(samples) == 0 {
			d.log.Infof("optimizer: session=%s — no conversation samples found (history not yet copied to pg?), skipping", ref.SessionID)
			continue
		}

		storedTemplate, _ := d.store.GetSessionPrompt(ctx, ref.KeyHash, ref.SessionID)
		inferredTemplate := deriveTemplateFromSamples(samples)
		d.log.Debugf("optimizer: stored template=%q", storedTemplate)
		d.log.Debugf("optimizer: inferred template=%q", inferredTemplate)
		if inferredTemplate != "" && inferredTemplate != storedTemplate {
			if err := d.store.SetSessionPrompt(ctx, ref.KeyHash, ref.SessionID, inferredTemplate, d.cfg.MaxVariantAge); err != nil {
				d.log.Warnf("optimizer: persist template failed: %v", err)
			} else {
				d.log.Debugf("optimizer: persisted template")
			}
		}

		if allPositive(samples) {
			d.log.Infof("optimizer: session=%s — all samples positive, marking optimized and skipping generation", ref.SessionID)
			_ = d.store.RollupConversationFeedback(ctx, ref.KeyHash, ref.SessionID)
			_ = d.store.MarkSessionOptimized(ctx, ref.KeyHash, ref.SessionID)
			continue
		}

		analysisPrompt := buildAnalysisPrompt(samples)
		d.log.Debugf("optimizer: analysis prompt length=%d", len(analysisPrompt))
		analysis, err := d.llm.Complete(ctx, "You are a prompt optimization expert.", analysisPrompt)
		if err != nil {
			d.log.Warnf("analysis call failed: %v", err)
			continue
		}
		d.log.Debugf("optimizer: analysis output=%q", analysis)

		generationPrompt := buildGenerationPrompt(analysis, d.cfg.GEPAOutputSize)
		d.log.Debugf("optimizer: generation prompt length=%d", len(generationPrompt))
		raw, err := d.llm.Complete(ctx, "You are a prompt optimization expert.", generationPrompt)
		if err != nil {
			d.log.Warnf("generation call failed: %v", err)
			continue
		}
		d.log.Debugf("optimizer: raw candidates=%q", raw)

		candidates := parseCandidates(raw)
		if len(candidates) == 0 {
			d.log.Warnf("optimizer: session=%s — no candidates produced from LLM output", ref.SessionID)
			continue
		}
		if len(candidates) > d.cfg.GEPAOutputSize {
			candidates = candidates[:d.cfg.GEPAOutputSize]
		}
		d.log.Infof("optimizer: session=%s — generated %d candidate(s)", ref.SessionID, len(candidates))
		for i, c := range candidates {
			d.log.Debugf("optimizer: candidate[%d]=%q", i, c)
		}

		d.store.MarkFeedbackUsed(ctx, ref.KeyHash, ref.SessionID, extractConversationIDs(samples))

		result := &gepa.Result{
			Candidates: make([]*gepa.Candidate, 0, len(candidates)),
		}
		for _, c := range candidates {
			result.Candidates = append(result.Candidates, &gepa.Candidate{Prompt: c})
		}
		if len(result.Candidates) > 0 {
			result.Best = result.Candidates[0]
		}

		if err := d.store.RollupConversationFeedback(ctx, ref.KeyHash, ref.SessionID); err != nil {
			d.log.Warnf("rollup failed: %v", err)
		}

		promoter := NewPromoter(d.store, d.cfg, d.log)
		if err := promoter.Promote(ctx, ref.KeyHash, ref.SessionID, result); err != nil {
			d.log.Warnf("optimizer: session=%s promote failed: %v", ref.SessionID, err)
			continue
		}
		d.log.Infof("optimizer: session=%s — optimization complete, promoted %d variant(s)", ref.SessionID, len(candidates))
		_ = d.store.MarkSessionOptimized(ctx, ref.KeyHash, ref.SessionID)
	}
}

func extractConversationIDs(samples []store.ConversationFeedback) []string {
	ids := make([]string, 0, len(samples))
	for _, s := range samples {
		ids = append(ids, s.ConversationID)
	}
	return ids
}

func allPositive(samples []store.ConversationFeedback) bool {
	if len(samples) == 0 {
		return false
	}
	for _, s := range samples {
		if s.Score != 1 {
			return false
		}
	}
	return true
}

func buildAnalysisPrompt(samples []store.ConversationFeedback) string {
	var b strings.Builder
	b.WriteString("You are analyzing prompt performance. Below are prompt/response pairs with scores (-1,0,1). For each, explain what worked and what failed. Provide a concise summary of improvements.\\n\\n")
	for i, s := range samples {
		b.WriteString("Sample ")
		b.WriteString(intToString(i + 1))
		b.WriteString("\\nPROMPT:\\n")
		b.WriteString(s.Prompt)
		b.WriteString("\\nRESPONSE:\\n")
		if s.Response != "" {
			b.WriteString(s.Response)
		} else {
			b.WriteString(s.Prompt)
		}
		b.WriteString("\\nSCORE: ")
		b.WriteString(intToString(s.Score))
		b.WriteString("\\n\\n")
	}
	b.WriteString("Summarize what works and what to improve. Return bullet points.")
	return b.String()
}

func buildGenerationPrompt(analysis string, outputSize int) string {
	var b strings.Builder
	b.WriteString("Using the analysis below, generate ")
	b.WriteString(intToString(outputSize))
	b.WriteString(" improved system prompts. Output ONLY a JSON array of strings.\\n\\nANALYSIS:\\n")
	b.WriteString(analysis)
	return b.String()
}

func parseCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var out []string
		if json.Unmarshal([]byte(raw), &out) == nil {
			return cleanCandidates(out)
		}
	}
	lines := strings.Split(raw, "\\n")
	return cleanCandidates(lines)
}

func cleanCandidates(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "-")
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func intToString(v int) string {
	return strconv.Itoa(v)
}

func deriveTemplateFromSamples(samples []store.ConversationFeedback) string {
	if len(samples) == 0 {
		return ""
	}
	template := samples[0].Prompt
	for i := 1; i < len(samples); i++ {
		template = commonPrefix(template, samples[i].Prompt)
		if template == "" {
			break
		}
	}
	return strings.TrimSpace(template)
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
	return a[:idx]
}

func truncateString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}
