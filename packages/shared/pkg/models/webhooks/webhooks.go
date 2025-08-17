package webhooks

type SandboxWebhooks struct {
	SandboxID string   `json:"sandboxID"`
	Events    []string `json:"events"`
	URL       string   `json:"url"`
}
