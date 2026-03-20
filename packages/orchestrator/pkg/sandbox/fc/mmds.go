package fc

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxID  string `json:"instanceID"`
	TemplateID string `json:"envID"`

	LogsCollectorAddress string `json:"address"`
	AccessTokenHash      string `json:"accessTokenHash,omitempty"`
}
