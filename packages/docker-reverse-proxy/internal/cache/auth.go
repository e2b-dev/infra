package cache

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/utils"
)

const (
	authInfoExpiration = time.Hour * 2
)

// AccessTokenData holds authentication details associated with a generated
// temporary e2b token: the underlying Docker registry token and the
// template identifier this token is valid for.
type AccessTokenData struct {
	DockerToken string
	TemplateID  string
}

// AuthCache provides a TTL-backed in-memory cache for mapping generated
// e2b tokens to `AccessTokenData`. It is intended to be short-lived and
// is used during reverse-proxy authentication flows.
type AuthCache struct {
	cache *ttlcache.Cache[string, *AccessTokenData]
}

// New returns a new initialized AuthCache instance.
// The cache is started in a separate goroutine and will store temporary
// access tokens for template/docker authentication lookup.
func New() *AuthCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *AccessTokenData](authInfoExpiration))

	go cache.Start()

	return &AuthCache{cache: cache}
}

// Get returns the auth token for the given teamID and e2bToken.
func (c *AuthCache) Get(e2bToken string) (*AccessTokenData, error) {
	if e2bToken == "" {
		return nil, errors.New("e2bToken is empty")
	}

	item := c.cache.Get(e2bToken)

	if item == nil {
		return nil, fmt.Errorf("creds for '%s' not found in cache", e2bToken)
	}

	return item.Value(), nil
}

// Create creates a new auth token for the given templateID and accessToken and returns e2bToken
func (c *AuthCache) Create(templateID, token string, expiresIn int) string {
	// Get docker token from the actual registry for the scope,
	// Create a new e2b token for the user and store it in the cache
	userToken := utils.GenerateRandomString(128)
	jsonResponse := fmt.Sprintf(`{"token": "%s", "expires_in": %d}`, userToken, expiresIn)

	data := &AccessTokenData{
		DockerToken: token,
		TemplateID:  templateID,
	}

	c.cache.Set(userToken, data, authInfoExpiration)

	log.Printf("Created new auth token for '%s' expiring in '%d'\n", templateID, expiresIn)

	return jsonResponse
}
