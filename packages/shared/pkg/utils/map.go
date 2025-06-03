package utils

// Map goes through each item in the input slice and applies the function f to it.
// It returns a new slice with the results.
func Map[T any, U any](input []T, f func(T) U) []U {
	output := make([]U, len(input))
	for i, v := range input {
		output[i] = f(v)
	}
	return output
}
