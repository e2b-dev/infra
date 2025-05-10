package proxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SandboxForwardProxy handles outbound traffic from sandboxes
type SandboxForwardProxy struct {
	server *http.Server
	ca     *x509.Certificate
	caKey  *rsa.PrivateKey
	certs  sync.Map // map[string]*tls.Certificate
}

func NewSandboxForwardProxy(port uint) *SandboxForwardProxy {
	server := &http.Server{Addr: fmt.Sprintf(":%d", port)}

	caCert, caKey, err := generateCA()
	if err != nil {
		log.Fatalf("Failed to generate CA certificate and key: %v", err)
		os.Exit(1)
	}

	return &SandboxForwardProxy{
		server: server,
		ca:     caCert,
		caKey:  caKey,
	}
}

// generateCA generates a new Certificate Authority (CA) certificate and private key
func generateCA() (*x509.Certificate, *rsa.PrivateKey, error) {
	// Generate private key for CA
	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA private key: %w", err)
	}

	// Create CA certificate template
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   "E2B Root CA",
			Organization: []string{"E2B"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0), // Valid for 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	// Self-sign the CA certificate
	caDerBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPrivateKey.PublicKey, caPrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	// Parse the CA certificate
	caCert, err := x509.ParseCertificate(caDerBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	return caCert, caPrivateKey, nil
}

func (p *SandboxForwardProxy) generateCert(hostname string) (*tls.Certificate, error) {
	// Check if we already have a certificate for this hostname
	if cert, ok := p.certs.Load(hostname); ok {
		return cert.(*tls.Certificate), nil
	}

	// Generate private key for the certificate
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   hostname,
			Organization: []string{"E2B MITM Proxy"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{hostname},
	}

	// Create certificate
	derBytes, err := x509.CreateCertificate(rand.Reader, template, p.ca, &privateKey.PublicKey, p.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Parse the certificate
	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Create TLS certificate
	tlsCert := &tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privateKey,
		Leaf:        cert,
	}

	// Store the certificate
	p.certs.Store(hostname, tlsCert)

	return tlsCert, nil
}

func (p *SandboxForwardProxy) Start() error {
	serverTransport := &http.Transport{
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   8192,
		IdleConnTimeout:       620 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 24 * time.Hour,
		DisableKeepAlives:     true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			zap.L().Info("Dialing", zap.String("network", network), zap.String("addr", addr))
			return net.Dial(network, addr)
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			zap.L().Info("Dialing TLS", zap.String("network", network), zap.String("addr", addr))
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			// Generate certificate for the host
			cert, err := p.generateCert(host)
			if err != nil {
				return nil, fmt.Errorf("failed to generate certificate: %w", err)
			}

			// Create TLS config for the connection
			tlsConfig := &tls.Config{
				Certificates: []tls.Certificate{*cert},
				MinVersion:   tls.VersionTLS12,
				MaxVersion:   tls.VersionTLS13,
			}

			// Establish TCP connection
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}

			// Wrap connection with TLS
			tlsConn := tls.Client(conn, tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return nil, err
			}

			return tlsConn, nil
		},
	}
	p.server.Handler = http.HandlerFunc(p.proxyHandler(serverTransport))

	return p.server.ListenAndServe()
}

func (p *SandboxForwardProxy) Close(ctx context.Context) error {
	var err error
	select {
	case <-ctx.Done():
		err = p.server.Close()
	default:
		err = p.server.Shutdown(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to shutdown proxy server: %w", err)
	}

	return nil
}

func (p *SandboxForwardProxy) proxyHandler(transport *http.Transport) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		zap.L().Info("Forwarding request", zap.String("Uurl", r.URL.String()), zap.String("method", r.Method))
		// Handle CONNECT method for HTTPS
		if r.Method == http.MethodConnect {
			handleConnect(w, r)
			return
		}

		// Handle regular HTTP requests
		handleHTTP(w, r, transport)
	}
}

// handleConnect handles HTTPS CONNECT requests
func handleConnect(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("handle CONNECT request", zap.String("url", r.URL.String()))
	// Get the target host and port
	host := r.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}

	// Connect to the target
	targetConn, err := net.Dial("tcp", host)
	if err != nil {
		zap.L().Error("Failed to connect to target", zap.Error(err))
		http.Error(w, "Failed to connect to target", http.StatusServiceUnavailable)
		return
	}
	defer targetConn.Close()

	// Send 200 OK to client
	w.WriteHeader(http.StatusOK)

	// Hijack the connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		zap.L().Error("Hijacking not supported")
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		zap.L().Error("Failed to hijack connection", zap.Error(err))
		return
	}
	defer clientConn.Close()

	// Start bidirectional copy
	go func() {
		_, err := io.Copy(targetConn, clientConn)
		if err != nil {
			zap.L().Error("Error copying from client to target", zap.Error(err))
		}
	}()

	_, err = io.Copy(clientConn, targetConn)
	if err != nil {
		zap.L().Error("Error copying from target to client", zap.Error(err))
	}
}

// handleHTTP handles regular HTTP requests
func handleHTTP(w http.ResponseWriter, r *http.Request, transport *http.Transport) {
	// Create a new request to the target
	targetURL := r.URL
	if targetURL.Scheme == "" {
		targetURL.Scheme = "http"
	}
	if targetURL.Host == "" {
		targetURL.Host = r.Host
	}

	zap.L().Info("handle HTTP request", zap.String("url", targetURL.String()))

	// Create a new request
	req, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		zap.L().Error("Failed to create new request", zap.Error(err))
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	req.Header = r.Header.Clone()

	// Send the request
	resp, err := transport.RoundTrip(req)
	if err != nil {
		zap.L().Error("Failed to send request", zap.Error(err))
		http.Error(w, "Failed to send request", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		zap.L().Error("Failed to copy response body", zap.Error(err))
	}
}
