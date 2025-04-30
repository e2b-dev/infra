package routing

type ErrInvalidHost struct{}

func (e *ErrInvalidHost) Error() string {
	return "invalid url host"
}

type ErrInvalidSandboxPort struct{}

func (e *ErrInvalidSandboxPort) Error() string {
	return "invalid sandbox port"
}

func NewErrSandboxNotFound(sandboxId string) *ErrSandboxNotFound {
	return &ErrSandboxNotFound{
		SandboxId: sandboxId,
	}
}

type ErrSandboxNotFound struct {
	SandboxId string
}

func (e *ErrSandboxNotFound) Error() string {
	return "sandbox not found"
}
