package phases

import (
	"errors"
)

type PhaseBuildError struct {
	Phase string
	Step  string
	Err   error
}

func (e *PhaseBuildError) Error() string {
	return e.Err.Error()
}

func (e *PhaseBuildError) Unwrap() error {
	return e.Err
}

func NewPhaseBuildError(phase BuilderPhase, err error) *PhaseBuildError {
	m := phase.Metadata()

	return &PhaseBuildError{
		Phase: string(m.Phase),
		Step:  phaseToStepString(phase),
		Err:   err,
	}
}

func UnwrapPhaseBuildError(err error) *PhaseBuildError {
	var phaseBuildError *PhaseBuildError
	if errors.As(err, &phaseBuildError) {
		return phaseBuildError
	}
	return nil
}
