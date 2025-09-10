package utils

// ShallowCopyMap creates a new map with the same keys and values as the input map.
// Warning: Do not use this function to copy maps that contain maps or slices.
func ShallowCopyMap[K comparable, V any](m map[K]V) map[K]V {
	newMap := make(map[K]V)
	for k, v := range m {
		newMap[k] = v
	}
	return newMap
}
