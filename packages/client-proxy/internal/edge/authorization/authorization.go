package authorization

import (
	"errors"
)

type AuthorizationService interface {
	VerifyAuthorization(token string) error
	GetSecret() string
}

type StaticTokenAuthorizationService struct {
	secret string
}

var ErrInvalidAuthorizationToken = errors.New("invalid authorization token")

func NewStaticTokenAuthorizationService(secret string) AuthorizationService {
	return &StaticTokenAuthorizationService{secret: secret}
}

func (s *StaticTokenAuthorizationService) VerifyAuthorization(token string) error {
	if token != s.secret {
		return ErrInvalidAuthorizationToken
	}

	return nil
}

func (s *StaticTokenAuthorizationService) GetSecret() string {
	return s.secret
}
