// Package cabundle parses delivered certificates and reconciles them with shared trust.
package cabundle

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// Certificate holds a DER identity and normalized PEM without block headers.
type Certificate struct {
	Fingerprint string
	PEM         []byte
}

// Bundle contains distinct certificates. Treat parsed bundles as immutable.
type Bundle []Certificate

// Parse rejects partial or malformed delivered bundles and removes duplicates.
func Parse(bundlePEM string) (Bundle, error) {
	var certs Bundle
	seen := make(map[string]struct{})
	for rest := bytes.TrimSpace([]byte(bundlePEM)); len(rest) > 0; {
		block, tail := pem.Decode(rest)
		// Decode skips malformed prefixes. Reject them rather than silently
		// installing only part of the delivered set.
		if block == nil || !bytes.HasPrefix(rest, []byte("-----BEGIN ")) || bytes.Count(rest[:len(rest)-len(tail)], []byte("-----BEGIN ")) != 1 {
			return nil, errors.New("malformed certificate PEM")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("PEM block is %q, not a certificate", block.Type)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		id := fmt.Sprintf("%x", sha256.Sum256(block.Bytes))
		if _, duplicate := seen[id]; !duplicate {
			certs = append(certs, Certificate{Fingerprint: id, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})})
			seen[id] = struct{}{}
		}
		rest = bytes.TrimSpace(tail)
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificates")
	}

	return certs, nil
}

// Equal compares certificate identities regardless of order or PEM formatting.
func (a Bundle) Equal(b Bundle) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]struct{}{}
	for _, cert := range a {
		set[cert.Fingerprint] = struct{}{}
	}
	for _, cert := range b {
		if _, ok := set[cert.Fingerprint]; !ok {
			return false
		}
	}

	return true
}

// Reconcile removes only previously owned certificates that are no longer requested.
// The caller must publish the returned bytes before committing the new ownership.
func Reconcile(content []byte, previouslyOwned, certs Bundle) ([]byte, Bundle) {
	previous := make(map[string]struct{}, len(previouslyOwned))
	for _, cert := range previouslyOwned {
		previous[cert.Fingerprint] = struct{}{}
	}
	wanted := make(map[string]struct{}, len(certs))
	for _, cert := range certs {
		wanted[cert.Fingerprint] = struct{}{}
	}
	present := make(map[string]struct{})
	var out bytes.Buffer
	for rest := content; len(rest) > 0; {
		block, tail := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = tail
		if block.Type == "CERTIFICATE" {
			id := fmt.Sprintf("%x", sha256.Sum256(block.Bytes))
			_, wasOwned := previous[id]
			_, want := wanted[id]
			if want {
				present[id] = struct{}{}
			}
			if wasOwned || want {
				continue
			}
		}
		_ = pem.Encode(&out, block)
	}
	var nextOwned Bundle
	for _, cert := range certs {
		_, found := present[cert.Fingerprint]
		out.Write(cert.PEM)
		if _, wasOwned := previous[cert.Fingerprint]; wasOwned || !found {
			nextOwned = append(nextOwned, cert)
		}
	}

	return out.Bytes(), nextOwned
}

// Missing returns requested certificates whose DER identities are absent from content.
// Like Reconcile, it scans existing PEM without validating X.509 or rewriting bytes.
func Missing(content []byte, certs Bundle) Bundle {
	missing := make(map[string]struct{}, len(certs))
	for _, cert := range certs {
		missing[cert.Fingerprint] = struct{}{}
	}
	for len(missing) > 0 {
		block, tail := pem.Decode(content)
		if block == nil {
			break
		}
		content = tail
		if block.Type == "CERTIFICATE" {
			delete(missing, fmt.Sprintf("%x", sha256.Sum256(block.Bytes)))
		}
	}
	var result Bundle
	for _, cert := range certs {
		if _, absent := missing[cert.Fingerprint]; absent {
			result = append(result, cert)
		}
	}

	return result
}
