package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/miekg/dns"
)

func proxy(w http.ResponseWriter, r *http.Request) {
	log.Printf("Request for %s %s\n", r.Host, r.URL.Path)
	host := r.Host

	host = strings.Split(host, "-")[1]
	msg := new(dns.Msg)
	// Set the question: "example.com", Type A (IPv4 address)
	msg.SetQuestion(host+".", dns.TypeA)

	// Define the DNS server to query
	dnsServer := "api.service.consul:5353"

	// Create a DNS client
	client := new(dns.Client)

	var resp *dns.Msg
	var err error
	for range 3 {
		// Send the query to the server
		resp, _, err = client.Exchange(msg, dnsServer)
		if err != nil || len(resp.Answer) == 0 {
			log.Printf("Host not found: %s\n", host)
			continue
		}

		if resp.Answer[0].String() == "localhost" {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("Sandbox not found"))

			return
		}

		break
	}

	if err != nil || len(resp.Answer) == 0 {
		log.Printf("Host not found: %s\n", host)
		http.Error(w, "Host not found", http.StatusBadGateway)
		return
	}

	log.Printf("Proxying request to %s\n", resp.Answer[0].String())
	targetUrl := &url.URL{
		Scheme: "http",
		Host:   resp.Answer[0].(*dns.A).A.String() + ":3003",
	}

	p := httputil.NewSingleHostReverseProxy(targetUrl)
	p.ServeHTTP(w, r)
}

func main() {

	go func() {
		// Health check
		server := http.Server{
			Addr: ":3001",
		}

		server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
			writer.Write([]byte("OK"))
		})

		err := server.ListenAndServe()
		if err != nil {
			log.Printf("server error: %v", err)
		}
	}()

	go func() {
		// Health check
		server := http.Server{
			Addr: ":3003",
		}

		server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			log.Printf("Request for %s %s\n", request.Host, request.URL.Path)
			writer.WriteHeader(http.StatusBadGateway)
			writer.Write([]byte("Sandbox is not running"))
		})

		err := server.ListenAndServe()
		if err != nil {
			log.Printf("server error: %v", err)
		}
	}()

	// go func() {
	// Not running
	server := http.Server{
		Addr: ":3002",
	}
	server.Handler = http.HandlerFunc(proxy)

	err := server.ListenAndServe()
	if err != nil {
		log.Printf("server error: %v", err)
	}
}
