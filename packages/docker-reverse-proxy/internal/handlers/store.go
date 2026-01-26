package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type APIStore struct {
	db        *client.Client
	authDb    *authdb.Client
	AuthCache *cache.AuthCache
	proxy     *httputil.ReverseProxy
}

func NewStore(ctx context.Context) *APIStore {
	authCache := cache.New()

	databaseURL := utils.RequiredEnv("POSTGRES_CONNECTION_STRING", "Postgres connection string")

	database, err := client.NewClient(ctx, databaseURL, pool.WithMaxConnections(3))
	if err != nil {
		log.Fatal(err)
	}
	authDatabase, err := authdb.NewClient(ctx, databaseURL, databaseURL, pool.WithMaxConnections(3))
	if err != nil {
		log.Fatal(err)
	}

	targetUrl := &url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s-docker.pkg.dev", consts.GCPRegion),
	}

	proxy := httputil.NewSingleHostReverseProxy(targetUrl)

	// Custom ModifyResponse function
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode == http.StatusUnauthorized {
			respBody, _ := io.ReadAll(resp.Body)
			log.Printf("Unauthorized request:[%s] %s\n", resp.Request.Method, respBody)
		}

		// You can also modify the response here if needed
		return nil
	}

	return &APIStore{
		db:        database,
		authDb:    authDatabase,
		AuthCache: authCache,
		proxy:     proxy,
	}
}

func (a *APIStore) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Set the host to the URL host
	req.Host = req.URL.Host

	a.proxy.ServeHTTP(rw, req)
}
