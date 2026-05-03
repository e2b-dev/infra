package internalcert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	privateca "google.golang.org/api/privateca/v1"
)

const (
	DefaultLifetime    = 90 * 24 * time.Hour
	DefaultRenewBefore = 30 * 24 * time.Hour
)

type Config struct {
	CertFile               string
	KeyFile                string
	CAPool                 string
	CertificateAuthorityID string
	DNSName                string
	CertificateIDPrefix    string
	Lifetime               time.Duration
	RenewBefore            time.Duration
	Now                    func() time.Time
}

type Result struct {
	Issued bool
	Expiry time.Time
}

func Ensure(ctx context.Context, config Config) (Result, error) {
	if config.CAPool == "" {
		return Result{}, nil
	}

	if config.CertFile == "" || config.KeyFile == "" {
		return Result{}, errors.New("cert and key files are required when internal TLS CA issuance is configured")
	}

	if config.DNSName == "" {
		return Result{}, errors.New("DNS name is required when internal TLS CA issuance is configured")
	}

	now := time.Now
	if config.Now != nil {
		now = config.Now
	}

	renewBefore := config.RenewBefore
	if renewBefore == 0 {
		renewBefore = DefaultRenewBefore
	}

	if cert, err := loadLeafCertificate(config.CertFile); err == nil {
		_, keyErr := os.Stat(config.KeyFile)
		if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
			return Result{}, fmt.Errorf("read existing internal TLS private key: %w", keyErr)
		}

		if keyErr == nil && cert.NotAfter.After(now().Add(renewBefore)) {
			return Result{Issued: false, Expiry: cert.NotAfter}, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("read existing internal TLS certificate: %w", err)
	}

	lifetime := config.Lifetime
	if lifetime == 0 {
		lifetime = DefaultLifetime
	}

	privateKey, keyPEM, err := newPrivateKey()
	if err != nil {
		return Result{}, err
	}

	csrPEM, err := newCSR(privateKey, config.DNSName)
	if err != nil {
		return Result{}, err
	}

	cert, err := issueCertificate(ctx, config, string(csrPEM), lifetime)
	if err != nil {
		return Result{}, err
	}

	certPEM := strings.Join(append([]string{cert.PemCertificate}, cert.PemCertificateChain...), "")
	if err := writePair(config.CertFile, []byte(certPEM), config.KeyFile, keyPEM); err != nil {
		return Result{}, err
	}

	leaf, err := firstCertificate([]byte(cert.PemCertificate))
	if err != nil {
		return Result{}, fmt.Errorf("parse issued internal TLS certificate: %w", err)
	}

	return Result{Issued: true, Expiry: leaf.NotAfter}, nil
}

func loadLeafCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return firstCertificate(data)
}

func firstCertificate(data []byte) (*x509.Certificate, error) {
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil, errors.New("certificate PEM block not found")
		}

		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}

		return x509.ParseCertificate(block.Bytes)
	}
}

func newPrivateKey() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate internal TLS private key: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal internal TLS private key: %w", err)
	}

	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func newCSR(key *ecdsa.PrivateKey, dnsName string) ([]byte, error) {
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   dnsName,
			Organization: []string{"E2B"},
		},
		DNSNames: []string{dnsName},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("create internal TLS CSR: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

func issueCertificate(ctx context.Context, config Config, csr string, lifetime time.Duration) (*privateca.Certificate, error) {
	service, err := privateca.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create CA Service client: %w", err)
	}

	create := service.Projects.Locations.CaPools.Certificates.Create(config.CAPool, &privateca.Certificate{
		Lifetime: durationSeconds(lifetime),
		PemCsr:   csr,
	}).CertificateId(certificateID(config.CertificateIDPrefix))

	if config.CertificateAuthorityID != "" {
		create = create.IssuingCertificateAuthorityId(config.CertificateAuthorityID)
	}

	cert, err := create.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("issue internal TLS certificate: %w", err)
	}

	return cert, nil
}

func writePair(certFile string, certPEM []byte, keyFile string, keyPEM []byte) error {
	if err := writeFileAtomic(certFile, certPEM, 0o644); err != nil {
		return fmt.Errorf("write internal TLS certificate: %w", err)
	}

	if err := writeFileAtomic(keyFile, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write internal TLS private key: %w", err)
	}

	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

func durationSeconds(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Seconds()))
}

var certificateIDCleaner = regexp.MustCompile(`[^a-z0-9-]+`)

func certificateID(prefix string) string {
	cleanPrefix := strings.Trim(certificateIDCleaner.ReplaceAllString(strings.ToLower(prefix), "-"), "-")
	if cleanPrefix == "" {
		cleanPrefix = "internal-tls"
	}

	suffix, err := rand.Int(rand.Reader, big.NewInt(1_000_000_000))
	if err != nil {
		return fmt.Sprintf("%s-%d", cleanPrefix, time.Now().UnixNano())
	}

	return fmt.Sprintf("%s-%d-%09d", cleanPrefix, time.Now().Unix(), suffix.Int64())
}
