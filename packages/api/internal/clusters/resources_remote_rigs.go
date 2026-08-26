package clusters

import (
	"context"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (r *ClusterResourceProviderImpl) GetRigs(ctx context.Context) ([]api.Rig, *api.APIError) {
	res, err := r.client.V1RigsWithResponse(ctx)
	if err != nil {
		return nil, edgeRequestError(err, "Failed to fetch rigs")
	}

	// Upstream declares only 401/500 here: no rig management means 200 with an
	// empty list, not 501.
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, handleEdgeRigErrorResponse(res.StatusCode(), nil, res.JSON401, nil, nil, res.JSON500, nil, "Failed to fetch rigs")
	}

	items := make([]api.Rig, 0, len(res.JSON200.Items))
	for _, rig := range res.JSON200.Items {
		items = append(items, api.Rig{
			Id:              rig.Id,
			Provider:        rig.Provider,
			ResourceID:      rig.ResourceId,
			CapacityDesired: rig.CapacityDesired,
			CapacityCurrent: rig.CapacityCurrent,
			CapacityMin:     rig.CapacityMin,
			CapacityMax:     rig.CapacityMax,
		})
	}

	return items, nil
}

func (r *ClusterResourceProviderImpl) SetRigCapacity(ctx context.Context, rigID string, desired int32) *api.APIError {
	res, err := r.client.V1RigsRigIDCapacityWithResponse(ctx, rigID, edgeapi.V1RigsRigIDCapacityJSONRequestBody{Desired: desired})
	if err != nil {
		return edgeRequestError(err, "Failed to set rig capacity")
	}

	if res.StatusCode() != http.StatusAccepted {
		return handleEdgeRigErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON404, res.JSON409, res.JSON500, res.JSON501, "Failed to set rig capacity")
	}

	return nil
}

func (r *ClusterResourceProviderImpl) GetRigInstances(ctx context.Context, rigID string) ([]api.RigInstance, *api.APIError) {
	res, err := r.client.V1RigsRigIDInstancesWithResponse(ctx, rigID)
	if err != nil {
		return nil, edgeRequestError(err, "Failed to fetch rig instances")
	}

	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, handleEdgeRigErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON404, nil, res.JSON500, res.JSON501, "Failed to fetch rig instances")
	}

	items := make([]api.RigInstance, 0, len(res.JSON200.Items))
	for _, instance := range res.JSON200.Items {
		items = append(items, api.RigInstance{
			Id:            instance.Id,
			CreatedAt:     instance.CreatedAt,
			Transitioning: instance.Transitioning,
			Terminating:   instance.Terminating,
		})
	}

	return items, nil
}

func (r *ClusterResourceProviderImpl) GetRigErrors(ctx context.Context, rigID string, limit *int32) ([]api.RigError, *api.APIError) {
	res, err := r.client.V1RigsRigIDErrorsWithResponse(ctx, rigID, &edgeapi.V1RigsRigIDErrorsParams{Limit: limit})
	if err != nil {
		return nil, edgeRequestError(err, "Failed to fetch rig errors")
	}

	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, handleEdgeRigErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON404, nil, res.JSON500, res.JSON501, "Failed to fetch rig errors")
	}

	raw := *res.JSON200
	items := make([]api.RigError, 0, len(raw))
	for _, rigErr := range raw {
		items = append(items, api.RigError{
			Timestamp: rigErr.Timestamp,
			Code:      rigErr.Code,
			Message:   rigErr.Message,
			Instance:  rigErr.Instance,
			Action:    rigErr.Action,
		})
	}

	return items, nil
}

func (r *ClusterResourceProviderImpl) TerminateRigInstance(ctx context.Context, instanceID string, decrementDesired bool) *api.APIError {
	res, err := r.client.V1RigsInstancesInstanceIDWithResponse(ctx, instanceID, &edgeapi.V1RigsInstancesInstanceIDParams{DecrementDesired: decrementDesired})
	if err != nil {
		return edgeRequestError(err, "Failed to terminate rig instance")
	}

	if res.StatusCode() != http.StatusAccepted {
		return handleEdgeRigErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON404, res.JSON409, res.JSON500, res.JSON501, "Failed to terminate rig instance")
	}

	return nil
}

func edgeRequestError(err error, clientMsg string) *api.APIError {
	return &api.APIError{
		Err:       err,
		ClientMsg: clientMsg,
		Code:      http.StatusInternalServerError,
	}
}

// handleEdgeRigErrorResponse forwards edge's status instead of collapsing it
// into a 500 like handleEdgeErrorResponse does: 404 and 501 tell a caller
// iterating clusters to skip this one, and 409 to retry.
func handleEdgeRigErrorResponse(statusCode int, json400, json401, json404, json409, json500, json501 *edgeapi.Error, clientMsg string) *api.APIError {
	forward := func(body *edgeapi.Error, code int) *api.APIError {
		msg := clientMsg
		if body != nil && body.Message != "" {
			msg = body.Message
		}

		return &api.APIError{
			Err:       fmt.Errorf("edge rig error: %s (http code %d)", msg, code),
			ClientMsg: msg,
			Code:      code,
		}
	}

	switch statusCode {
	case http.StatusBadRequest:
		return forward(json400, http.StatusBadRequest)
	case http.StatusNotFound:
		return forward(json404, http.StatusNotFound)
	case http.StatusConflict:
		return forward(json409, http.StatusConflict)
	case http.StatusNotImplemented:
		return forward(json501, http.StatusNotImplemented)
	}

	// 401 means this API is misconfigured against the cluster, not that the
	// caller is unauthorized. Never forward it as a 401.
	errMsg := "Unexpected error occurred"
	if json401 != nil && json401.Message != "" {
		errMsg = json401.Message
	}

	if json500 != nil && json500.Message != "" {
		errMsg = json500.Message
	}

	return &api.APIError{
		Err:       fmt.Errorf("edge rig error: %s (http code %d)", errMsg, statusCode),
		ClientMsg: clientMsg,
		Code:      http.StatusInternalServerError,
	}
}
