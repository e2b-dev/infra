package phases

import (
	"errors"
	"fmt"
)

type PhaseBuildError struct {
	Phase   string
	Step    string
	Message string
	Err     error
}

func (e *PhaseBuildError) Error() string {
	return fmt.Sprintf("%s/%s: %s: %v", e.Phase, e.Step, e.Message, e.Unwrap())
}

func (e *PhaseBuildError) Unwrap() error {
	return e.Err
}

func UnwrapPhaseBuildError(err error) *PhaseBuildError {
	var phaseBuildError *PhaseBuildError
	if errors.As(err, &phaseBuildError) {
		return phaseBuildError
	}
	return nil
}
