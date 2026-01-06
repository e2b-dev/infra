package phases

import "strconv"

func stepString(phaseMetadata PhaseMeta) string {
	step := phaseMetadata.StepType
	if phaseMetadata.StepNumber != nil {
		step = strconv.Itoa(*phaseMetadata.StepNumber)
	}

	return step
}
