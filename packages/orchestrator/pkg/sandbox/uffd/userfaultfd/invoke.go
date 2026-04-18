package userfaultfd

// safeInvoke calls fn and returns its result, or nil if fn is nil.
func safeInvoke(fn func() error) error {
	if fn == nil {
		return nil
	}

	return fn()
}
