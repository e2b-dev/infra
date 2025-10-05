package utils

func MapKeys[K comparable](m map[K]struct{}) []K {
	keys := make([]K, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	return keys
}
