package openaichat

import (
	"encoding/json"
	"strings"
)

type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string { return "openai-chat" }

func (a *Adapter) Hosts() []string { return nil }

func (a *Adapter) ExtractSystemPrompt(contentType string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return "", nil
	}
	first, ok := msgs[0].(map[string]any)
	if !ok {
		return "", nil
	}
	if content, ok := first["content"].(string); ok {
		return content, nil
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
	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return body, nil
	}
	first, ok := msgs[0].(map[string]any)
	if !ok {
		return body, nil
	}
	first["content"] = newPrompt
	msgs[0] = first
	payload["messages"] = msgs
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
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(choices))
	for _, item := range choices {
		choice, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if msg, ok := choice["message"].(map[string]any); ok {
			if content, ok := msg["content"].(string); ok {
				parts = append(parts, content)
				continue
			}
		}
		if text, ok := choice["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ""), nil
}
