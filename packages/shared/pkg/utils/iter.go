package utils

import "iter"

func TransformTo[S, T any](iterator iter.Seq[S], f func(S) T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range iterator {
			if !yield(f(v)) {
				break
			}
		}
	}
}
