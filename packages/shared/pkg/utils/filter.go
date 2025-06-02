package utils

func Filter[T any](input []T, f func(T) bool) []T {
	var output []T
	for _, v := range input {
		if f(v) {
			output = append(output, v)
		}
	}
	return output
}
