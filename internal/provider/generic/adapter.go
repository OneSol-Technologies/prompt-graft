package generic

type Adapter struct {}

func New() *Adapter {
    return &Adapter{}
}

func (a *Adapter) Name() string { return "generic" }

func (a *Adapter) Hosts() []string { return nil }

func (a *Adapter) ExtractSystemPrompt(contentType string, body []byte) (string, error) {
    return "", nil
}

func (a *Adapter) InjectSystemPrompt(contentType string, body []byte, newPrompt string) ([]byte, error) {
    return body, nil
}
