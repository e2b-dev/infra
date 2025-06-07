package utils

// Filter takes a slice of any type T and a predicate function f.
// It returns a new slice containing only the elements from the input slice
// for which the predicate function returns true.
func Filter[T any](input []T, f func(T) bool) []T {
	var output []T
	for _, v := range input {
		if f(v) {
			output = append(output, v)
		}
	}
	return output
}
