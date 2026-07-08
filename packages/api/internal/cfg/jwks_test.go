package cfg

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pkixPEM encodes a public key as a PKIX PEM block, matching the Terraform
// tls_private_key `public_key_pem` output that feeds the config.
func pkixPEM(t *testing.T, pub crypto.PublicKey) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)

	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestPublicJWKS(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	t.Run("disabled config is not published", func(t *testing.T) {
		t.Parallel()

		c := VolumesTokenConfig{Enabled: false, SigningKey: edKey}
		_, ok := c.PublicJWKS()
		assert.False(t, ok)
	})

	t.Run("HMAC secret is never published", func(t *testing.T) {
		t.Parallel()

		c := VolumesTokenConfig{
			Enabled:       true,
			SigningMethod: jwt.SigningMethodHS256,
			SigningKey:    []byte("super-secret-key"),
		}
		_, ok := c.PublicJWKS()
		assert.False(t, ok, "symmetric HMAC secret must not be exposed via JWKS")
	})

	asymmetric := []struct {
		name   string
		method jwt.SigningMethod
		key    any
	}{
		{"RSA", jwt.SigningMethodRS256, rsaKey},
		{"ECDSA", jwt.SigningMethodES256, ecKey},
		{"Ed25519", jwt.SigningMethodEdDSA, edKey},
	}

	for _, tc := range asymmetric {
		t.Run(tc.name+" falls back to the active key when no rotation set", func(t *testing.T) {
			t.Parallel()

			c := VolumesTokenConfig{
				Enabled:        true,
				SigningMethod:  tc.method,
				SigningKey:     tc.key,
				SigningKeyName: "key-v1",
			}

			jwks, ok := c.PublicJWKS()
			require.True(t, ok)
			require.Len(t, jwks.Keys, 1)

			key := jwks.Keys[0]
			assert.Equal(t, "key-v1", key.KeyID)
			assert.Equal(t, tc.method.Alg(), key.Algorithm)
			assert.Equal(t, "sig", key.Use)
			assert.True(t, key.IsPublic(), "only the public half must be exposed")
		})
	}

	t.Run("publishes full rotation set unioned with active key", func(t *testing.T) {
		t.Parallel()

		// Active key = key-v2 (also present in the rotation set); key-v1 is a
		// retired key still valid for verification.
		c := VolumesTokenConfig{
			Enabled:        true,
			SigningMethod:  jwt.SigningMethodEdDSA,
			SigningKey:     edKey,
			SigningKeyName: "key-v2",
			SigningPublicKeys: PublicSigningKeys{
				{Name: "key-v1", Method: "RS256", Key: rsaKey.Public()},
				{Name: "key-v2", Method: "EdDSA", Key: edKey.Public()},
			},
		}

		jwks, ok := c.PublicJWKS()
		require.True(t, ok)
		require.Len(t, jwks.Keys, 2, "active key already in the set must not be duplicated")

		byID := map[string]string{}
		for _, k := range jwks.Keys {
			assert.True(t, k.IsPublic())
			byID[k.KeyID] = k.Algorithm
		}
		assert.Equal(t, "RS256", byID["key-v1"])
		assert.Equal(t, "EdDSA", byID["key-v2"])
	})

	t.Run("appends active key when omitted from rotation set", func(t *testing.T) {
		t.Parallel()

		c := VolumesTokenConfig{
			Enabled:        true,
			SigningMethod:  jwt.SigningMethodEdDSA,
			SigningKey:     edKey,
			SigningKeyName: "key-v2",
			SigningPublicKeys: PublicSigningKeys{
				{Name: "key-v1", Method: "RS256", Key: rsaKey.Public()},
			},
		}

		jwks, ok := c.PublicJWKS()
		require.True(t, ok)
		require.Len(t, jwks.Keys, 2)
	})
}

func TestParsePublicSigningKeys(t *testing.T) {
	t.Parallel()

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	t.Run("empty is nil", func(t *testing.T) {
		t.Parallel()

		got, err := parsePublicSigningKeys("")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("parses terraform-shaped JSON array", func(t *testing.T) {
		t.Parallel()

		payload, err := json.Marshal([]map[string]string{
			{"name": "default", "method": "EdDSA", "algorithm": "ED25519", "public_key": pkixPEM(t, edKey.Public())},
			{"name": "next", "method": "RS256", "algorithm": "RSA", "public_key": pkixPEM(t, rsaKey.Public())},
		})
		require.NoError(t, err)

		got, err := parsePublicSigningKeys(string(payload))
		require.NoError(t, err)

		keys, ok := got.(PublicSigningKeys)
		require.True(t, ok)
		require.Len(t, keys, 2)
		assert.Equal(t, "default", keys[0].Name)
		assert.Equal(t, "EdDSA", keys[0].Method)
		assert.NotNil(t, keys[0].Key)
		assert.Equal(t, "next", keys[1].Name)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		t.Parallel()

		_, err := parsePublicSigningKeys("not json")
		require.Error(t, err)
	})

	t.Run("rejects entry with no name", func(t *testing.T) {
		t.Parallel()

		payload, err := json.Marshal([]map[string]string{
			{"method": "EdDSA", "public_key": pkixPEM(t, edKey.Public())},
		})
		require.NoError(t, err)

		_, err = parsePublicSigningKeys(string(payload))
		require.ErrorContains(t, err, "missing a name")
	})

	t.Run("rejects malformed PEM", func(t *testing.T) {
		t.Parallel()

		payload, err := json.Marshal([]map[string]string{
			{"name": "bad", "method": "EdDSA", "public_key": "not a pem"},
		})
		require.NoError(t, err)

		_, err = parsePublicSigningKeys(string(payload))
		require.ErrorContains(t, err, "bad")
	})
}
