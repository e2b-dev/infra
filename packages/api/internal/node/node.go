package node

import "github.com/google/uuid"

const UnknownNomadNodeShortID = "unknown"

type NodeInfo struct {
	// Deprecated: Used only for back compatibility with Nomad cluster deployments.
	NomadNodeShortID string

	ClusterID uuid.UUID
	NodeID    string
	IPAddress string
}
