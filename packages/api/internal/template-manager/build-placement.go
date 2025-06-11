package template_manager

import (
	"context"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
)

type BuildPlacement interface {
	StartBuild(ctx context.Context, buildId string, templateId string) error
	GetStatus(ctx context.Context, buildId string, templateId string) (string, error)
}

// ---
// local (template manager direct calling)
// ---

type LocalBuildPlacement struct {
}

func NewLocalBuildPlacement(*GRPCClient) *LocalBuildPlacement {
	return &LocalBuildPlacement{}
}

func (l *LocalBuildPlacement) GetStatus(ctx context.Context, buildId string, templateId string) (string, error) {
	return "local build status", nil
}

func (l *LocalBuildPlacement) StartBuild(ctx context.Context, buildId string, templateId string) error {
	// Logic to start a build locally
	// This is a placeholder implementation
	return nil
}

// ---
// clustered
// ---

type ClusteredBuildPlacement struct {
}

func NewClusteredBuildPlacement(cluster *edge.Cluster, clusterNodeId string) *ClusteredBuildPlacement {

	


	return &ClusteredBuildPlacement{}
}

func (c *ClusteredBuildPlacement) GetStatus(ctx context.Context, buildId string, templateId string) (string, error) {
	return "clustered build status", nil
}

func (c *ClusteredBuildPlacement) StartBuild(ctx context.Context, buildId string, templateId string) error {
	// Logic to start a build in a clustered environment
	// This is a placeholder implementation
	return nil
}
