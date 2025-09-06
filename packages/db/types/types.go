package types

type JSONBStringMap map[string]string

type BuildReason struct {
	// Message with the status reason, currently reporting only for error status
	Message string `json:"message"`

	// Step that failed
	Step *string `json:"step,omitempty"`
}
