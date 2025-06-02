package utils

func UniqueBy[T any, K comparable](input []T, keyFunc func(T) K) []T {
	seen := make(map[K]struct{})
	var output []T
	for _, v := range input {
		key := keyFunc(v)
		if _, found := seen[key]; !found {
			seen[key] = struct{}{}
			output = append(output, v)
		}
	}
	return output
}
