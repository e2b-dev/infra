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

type AccessTokenData struct {
	TemplateID string
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
		return nil, errors.New("e2bToken is empty")
	}

	item := c.cache.Get(e2bToken)

	if item == nil {
		return nil, fmt.Errorf("creds for '%s' not found in cache", e2bToken)
	}

	return item.Value(), nil
}

// Create creates a new short-lived proxy token for the given templateID.
// Upstream Artifact Registry credentials are resolved per request and never
// retained in this cache.
func (c *AuthCache) Create(templateID string, expiresIn int) string {
	userToken := utils.GenerateRandomString(128)
	jsonResponse := fmt.Sprintf(`{"token": "%s", "expires_in": %d}`, userToken, expiresIn)

	data := &AccessTokenData{
		TemplateID: templateID,
	}

	ttl := min(time.Duration(expiresIn)*time.Second, authInfoExpiration)
	c.cache.Set(userToken, data, ttl)

	log.Printf("Created new auth token for '%s' expiring in '%d'\n", templateID, expiresIn)

	return jsonResponse
}
