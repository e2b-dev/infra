package phases

import "strconv"

func phaseToStepString(phase BuilderPhase) string {
	m := phase.Metadata()

	step := m.StepType
	if m.StepNumber != nil {
		step = strconv.Itoa(*m.StepNumber)
	}

	return step
}
