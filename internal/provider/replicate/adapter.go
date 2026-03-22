package replicate

import (
    "encoding/json"
)

type Adapter struct {}

func New() *Adapter {
    return &Adapter{}
}

func (a *Adapter) Name() string { return "replicate" }

func (a *Adapter) Hosts() []string { return []string{"api.replicate.com"} }

func (a *Adapter) ExtractSystemPrompt(contentType string, body []byte) (string, error) {
    if len(body) == 0 {
        return "", nil
    }
    var payload map[string]any
    if err := json.Unmarshal(body, &payload); err != nil {
        return "", err
    }
    input, ok := payload["input"].(map[string]any)
    if !ok {
        return "", nil
    }
    if prompt, ok := input["prompt"].(string); ok {
        return prompt, nil
    }
    return "", nil
}

func (a *Adapter) InjectSystemPrompt(contentType string, body []byte, newPrompt string) ([]byte, error) {
    if len(body) == 0 {
        return body, nil
    }
    var payload map[string]any
    if err := json.Unmarshal(body, &payload); err != nil {
        return body, err
    }
    input, ok := payload["input"].(map[string]any)
    if !ok {
        input = map[string]any{}
        payload["input"] = input
    }
    input["prompt"] = newPrompt
    return json.Marshal(payload)
}
