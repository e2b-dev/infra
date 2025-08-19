package utils

import "fmt"

func ToPtr[T any](v T) *T {
	return &v
}

func FromPtr[T any](s *T) T {
	if s == nil {
		var zero T
		return zero
	}

	return *s
}

func Sprintp[T any](s *T) string {
	if s == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%v", *s)
}
