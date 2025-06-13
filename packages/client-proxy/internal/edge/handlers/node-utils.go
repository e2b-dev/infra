package handlers

import (
	"errors"
	"fmt"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	e2borchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"net/http"
	"slices"
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

func (a *APIStore) getTemplateManagerNode(orchestratorId string) (*e2borchestrators.OrchestratorNode, *APIUserFacingError) {
	orchestrator, err := a.getOrchestratorNode(orchestratorId)
	if err != nil {
		return nil, err
	}

	if !slices.Contains(
		orchestrator.Roles, e2borchestrator.ServiceInfoRole_TemplateManager) {
		return nil, &APIUserFacingError{
			internalError:      errors.New("orchestrator is not marked as template builder"),
			prettyErrorCode:    http.StatusBadRequest,
			prettyErrorMessage: "Orchestrator does not support template builds",
		}
	}

	return orchestrator, nil
}
