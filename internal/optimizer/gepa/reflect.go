package gepa

import (
    "context"
    "fmt"
    "strings"
)

type ReflectionResult struct {
    ASI         string
    Suggestions string
}

type Reflector struct {
    llm LLMClient
}

func NewReflector(client LLMClient) *Reflector {
    return &Reflector{llm: client}
}

func (r *Reflector) Reflect(ctx context.Context, parent *Candidate, minibatch Dataset, scores []float64) (ReflectionResult, error) {
    if r.llm == nil {
        return ReflectionResult{}, nil
    }
    systemPrompt := "You are a prompt optimization expert."
    userPrompt := buildReflectionPrompt(parent.Prompt, minibatch, scores)
    raw, err := r.llm.Complete(ctx, systemPrompt, userPrompt)
    if err != nil {
        return ReflectionResult{}, err
    }
    asi, suggestions := splitReflection(raw)
    return ReflectionResult{ASI: asi, Suggestions: suggestions}, nil
}

func (r *Reflector) Mutate(ctx context.Context, parent *Candidate, result ReflectionResult) (string, error) {
    if r.llm == nil {
        return parent.Prompt, nil
    }
    systemPrompt := "You are a prompt optimization expert."
    userPrompt := buildMutationPrompt(parent.Prompt, result.ASI, result.Suggestions)
    raw, err := r.llm.Complete(ctx, systemPrompt, userPrompt)
    if err != nil {
        return parent.Prompt, err
    }
    trimmed := strings.TrimSpace(raw)
    if trimmed == "" {
        return parent.Prompt, nil
    }
    return trimmed, nil
}

func (r *Reflector) Crossover(ctx context.Context, a *Candidate, b *Candidate) (string, error) {
    if r.llm == nil {
        return a.Prompt, nil
    }
    systemPrompt := "You are a prompt optimization expert."
    userPrompt := buildCrossoverPrompt(a.Prompt, b.Prompt)
    raw, err := r.llm.Complete(ctx, systemPrompt, userPrompt)
    if err != nil {
        return a.Prompt, err
    }
    trimmed := strings.TrimSpace(raw)
    if trimmed == "" {
        return a.Prompt, nil
    }
    return trimmed, nil
}

func buildReflectionPrompt(parent string, minibatch Dataset, scores []float64) string {
    var b strings.Builder
    b.WriteString("SYSTEM PROMPT:\n")
    b.WriteString(parent)
    b.WriteString("\n\nEVALUATION RESULTS:\n")
    for i, item := range minibatch {
        b.WriteString(fmt.Sprintf("Example %d:\n", i+1))
        b.WriteString("Input:\n")
        b.WriteString(item.Input)
        b.WriteString("\nOutput:\n")
        b.WriteString(item.Output)
        if i < len(scores) {
            b.WriteString(fmt.Sprintf("\nScore: %.3f\n", scores[i]))
        } else {
            b.WriteString("\nScore: 0.000\n")
        }
        if item.ASI != "" {
            b.WriteString("UserFeedback:\n")
            b.WriteString(item.ASI)
            b.WriteString("\n")
        }
        b.WriteString("\n")
    }
    b.WriteString("Analyze why the prompt is failing on the low-scoring examples. Identify specific patterns in the failures. Provide a concise natural-language diagnosis (ASI) and concrete suggestions for improving the prompt. Be specific and cite failure modes.\n")
    b.WriteString("Return in two sections labeled ASI: and SUGGESTIONS:.\n")
    return b.String()
}

func buildMutationPrompt(parent, asi, suggestions string) string {
    var b strings.Builder
    b.WriteString("CURRENT SYSTEM PROMPT:\n")
    b.WriteString(parent)
    b.WriteString("\n\nDIAGNOSIS (what is going wrong and why):\n")
    b.WriteString(asi)
    b.WriteString("\n\nSUGGESTIONS:\n")
    b.WriteString(suggestions)
    b.WriteString("\n\nRewrite the system prompt to address these failure modes. Preserve everything that is working well. Output only the new system prompt text, nothing else.\n")
    return b.String()
}

func buildCrossoverPrompt(a, b string) string {
    var sb strings.Builder
    sb.WriteString("PROMPT A:\n")
    sb.WriteString(a)
    sb.WriteString("\n\nPROMPT B:\n")
    sb.WriteString(b)
    sb.WriteString("\n\nCombine the strengths of both prompts into a single, unified system prompt. Output only the new system prompt text, nothing else.\n")
    return sb.String()
}

func splitReflection(raw string) (string, string) {
    text := strings.TrimSpace(raw)
    if text == "" {
        return "", ""
    }
    lower := strings.ToLower(text)
    asiIdx := strings.Index(lower, "asi:")
    sugIdx := strings.Index(lower, "suggestions:")
    if asiIdx >= 0 && sugIdx > asiIdx {
        asi := strings.TrimSpace(text[asiIdx+4 : sugIdx])
        sugg := strings.TrimSpace(text[sugIdx+12:])
        return asi, sugg
    }
    return text, ""
}
