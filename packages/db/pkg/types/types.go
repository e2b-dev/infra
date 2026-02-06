package types

type JSONBStringMap map[string]string

type BuildReason struct {
	// Message with the status reason, currently reporting only for error status
	Message string `json:"message"`

	// Step that failed
	Step *string `json:"step,omitempty"`
}

const PausedSandboxConfigVersion = "v1"

type SandboxNetworkEgressConfig struct {
	AllowedAddresses []string `json:"allowedAddresses,omitempty"`
	DeniedAddresses  []string `json:"deniedAddresses,omitempty"`
}

const AllowPublicAccessDefault = true

type SandboxNetworkIngressConfig struct {
	AllowPublicAccess *bool   `json:"allowPublicAccess,omitempty"`
	MaskRequestHost   *string `json:"maskRequestHost,omitempty"`
}

type SandboxNetworkConfig struct {
	Egress  *SandboxNetworkEgressConfig  `json:"egress,omitempty"`
	Ingress *SandboxNetworkIngressConfig `json:"ingress,omitempty"`
}

type SandboxAutoResumePolicy string

const (
	SandboxAutoResumeAny SandboxAutoResumePolicy = "any"
	SandboxAutoResumeOff SandboxAutoResumePolicy = "off"
)

type SandboxAutoResumeConfig struct {
	// Policy is optional; unset means "off".
	Policy *SandboxAutoResumePolicy `json:"policy,omitempty"`
}

type PausedSandboxConfig struct {
	Version    string                   `json:"version"`
	Network    *SandboxNetworkConfig    `json:"network,omitempty"`
	AutoResume *SandboxAutoResumeConfig `json:"autoResume,omitempty"`
}

// Status defines the type for the "status" enum field.
type BuildStatus string

// Status values.
const (
	BuildStatusWaiting      BuildStatus = "waiting"
	BuildStatusBuilding     BuildStatus = "building"
	BuildStatusSnapshotting BuildStatus = "snapshotting"
	BuildStatusFailed       BuildStatus = "failed"
	BuildStatusSuccess      BuildStatus = "success"
	BuildStatusUploaded     BuildStatus = "uploaded"
)
