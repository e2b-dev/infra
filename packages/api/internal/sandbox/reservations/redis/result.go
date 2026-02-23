package redis

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

// reservationResult is the serializable result stored in Redis for cross-instance communication.
type reservationResult struct {
	Sandbox sandbox.Sandbox   `json:"sandbox,omitzero"`
	Error   *reservationError `json:"error,omitempty"`
}

// reservationError preserves api.APIError fields for cross-instance error propagation.
type reservationError struct {
	Message   string `json:"message"`
	Code      int    `json:"code,omitempty"`
	ClientMsg string `json:"client_msg,omitempty"`
}

func encodeResult(sbx sandbox.Sandbox, err error) ([]byte, error) {
	result := reservationResult{
		Sandbox: sbx,
	}

	if err != nil {
		re := &reservationError{Message: err.Error()}
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			re.Code = apiErr.Code
			re.ClientMsg = apiErr.ClientMsg
		}
		result.Error = re
	}

	return json.Marshal(result)
}

func decodeResult(data []byte) (sandbox.Sandbox, error) {
	var result reservationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to unmarshal reservation result: %w", err)
	}

	if result.Error != nil {
		return sandbox.Sandbox{}, reconstructError(result.Error)
	}

	return result.Sandbox, nil
}

// reconstructError rebuilds the appropriate error type from the serialized representation.
// If the error had an API code, it reconstructs an *api.APIError to preserve
// errors.As(err, &apiErr) behavior in create_instance.go.
func reconstructError(re *reservationError) error {
	if re.Code != 0 {
		return &api.APIError{
			Code:      re.Code,
			ClientMsg: re.ClientMsg,
			Err:       fmt.Errorf("%s", re.Message),
		}
	}

	return fmt.Errorf("%s", re.Message)
}
