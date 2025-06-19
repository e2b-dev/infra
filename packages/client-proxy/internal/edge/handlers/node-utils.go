package handlers

import (
	"fmt"
	"net/http"

	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
)

func (a *APIStore) getOrchestratorNode(orchestratorId string) (*e2borchestrators.OrchestratorNode, *APIUserFacingError) {
	orchestrator, ok := a.orchestratorPool.GetOrchestrator(orchestratorId)
	if !ok {
		return nil, &APIUserFacingError{
			internalError:      fmt.Errorf("orchestrator not found: %s", orchestratorId),
			prettyErrorCode:    http.StatusBadRequest,
			prettyErrorMessage: "Orchestrator not found",
		}
	}

	if orchestrator.Status != e2borchestrators.OrchestratorStatusHealthy {
		return nil, &APIUserFacingError{
			internalError:      fmt.Errorf("orchestrator is not ready for build placement"),
			prettyErrorCode:    http.StatusBadRequest,
			prettyErrorMessage: "Orchestrator is not ready for build placement",
		}
	}

	return orchestrator, nil
}
