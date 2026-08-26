package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetClustersClusterIDRigs(c *gin.Context, clusterID api.ClusterID) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "cluster-rigs-list")
	defer span.End()

	resources, ok := a.clusterResources(c, clusterID)
	if !ok {
		return
	}

	rigs, apiErr := resources.GetRigs(ctx)
	if apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error listing rigs", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, rigs)
}

func (a *APIStore) PutClustersClusterIDRigsRigIDCapacity(c *gin.Context, clusterID api.ClusterID, rigID api.RigID) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "cluster-rig-capacity")
	defer span.End()

	body, err := ginutils.ParseBody[api.PutClustersClusterIDRigsRigIDCapacityJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when parsing request: "+err.Error())
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	resources, ok := a.clusterResources(c, clusterID)
	if !ok {
		return
	}

	if apiErr := resources.SetRigCapacity(ctx, rigID, body.Desired); apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error setting rig capacity", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusAccepted)
}

func (a *APIStore) GetClustersClusterIDRigsRigIDInstances(c *gin.Context, clusterID api.ClusterID, rigID api.RigID) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "cluster-rig-instances")
	defer span.End()

	resources, ok := a.clusterResources(c, clusterID)
	if !ok {
		return
	}

	instances, apiErr := resources.GetRigInstances(ctx, rigID)
	if apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error listing rig instances", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, instances)
}

func (a *APIStore) GetClustersClusterIDRigsRigIDErrors(c *gin.Context, clusterID api.ClusterID, rigID api.RigID, params api.GetClustersClusterIDRigsRigIDErrorsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "cluster-rig-errors")
	defer span.End()

	resources, ok := a.clusterResources(c, clusterID)
	if !ok {
		return
	}

	rigErrors, apiErr := resources.GetRigErrors(ctx, rigID, params.Limit)
	if apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error listing rig errors", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, rigErrors)
}

func (a *APIStore) DeleteClustersClusterIDRigsInstancesInstanceID(c *gin.Context, clusterID api.ClusterID, instanceID string, params api.DeleteClustersClusterIDRigsInstancesInstanceIDParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "cluster-rig-instance-terminate")
	defer span.End()

	resources, ok := a.clusterResources(c, clusterID)
	if !ok {
		return
	}

	if apiErr := resources.TerminateRigInstance(ctx, instanceID, params.DecrementDesired); apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error terminating rig instance", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusAccepted)
}

// clusterResources answers 404 when the cluster is unknown.
func (a *APIStore) clusterResources(c *gin.Context, clusterID api.ClusterID) (clusters.ClusterResource, bool) {
	cluster, found := a.clusters.GetClusterById(clusterID)
	if !found {
		a.sendAPIStoreError(c, http.StatusNotFound, "Cluster not found")

		return nil, false
	}

	return cluster.GetResources(), true
}
