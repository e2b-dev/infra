package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementDeleteProject is declared by the contract and answers 501, which is
// a decision rather than a gap.
//
// envs, snapshots and volumes reference teams with ON DELETE NO ACTION and
// template deletion only stamps deleted_at, so any project that ever built one
// pins its team row. Releasing it means killing sandboxes, cancelling builds and
// reclaiming stored artifacts, all of which need the api service's orchestrator
// connections that this process does not have.
//
// Someone has to choose between a gateway to those teardown routes, moving the
// operation there, and asynchronous reconciliation. Until then, control planes
// do not delete projects.
func (s *APIStore) ManagementDeleteProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}
