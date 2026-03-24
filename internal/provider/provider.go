package provider

// Provider is implemented once per upstream AI platform.
type Provider interface {
	Name() string
	Hosts() []string
	ExtractSystemPrompt(contentType string, body []byte) (string, error)
	InjectSystemPrompt(contentType string, body []byte, newPrompt string) ([]byte, error)
	ExtractOutputText(contentType string, body []byte) (string, error)
}
