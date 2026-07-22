package team

type ForbiddenError struct {
	Message string
}

func (e *ForbiddenError) Error() string {
	return e.Message
}

type BlockedError struct {
	Message string
}

func (e *BlockedError) Error() string {
	return e.Message
}
