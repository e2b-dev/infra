//go:build linux

package phases

import (
	"errors"
	"fmt"
)

type PhaseBuildError struct {
	Phase string
	Step  string
	Err   error
}

func (e *PhaseBuildError) Error() string {
	if e.Err == nil {
		if e.Phase == "" && e.Step == "" {
			return "unknown build error"
		}
		if e.Step == "" {
			return fmt.Sprintf("build error (phase: %s)", e.Phase)
		}
		return fmt.Sprintf("build error (phase: %s, step: %s)", e.Phase, e.Step)
	}

	// Skip appending phase/step when they are empty or when the inner error
	// chain already carries identical metadata, to avoid duplication.
	if pbe := UnwrapPhaseBuildError(e.Err); pbe != nil && pbe.Phase == e.Phase && pbe.Step == e.Step {
		return e.Err.Error()
	}
	if e.Phase == "" && e.Step == "" {
		return e.Err.Error()
	}

	if e.Step == "" {
		return fmt.Sprintf("%s (phase: %s)", e.Err.Error(), e.Phase)
	}

	return fmt.Sprintf("%s (phase: %s, step: %s)", e.Err.Error(), e.Phase, e.Step)
}

func (e *PhaseBuildError) Unwrap() error {
	return e.Err
}

func NewPhaseBuildError(phaseMetadata PhaseMeta, err error) *PhaseBuildError {
	return &PhaseBuildError{
		Phase: string(phaseMetadata.Phase),
		Step:  stepString(phaseMetadata),
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
