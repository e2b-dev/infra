package gcpauth

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type sequenceTokenSource struct {
	tokens []*oauth2.Token
	err    error
	calls  int
}

func (s *sequenceTokenSource) Token() (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}

	token := s.tokens[s.calls]
	s.calls++

	return token, nil
}

func TestRegistryAuthenticatorRefreshesExpiredADCTokens(t *testing.T) {
	source := &sequenceTokenSource{tokens: []*oauth2.Token{
		{AccessToken: "first", Expiry: time.Now().Add(-time.Minute)},
		{AccessToken: "second", Expiry: time.Now().Add(time.Hour)},
	}}
	authenticator := NewRegistryAuthenticatorWithTokenSource(source)

	first, err := authenticator.Authorization()
	require.NoError(t, err)
	require.Equal(t, "first", first.Password)

	second, err := authenticator.Authorization()
	require.NoError(t, err)
	require.Equal(t, "second", second.Password)
	require.Equal(t, "oauth2accesstoken", second.Username)
	require.Equal(t, 2, source.calls)
}

func TestRegistryAuthenticatorReusesValidADCToken(t *testing.T) {
	source := &sequenceTokenSource{tokens: []*oauth2.Token{
		{AccessToken: "valid", Expiry: time.Now().Add(time.Hour)},
	}}
	authenticator := NewRegistryAuthenticatorWithTokenSource(source)

	first, err := authenticator.Authorization()
	require.NoError(t, err)
	second, err := authenticator.Authorization()
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, source.calls)
}

func TestRegistryAuthenticatorRejectsTokenFailures(t *testing.T) {
	t.Run("nil source", func(t *testing.T) {
		authenticator := NewRegistryAuthenticatorWithTokenSource(nil)

		_, err := authenticator.Authorization()
		require.ErrorContains(t, err, "nil")
	})

	t.Run("source error", func(t *testing.T) {
		authenticator := NewRegistryAuthenticatorWithTokenSource(&sequenceTokenSource{err: errors.New("metadata unavailable")})

		_, err := authenticator.Authorization()
		require.ErrorContains(t, err, "metadata unavailable")
	})

	t.Run("empty token", func(t *testing.T) {
		authenticator := NewRegistryAuthenticatorWithTokenSource(&sequenceTokenSource{tokens: []*oauth2.Token{{}}})

		_, err := authenticator.Authorization()
		require.ErrorContains(t, err, "empty access token")
	})
}
