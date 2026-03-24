package replicate

import (
	"encoding/json"
	"strings"
)

type Adapter struct{}

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

func (a *Adapter) ExtractOutputText(contentType string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	out, ok := payload["output"]
	if !ok {
		return "", nil
	}
	switch v := out.(type) {
	case string:
		return v, nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ""), nil
	default:
		return "", nil
	}
}
