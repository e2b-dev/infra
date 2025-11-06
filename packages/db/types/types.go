package types

type JSONBStringMap map[string]string

type BuildReason struct {
	// Message with the status reason, currently reporting only for error status
	Message string `json:"message"`

	// Step that failed
	Step *string `json:"step,omitempty"`
}

type SandboxFirewallEgressConfig struct {
	AllowedCidrs []string `json:"allowedCidrs,omitempty"`
	BlockedCidrs []string `json:"blockedCidrs,omitempty"`
}

type SandboxFirewallConfig struct {
	Egress *SandboxFirewallEgressConfig `json:"egress,omitempty"`
}
