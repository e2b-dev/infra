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

type PausedSandboxConfig struct {
	Version               string                `json:"version"`
	Network               *SandboxNetworkConfig `json:"network,omitempty"`
	SandboxResumesOn      *string               `json:"sandboxResumesOn,omitempty"`
	SandboxPausedSeconds  *int32                `json:"sandboxPausedSeconds,omitempty"`
	// Deprecated: keep for backward-compatible reads of existing snapshots.
	SandboxTimeoutSeconds *int32 `json:"sandboxTimeoutSeconds,omitempty"`
	// Deprecated: old snake_case key used in earlier snapshots.
	SandboxTimeoutSecondsSnake *int32 `json:"sandbox_timeout_seconds,omitempty"`
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
