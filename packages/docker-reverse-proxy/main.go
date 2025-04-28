package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/constants"
	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/handlers"
	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/utils"
)

var commitSHA string

func main() {
	err := constants.CheckRequired()
	if err != nil {
		log.Fatal(err)
	}

	port := flag.Int("port", 5000, "Port for test HTTP server")
	flag.Parse()

	log.Println("Starting docker reverse proxy", "commit", commitSHA)

	store := handlers.NewStore()

	// https://distribution.github.io/distribution/spec/api/
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		log.Printf("Request: %s %s\n", req.Method, utils.SubstringMax(req.URL.String(), 100))

		// Health check for nomad
		if req.URL.Path == "/health" {
			store.HealthCheck(w, req)
			return
		}

		// https://docker-docs.uclv.cu/registry/spec/auth/oauth/
		// We are using Token validation, and not OAuth2, so we need to return 404 for the POST /v2/token endpoint
		if req.URL.Path == "/v2/token" && req.Method == http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// Provides version support for the Docker registry API
		// Doesn't require authentication as it will be requested for the push anyway
		// https://distribution.github.io/distribution/spec/api/#api-version-check
		if req.URL.Path == "/v2/" {
			return
		}

		// If the request doesn't have the Authorization header, we return 401 with the url for getting a token
		if req.Header.Get("Authorization") == "" {
			log.Printf("Authorization header is missing: %s\n", utils.SubstringMax(req.URL.String(), 100))
			utils.SetDockerUnauthorizedHeaders(w)

			return
		}

		// Get token to access the Docker repository
		if req.URL.Path == "/v2/token" {
			err = store.GetToken(w, req)
			if err != nil {
				log.Printf("Error while getting token: %s\n", err)
			}
			return
		}

		// Proxy all other requests
		store.Proxy(w, req)
	})

	log.Printf("Starting server on port: %d\n", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", strconv.Itoa(*port)), nil))
}
