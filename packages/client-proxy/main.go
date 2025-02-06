package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/miekg/dns"
)

const dnsServer = "api.service.consul:5353"

// Create a DNS client
var client = new(dns.Client)

func proxy(w http.ResponseWriter, r *http.Request) {
	log.Printf("Request for %s %s\n", r.Host, r.URL.Path)

	// Extract sandbox id from the host (<port>-<sandbox id>-<old client id>.e2b.dev)
	host := strings.Split(r.Host, "-")[1]
	msg := new(dns.Msg)

	// Set the question
	msg.SetQuestion(fmt.Sprintf("%s.", host), dns.TypeA)

	var resp *dns.Msg
	var err error
	for range 3 {
		// Send the query to the server
		resp, _, err = client.Exchange(msg, dnsServer)

		// The api server wasn't found, maybe the API server is rolling and the DNS server is not updated yet
		if err != nil || len(resp.Answer) == 0 {
			log.Printf("Host not found: %s\n", host)
			continue
		}

		// The sandbox was not found, we want to return this information to the user
		if resp.Answer[0].String() == "localhost" {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("Sandbox not found"))

			return
		}

		break
	}

	// There's no answer, we can't proxy the request
	if err != nil || len(resp.Answer) == 0 {
		log.Printf("Host not found: %s\n", host)
		http.Error(w, "Host not found", http.StatusBadGateway)
		return
	}

	// We've resolved the node to proxy the request to
	log.Printf("Proxying request to %s\n", resp.Answer[0].String())
	targetUrl := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s: 30003", resp.Answer[0].(*dns.A).A.String()),
	}

	// Proxy the request
	httputil.NewSingleHostReverseProxy(targetUrl).ServeHTTP(w, r)
}

func main() {
	logger, err := logging.New(env.IsLocal())
	if err != nil {
		log.Fatalf("error creating logger: %v", err)
	}

	go func() {
		// Health check
		server := http.Server{Addr: ":3001"}

		server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			logger.Debug("Health check")
			writer.WriteHeader(http.StatusOK)
		})

		err := server.ListenAndServe()
		if err != nil {
			// Add different handling for the error
			logger.Infof("server error: %v", err)
		}
	}()

	// Proxy request to the correct node
	server := http.Server{Addr: ":3002"}
	server.Handler = http.HandlerFunc(proxy)

	err = server.ListenAndServe()
	// Add different handling for the error
	if err != nil {
		logger.Infof("server error: %v", err)
	}
}
