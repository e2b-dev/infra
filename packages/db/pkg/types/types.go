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
	Policy SandboxAutoResumePolicy `json:"policy"`
}

type PausedSandboxConfig struct {
	Version    string                   `json:"version"`
	Network    *SandboxNetworkConfig    `json:"network,omitempty"`
	AutoResume *SandboxAutoResumeConfig `json:"autoResume,omitempty"`
}

// BuildStatus represents the raw status value written to the env_builds table.
// Use BuildStatusGroup for read-side comparisons.
type BuildStatus string

const (
	BuildStatusPending      BuildStatus = "pending"
	BuildStatusWaiting      BuildStatus = "waiting"
	BuildStatusBuilding     BuildStatus = "building"
	BuildStatusSnapshotting BuildStatus = "snapshotting"
	BuildStatusUploaded     BuildStatus = "uploaded"
	BuildStatusSuccess      BuildStatus = "success"
	BuildStatusFailed       BuildStatus = "failed"
)

// BuildStatusGroup represents the normalized status from the status_group
// computed column. Use this type for all read-side comparisons.
type BuildStatusGroup string

const (
	BuildStatusGroupPending    BuildStatusGroup = "pending"
	BuildStatusGroupInProgress BuildStatusGroup = "in_progress"
	BuildStatusGroupReady      BuildStatusGroup = "ready"
	BuildStatusGroupFailed     BuildStatusGroup = "failed"
)
