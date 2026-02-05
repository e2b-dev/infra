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
	Version string                `json:"version"`
	Network *SandboxNetworkConfig `json:"network,omitempty"`
}

// BuildStatus defines the status values for env builds.
//
// Multiple legacy statuses map to the same semantic meaning.
// All code should use the Is*() helpers instead of comparing to specific values.
// The target unified statuses are: pending, in_progress, ready, failed.
type BuildStatus string

const (
	// Target unified statuses (use these for new code)
	BuildStatusPending    BuildStatus = "pending"
	BuildStatusInProgress BuildStatus = "in_progress"
	BuildStatusReady      BuildStatus = "ready"
	BuildStatusFailed     BuildStatus = "failed"

	// Legacy statuses (kept for backward compatibility during migration)
	BuildStatusWaiting      BuildStatus = "waiting"
	BuildStatusBuilding     BuildStatus = "building"
	BuildStatusUploaded     BuildStatus = "uploaded"
	BuildStatusSnapshotting BuildStatus = "snapshotting"
	BuildStatusSuccess      BuildStatus = "success"
)

// IsPending returns true if the build is waiting to start.
func (s BuildStatus) IsPending() bool {
	return s == BuildStatusPending || s == BuildStatusWaiting
}

// IsInProgress returns true if the build is currently running.
func (s BuildStatus) IsInProgress() bool {
	return s == BuildStatusInProgress || s == BuildStatusBuilding || s == BuildStatusSnapshotting
}

// IsReady returns true if the build completed successfully.
func (s BuildStatus) IsReady() bool {
	return s == BuildStatusReady || s == BuildStatusUploaded || s == BuildStatusSuccess
}

// IsFailed returns true if the build failed.
func (s BuildStatus) IsFailed() bool {
	return s == BuildStatusFailed
}
