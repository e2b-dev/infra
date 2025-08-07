package utils

import "fmt"

func ToPtr[T any](v T) *T {
	return &v
}

func Sprintp[T any](s *T) string {
	if s == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%v", *s)
}
