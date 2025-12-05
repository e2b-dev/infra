package dates

import "time"

// CalculateStep determines the step size for metrics based on the time range.
// The result should always contain less than 1000 points.
func CalculateStep(start, end time.Time) time.Duration {
	// Calculate the step size in seconds
	duration := end.Sub(start)
	switch {
	case duration < time.Hour:
		return 5 * time.Second
	case duration < 6*time.Hour:
		return 30 * time.Second
	case duration < 12*time.Hour:
		return time.Minute
	case duration < 24*time.Hour:
		return 2 * time.Minute
	case duration < 7*24*time.Hour:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}
