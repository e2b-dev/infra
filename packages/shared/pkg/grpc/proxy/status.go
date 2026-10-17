package proxygrpc

const (
	// SandboxStillTransitioningMessage identifies the explicit transient autoresume wait failure.
	SandboxStillTransitioningMessage = "sandbox is still transitioning"

	// ResumeErrorDomain is the ErrorInfo domain under which the API attaches
	// its semantic error code to a resume failure's gRPC status, so the proxy
	// can tell a refusal no retry helps from a shortage that clears.
	ResumeErrorDomain = "api.e2b.dev"

	// ResumeErrorHTTPStatusKey is the ErrorInfo metadata key under which the
	// API attaches the HTTP status it answered the failure with, so the page
	// the proxy renders for a refusal carries the API's own status rather
	// than one read back off the gRPC code.
	ResumeErrorHTTPStatusKey = "http_status"

	// Placement error codes the API emits (also its REST error_code values).
	PlacementTimeout             = "sandbox_placement_timeout"
	PlacementCapacityUnavailable = "sandbox_capacity_unavailable"
	PlacementNoCompatibleNode    = "sandbox_no_compatible_node"
	PlacementFeatureUnsupported  = "sandbox_feature_unsupported"
	PlacementCreateFailed        = "sandbox_create_failed"
	PlacementCreateRefused       = "sandbox_create_refused"
)

// PermanentPlacementRefusal reports an error code for a placement no retry
// helps: no orchestrator in the cluster runs a version a requested feature
// needs, or a node refused the create for a reason the same on every node
// (a vcpu request out of bounds), both of which the API states as such. A
// capacity shortage clears on its own, and "no compatible node" also names
// a node pool that is only briefly draining or unhealthy, so both are
// retried.
func PermanentPlacementRefusal(code string) bool {
	return code == PlacementFeatureUnsupported || code == PlacementCreateRefused
}

// RetryablePlacementFailure reports an error code for a placement the API
// itself tells the client to retry: a shortage, a timeout or a create that
// failed on the node. The API sends the timeout as 504 and the failed create
// as 500, which alone read as terminal; the code says they are not.
func RetryablePlacementFailure(code string) bool {
	switch code {
	case PlacementTimeout, PlacementCapacityUnavailable, PlacementNoCompatibleNode, PlacementCreateFailed:
		return true
	default:
		return false
	}
}
