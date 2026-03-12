package v2

// PublishProfile defines how a sandbox exposes services to the outside world.
// This is a stub data model for the PoC — behavior will be added later.
type PublishProfile struct {
	ID        string
	OwnerType string // "customer" or "sandbox"
	OwnerID   string
	Region    string

	// Mode selects the publish mechanism.
	// "proxy"  — traffic routed through client-proxy (current model)
	// "direct" — direct port mapping (future)
	Mode string

	// Ports maps container port → public port (for future direct mode).
	Ports map[int]int
}

// DefaultPublishProfile returns a proxy-mode profile (current E2B behavior).
func DefaultPublishProfile() *PublishProfile {
	return &PublishProfile{
		ID:        "default",
		OwnerType: "system",
		OwnerID:   "default",
		Region:    "local",
		Mode:      "proxy",
		Ports:     make(map[int]int),
	}
}
