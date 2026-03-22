package gepa

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"

    "promptguru/internal/config"
)

type LLMClient interface {
    Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

type NoopClient struct {}

func (n *NoopClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
    return "", nil
}

type ReplicateClient struct {
    BaseURL string
    APIToken string
    Model string

    ReasoningEffort string
    Verbosity string
    MaxCompletionTokens int
    Temperature float64
    TopP float64
    PresencePenalty float64
    FrequencyPenalty float64
    Timeout time.Duration

    httpClient *http.Client
}

type replicatePredictionRequest struct {
    Version string `json:"version"`
    Input   map[string]any `json:"input"`
}

type replicatePredictionResponse struct {
    ID     string `json:"id"`
    Status string `json:"status"`
    Output any    `json:"output"`
    Error  any    `json:"error"`
}

func (r *ReplicateClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
    if r.APIToken == "" {
        return "", errors.New("replicate api token is required")
    }
    if r.Model == "" {
        return "", errors.New("replicate model is required")
    }

    payload := replicatePredictionRequest{
        Version: r.Model,
        Input: map[string]any{
            "system_prompt": systemPrompt,
            "prompt": userPrompt,
            "max_completion_tokens": r.MaxCompletionTokens,
            "temperature": r.Temperature,
            "top_p": r.TopP,
            "presence_penalty": r.PresencePenalty,
            "frequency_penalty": r.FrequencyPenalty,
            "reasoning_effort": r.ReasoningEffort,
            "verbosity": r.Verbosity,
        },
    }

    body, _ := json.Marshal(payload)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.BaseURL, "/")+"/v1/predictions", bytes.NewReader(body))
    if err != nil {
        return "", err
    }
    req.Header.Set("Authorization", "Bearer "+r.APIToken)
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Prefer", "wait=60")

    resp, err := r.client().Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    prediction, err := decodePrediction(resp.Body)
    if err != nil {
        return "", err
    }

    if prediction.Status == "succeeded" {
        return normalizeOutput(prediction.Output), nil
    }
    if prediction.Status == "failed" {
        return "", fmt.Errorf("replicate failed: %v", prediction.Error)
    }

    return r.poll(ctx, prediction.ID)
}

func (r *ReplicateClient) poll(ctx context.Context, id string) (string, error) {
    deadline := time.Now().Add(r.Timeout)
    for {
        if time.Now().After(deadline) {
            return "", errors.New("replicate prediction timeout")
        }
        req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.BaseURL, "/")+"/v1/predictions/"+id, nil)
        if err != nil {
            return "", err
        }
        req.Header.Set("Authorization", "Bearer "+r.APIToken)
        resp, err := r.client().Do(req)
        if err != nil {
            return "", err
        }
        prediction, err := decodePrediction(resp.Body)
        resp.Body.Close()
        if err != nil {
            return "", err
        }
        switch prediction.Status {
        case "succeeded":
            return normalizeOutput(prediction.Output), nil
        case "failed":
            return "", fmt.Errorf("replicate failed: %v", prediction.Error)
        }
        time.Sleep(2 * time.Second)
    }
}

func (r *ReplicateClient) client() *http.Client {
    if r.httpClient != nil {
        return r.httpClient
    }
    r.httpClient = &http.Client{Timeout: r.Timeout}
    return r.httpClient
}

func decodePrediction(reader io.Reader) (*replicatePredictionResponse, error) {
    var resp replicatePredictionResponse
    if err := json.NewDecoder(reader).Decode(&resp); err != nil {
        return nil, err
    }
    return &resp, nil
}

func normalizeOutput(out any) string {
    switch v := out.(type) {
    case string:
        return v
    case []any:
        parts := make([]string, 0, len(v))
        for _, item := range v {
            if s, ok := item.(string); ok {
                parts = append(parts, s)
            }
        }
        return strings.Join(parts, "")
    default:
        raw, _ := json.Marshal(out)
        return string(raw)
    }
}

type OpenAIClient struct {
    BaseURL string
    APIKey  string
    Model   string
}

func (o *OpenAIClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
    return "", errors.New("OpenAI client not implemented in skeleton")
}

type AnthropicClient struct {
    APIKey string
    Model  string
}

func (a *AnthropicClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
    return "", errors.New("Anthropic client not implemented in skeleton")
}

func NewLLMClient(cfg *config.Config) LLMClient {
    if cfg.OptimizerLLMProvider == "replicate" {
        return &ReplicateClient{
            BaseURL: cfg.ReplicateBaseURL,
            APIToken: firstNonEmpty(cfg.ReplicateAPIToken, cfg.OptimizerLLMAPIKey),
            Model: firstNonEmpty(cfg.ReplicateModel, cfg.OptimizerLLMModel),
            ReasoningEffort: cfg.ReplicateReasoningEffort,
            Verbosity: cfg.ReplicateVerbosity,
            MaxCompletionTokens: cfg.ReplicateMaxCompletionTokens,
            Temperature: cfg.ReplicateTemperature,
            TopP: cfg.ReplicateTopP,
            PresencePenalty: cfg.ReplicatePresencePenalty,
            FrequencyPenalty: cfg.ReplicateFrequencyPenalty,
            Timeout: cfg.ReplicateTimeout,
        }
    }

    if cfg.OptimizerLLMAPIKey == "" {
        return &NoopClient{}
    }
    if cfg.OptimizerLLMProvider == "anthropic" {
        return &AnthropicClient{APIKey: cfg.OptimizerLLMAPIKey, Model: cfg.OptimizerLLMModel}
    }
    return &OpenAIClient{APIKey: cfg.OptimizerLLMAPIKey, Model: cfg.OptimizerLLMModel}
}

func firstNonEmpty(values ...string) string {
    for _, v := range values {
        if v != "" {
            return v
        }
    }
    return ""
}
