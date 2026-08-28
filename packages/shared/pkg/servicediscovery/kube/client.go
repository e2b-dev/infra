package kube

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
	// Without the email scope the API server sees the service account's
	// numeric unique ID, so an RBAC binding written against its email does not
	// match.
	userinfoEmailScope = "https://www.googleapis.com/auth/userinfo.email"

	// oauth2.Transport mints without a request context, so no deadline above it
	// reaches the exchange and an unanswered token endpoint blocks forever.
	tokenMintTimeout = 10 * time.Second
)

var (
	ErrEndpointNotHTTPS       = errors.New("kubernetes api endpoint must be an https:// URL")
	ErrEndpointHasCredentials = errors.New("kubernetes api endpoint must not contain a username or password")
	ErrOffEndpointOrigin      = errors.New("refusing to leave the configured kubernetes api origin")
	ErrEndpointUnparseable    = errors.New("kubernetes api endpoint is not a valid URL")
)

// NewClient builds the client the pod listers read through: the pod's own
// ServiceAccount when endpoint is empty, otherwise endpoint reached on the
// caller's Google identity. ctx bounds the credential, which is refreshed for
// as long as the client is used.
func NewClient(ctx context.Context, endpoint string) (kubernetes.Interface, error) {
	config, err := newRESTConfig(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes client: %w", err)
	}

	return client, nil
}

func newRESTConfig(ctx context.Context, endpoint string) (*rest.Config, error) {
	if endpoint == "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("building in-cluster config: %w", err)
		}

		return config, nil
	}

	// The config below carries no TLS material and client-go derives the
	// scheme from it, so a scheme-less host resolves to http://.
	parsed, err := url.Parse(endpoint)
	if err != nil {
		// No cause and no value: this is startup-fatal and reaches the logs,
		// and net/url quotes the fragment it choked on — for "https://user:tok"
		// that fragment is the token.
		return nil, ErrEndpointUnparseable
	}
	// Userinfo moves the authority: "https://real-endpoint@elsewhere" dials
	// elsewhere while reading as the real endpoint. Checked before any
	// formatting, because url.Redacted masks a password but not a username.
	if parsed.User != nil {
		return nil, ErrEndpointHasCredentials
	}
	// The host the credential will bind to, not the one the URL nominally
	// carries: ":6443" and "." are non-empty authorities with no host, and
	// they otherwise fail unrecognisably inside the TLS handshake.
	if parsed.Scheme != "https" || canonicalHost(parsed) == "" {
		return nil, fmt.Errorf("%w: got scheme %q host %q", ErrEndpointNotHTTPS, parsed.Scheme, parsed.Host)
	}

	credentials := context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenMintTimeout})

	tokenSource, err := google.DefaultTokenSource(credentials, cloudPlatformScope, userinfoEmailScope)
	if err != nil {
		return nil, fmt.Errorf("building google default token source: %w", err)
	}

	config := &rest.Config{Host: endpoint}
	config.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &hostBoundBearer{
			authority: canonicalAuthority(parsed),
			authed:    &oauth2.Transport{Source: tokenSource, Base: rt},
		}
	})

	return config, nil
}

// hostBoundBearer refuses to leave the origin it was built for. net/http drops
// Authorization across a cross-host redirect, but an oauth2 transport re-adds
// it, handing a project-scoped Google token wherever the endpoint points, and a
// same-host redirect to http:// keeps it and sends it in clear. It fails such a
// request rather than retrying unauthenticated: a 200 from off-origin would be
// parsed as a pod list, and an empty one deregisters the fleet.
type hostBoundBearer struct {
	authority string
	authed    http.RoundTripper
}

// canonicalAuthority collapses the spellings an intermediary may redirect
// through — case, a trailing root dot, an explicit :443 — so a normalising hop
// keeps the credential rather than losing it and 401ing.
func canonicalAuthority(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}

	return net.JoinHostPort(canonicalHost(u), port)
}

// canonicalHost resolves the name net/http will dial, which it derives with
// IDNA, not case folding: ToLower maps U+0130 to "i" and IDNA maps it to a
// different registrable domain, so pinning the first hands the token to the
// second. An unresolvable name returns "", which the endpoint check rejects.
func canonicalHost(u *url.URL) string {
	ascii, err := idna.Lookup.ToASCII(strings.TrimSuffix(u.Hostname(), "."))
	if err != nil {
		return ""
	}

	return ascii
}

func (t *hostBoundBearer) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || canonicalAuthority(req.URL) != t.authority {
		return nil, fmt.Errorf("%w: %s://%s", ErrOffEndpointOrigin, req.URL.Scheme, req.URL.Host)
	}

	return t.authed.RoundTrip(req)
}
