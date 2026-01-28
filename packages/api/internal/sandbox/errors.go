package sandbox

import (
	"errors"
	"fmt"
)

type LimitExceededError struct {
	TeamID string
}

func (e *LimitExceededError) Error() string {
	return fmt.Sprintf("team %s has exceeded the limit", e.TeamID)
}

type NotFoundError struct {
	SandboxID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("sandbox %s not found", e.SandboxID)
}

var (
	ErrAlreadyExists    = errors.New("sandbox already exists")
	ErrCannotShortenTTL = errors.New("cannot shorten ttl")
)
