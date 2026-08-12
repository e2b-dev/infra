package jwks

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

const (
	discoveryTestAudience = "test-audience"
	unconventionalPath    = "/oauth2/keys"
)

// issuerFixture serves a key set at an unconventional path and, separately, at
// the conventional one, so a test can tell which of the two a verifier read.
type issuerFixture struct {
	server            *httptest.Server
	discoveryIssuer   func() string
	discoveryRequests *atomic.Int64
	keys              *atomic.Value
	keyID             string
	privateKey        *ecdsa.PrivateKey
}

func newIssuerFixture(t *testing.T, serveConventionalPath bool) *issuerFixture {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	thumbprint, err := (&jose.JSONWebKey{Key: &privateKey.PublicKey}).Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	keyID := base64.RawURLEncoding.EncodeToString(thumbprint)

	fixture := &issuerFixture{
		discoveryRequests: &atomic.Int64{},
		keys:              &atomic.Value{},
		keyID:             keyID,
		privateKey:        privateKey,
	}
	fixture.keys.Store([]jose.JSONWebKey{{
		Key:       &privateKey.PublicKey,
		KeyID:     keyID,
		Algorithm: jwt.SigningMethodES256.Alg(),
		Use:       "sig",
	}})

	mux := http.NewServeMux()
	fixture.server = httptest.NewTLSServer(mux)
	t.Cleanup(fixture.server.Close)
	fixture.discoveryIssuer = func() string { return fixture.server.URL }

	keySet := func(w http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: fixture.keys.Load().([]jose.JSONWebKey)}
		if err := json.NewEncoder(w).Encode(set); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}
	mux.HandleFunc(unconventionalPath, keySet)
	if serveConventionalPath {
		mux.HandleFunc(defaultJWKSPath, keySet)
	}

	mux.HandleFunc(defaultDiscoveryPath, func(w http.ResponseWriter, _ *http.Request) {
		fixture.discoveryRequests.Add(1)
		err := json.NewEncoder(w).Encode(map[string]string{
			"issuer":   fixture.discoveryIssuer(),
			"jwks_uri": fixture.server.URL + unconventionalPath,
		})
		if err != nil {
			t.Errorf("encode discovery document: %v", err)
		}
	})

	return fixture
}

func (f *issuerFixture) config(discoveryURL string) Config {
	return Config{Issuer: Issuer{
		URL:                 f.server.URL,
		DiscoveryURL:        discoveryURL,
		Audiences:           []string{discoveryTestAudience},
		AudienceMatchPolicy: AudienceMatchAny,
	}}
}

func (f *issuerFixture) discoveryURL() string {
	return f.server.URL + defaultDiscoveryPath
}

func (f *issuerFixture) token(t *testing.T) string {
	t.Helper()

	return f.tokenFrom(t, f.server.URL)
}

// tokenFrom signs a token naming an issuer of the caller's choosing, so a test
// can present one the verifier is not configured for. Everything else is what
// the issuer would mint: its key, its key id, and the audience under test.
func (f *issuerFixture) tokenFrom(t *testing.T, issuer string) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": issuer,
		"aud": discoveryTestAudience,
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	token.Header["kid"] = f.keyID
	signed, err := token.SignedString(f.privateKey)
	require.NoError(t, err)

	return signed
}

// The whole backwards-compatibility claim: an unset discovery URL must not
// start reading a discovery document. The fixture's document points at a key
// set the conventional path does not serve and, in the mismatch case, declares
// an issuer that would be refused, so a verifier that read it would be
// observable either way.
func TestUnsetDiscoveryURLNeverReadsTheDiscoveryDocument(t *testing.T) {
	t.Parallel()

	fixture := newIssuerFixture(t, true)
	fixture.discoveryIssuer = func() string { return "https://elsewhere.example.com" }

	verifier, err := NewVerifierFromIssuerJWKS(t.Context(), fixture.config(""), fixture.server.Client())
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), fixture.token(t))
	require.NoError(t, err)
	require.Zero(t, fixture.discoveryRequests.Load(),
		"the conventional path must resolve without a discovery fetch")
}

// An issuer publishing its key set anywhere but the conventional path is
// unreachable without this: the location lives only in the document.
func TestExplicitDiscoveryURLTakesTheKeySetLocationFromTheDocument(t *testing.T) {
	t.Parallel()

	fixture := newIssuerFixture(t, false)

	verifier, err := NewVerifierFromIssuerJWKS(t.Context(), fixture.config(fixture.discoveryURL()), fixture.server.Client())
	require.NoError(t, err)
	require.Positive(t, fixture.discoveryRequests.Load())

	_, err = verifier.Verify(t.Context(), fixture.token(t))
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), fixture.tokenFrom(t, "https://elsewhere.example.com"))
	require.Error(t, err, "a token naming an issuer this verifier is not configured for must be rejected")
}

// The control that stops a hijacked document redirecting key resolution.
func TestExplicitDiscoveryURLRejectsAnIssuerTheDocumentDoesNotDeclare(t *testing.T) {
	t.Parallel()

	fixture := newIssuerFixture(t, false)
	fixture.discoveryIssuer = func() string { return "https://elsewhere.example.com" }

	verifier, err := NewVerifierFromIssuerJWKS(t.Context(), fixture.config(fixture.discoveryURL()), fixture.server.Client())
	require.Nil(t, verifier)
	require.ErrorContains(t, err, "does not match configured issuer")
	require.ErrorContains(t, err, "https://elsewhere.example.com")
}

// Startup aborts rather than serving 401s until somebody restarts the process.
func TestUnreachableDiscoveryURLFailsConstruction(t *testing.T) {
	t.Parallel()

	fixture := newIssuerFixture(t, false)
	unreachable := httptest.NewTLSServer(http.NewServeMux())
	unreachableURL := unreachable.URL
	unreachable.Close()

	verifier, err := NewVerifierFromIssuerJWKS(t.Context(),
		fixture.config(unreachableURL+defaultDiscoveryPath), fixture.server.Client())
	require.Nil(t, verifier)
	require.ErrorContains(t, err, "fetch OIDC discovery document")
}
