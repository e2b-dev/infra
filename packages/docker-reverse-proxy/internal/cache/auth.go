package cache

import (
	"fmt"
	"log"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/utils"
)

const (
	authInfoExpiration = time.Hour * 2
)

type AccessTokenData struct {
	DockerToken string
	TemplateID  string
}

type AuthCache struct {
	cache *ttlcache.Cache[string, *AccessTokenData]
}

func New() *AuthCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *AccessTokenData](authInfoExpiration))

	go cache.Start()

	return &AuthCache{cache: cache}
}

// Get returns the auth token for the given teamID and e2bToken.
func (c *AuthCache) Get(e2bToken string) (*AccessTokenData, error) {
	if e2bToken == "" {
		return nil, fmt.Errorf("e2bToken is empty")
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
