package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

type APIStore struct {
	db        *db.DB
	AuthCache *cache.AuthCache
	proxy     *httputil.ReverseProxy
}

func NewStore() *APIStore {
	authCache := cache.New()
	database, err := db.NewClient(3, 2)
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
		AuthCache: authCache,
		proxy:     proxy,
	}
}

func (a *APIStore) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Set the host to the URL host
	req.Host = req.URL.Host

	a.proxy.ServeHTTP(rw, req)
}
