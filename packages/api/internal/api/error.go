package api

var _ error = (*APIError)(nil)

type APIError struct {
	Err       error
	ClientMsg string
	Code      int
}

func (e *APIError) Error() string {
	return e.Err.Error()
}
