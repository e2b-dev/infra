package cfg

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

const (
	// ServiceDiscoveryProviderNomad queries Nomad's HTTP API (the original / Nomad-based deploy).
	ServiceDiscoveryProviderNomad = "nomad"
	// ServiceDiscoveryProviderKubernetes queries the in-cluster K8s API (the K8s deploy).
	ServiceDiscoveryProviderKubernetes = "kubernetes"
	// ServiceDiscoveryProviderLocal returns a single statically configured
	// orchestrator address. Used to develop the API against the darwin dummy
	// orchestrator on macOS, where neither Nomad nor Kubernetes is available.
	ServiceDiscoveryProviderLocal = "local"
)

type Config struct {
	AdminToken string `env:"ADMIN_TOKEN"`

	AnalyticsCollectorAPIToken string `env:"ANALYTICS_COLLECTOR_API_TOKEN"`
	AnalyticsCollectorHost     string `env:"ANALYTICS_COLLECTOR_HOST"`

	ClickhouseConnectionString  string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	ClickhouseConnectionStrings []string `env:"CLICKHOUSE_CONNECTION_STRINGS" envSeparator:";"`

	LokiPassword string `env:"LOKI_PASSWORD"`
	LokiURL      string `env:"LOKI_URL,required"`
	LokiUser     string `env:"LOKI_USER"`

	// ServiceDiscoveryProvider selects how the API discovers orchestrator and template-manager instances.
	// Allowed values:
	//   "nomad"      (default) - query the local Nomad agent's HTTP API.
	//   "kubernetes"           - list pods via the in-cluster K8s API.
	ServiceDiscoveryProvider string `env:"SERVICE_DISCOVERY_PROVIDER" envDefault:"nomad"`

	NomadAddress string `env:"NOMAD_ADDRESS" envDefault:"http://localhost:4646"`
	NomadToken   string `env:"NOMAD_TOKEN"`

	// NomadOrchestratorServiceNames is the comma-separated list of
	// Nomad-native service names whose registrations enumerate orchestrator
	// instances (GET /v1/service/<name> per name, results unioned). Every
	// orchestrator jobspec registers one of these services, regardless of
	// job type or node pool. Used when ServiceDiscoveryProvider=nomad.
	NomadOrchestratorServiceNames []string `env:"NOMAD_ORCHESTRATOR_SERVICE_NAMES" envDefault:"orchestrator" envSeparator:","`

	// NomadOrchestratorLegacyDiscoveryEnabled enables a node-pool-based
	// discovery FALLBACK unioned with the service-based discovery above: all
	// ready Nomad nodes in the "default" pool are assumed to run an
	// orchestrator on the well-known port. It covers orchestrator jobs
	// deployed from jobspecs that register their service with an empty
	// Address (pre-port-label-fix), which service discovery skips as
	// unroutable, and removes any rollout ordering constraint between the
	// API and orchestrator releases. Set to false once no legacy orchestrator
	// jobs remain. Used when ServiceDiscoveryProvider=nomad.
	NomadOrchestratorLegacyDiscoveryEnabled bool `env:"NOMAD_ORCHESTRATOR_LEGACY_DISCOVERY_ENABLED" envDefault:"true"`

	// LocalOrchestratorAddress is the "host:port" address of a statically
	// configured orchestrator instance. Required when
	// ServiceDiscoveryProvider=local. Used for local dev against the darwin
	// dummy orchestrator.
	LocalOrchestratorAddress string `env:"LOCAL_ORCHESTRATOR_ADDRESS" envDefault:"127.0.0.1:5008"`

	// Used when ServiceDiscoveryProvider=kubernetes.
	K8sNamespace                       string `env:"K8S_NAMESPACE"                           envDefault:"default"`
	K8sOrchestratorPodLabelSelector    string `env:"K8S_ORCHESTRATOR_POD_LABEL_SELECTOR"     envDefault:"app.kubernetes.io/name=orchestrator"`
	K8sTemplateManagerPodLabelSelector string `env:"K8S_TEMPLATE_MANAGER_POD_LABEL_SELECTOR" envDefault:"app.kubernetes.io/name=template-manager"`

	PostgresConnectionString string `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	DBMaxOpenConnections     int32  `env:"DB_MAX_OPEN_CONNECTIONS"                      envDefault:"40"`
	DBMinIdleConnections     int32  `env:"DB_MIN_IDLE_CONNECTIONS"                      envDefault:"5"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`
	AuthDBMinIdleConnections          int32  `env:"AUTH_DB_MIN_IDLE_CONNECTIONS"           envDefault:"5"`
	AuthDBMaxOpenConnections          int32  `env:"AUTH_DB_MAX_OPEN_CONNECTIONS"           envDefault:"20"`

	PosthogAPIKey string `env:"POSTHOG_API_KEY"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`
	RedisPoolSize    int    `env:"REDIS_POOL_SIZE"     envDefault:"160"`

	APIInternalGrpcPort uint16 `env:"API_INTERNAL_GRPC_PORT" envDefault:"5009"`
	APIEdgeGrpcPort     uint16 `env:"API_EDGE_GRPC_PORT"     envDefault:"5109"`

	ClientProxyOIDCIssuerURL string `env:"CLIENT_PROXY_OIDC_ISSUER_URL"`

	SandboxAccessTokenHashSeed string `env:"SANDBOX_ACCESS_TOKEN_HASH_SEED"`

	VolumesToken VolumesTokenConfig

	AuthProvider auth.ProviderConfig `env:"AUTH_PROVIDER_CONFIG"`

	DefaultPersistentVolumeType string `env:"DEFAULT_PERSISTENT_VOLUME_TYPE"`

	DomainName string `env:"DOMAIN_NAME" envDefault:""`
}

type FailureCondition string

const (
	FailureConditionInvalidServiceDiscoveryProvider FailureCondition = "invalid_service_discovery_provider"
)

type FailureError struct {
	Condition FailureCondition
	err       error
}

func (e *FailureError) Error() string {
	return e.err.Error()
}

func (e *FailureError) Unwrap() error {
	return e.err
}

func ParseFailureCondition(err error) (FailureCondition, bool) {
	var failureErr *FailureError
	if !errors.As(err, &failureErr) {
		return "", false
	}

	return failureErr.Condition, true
}

func newFailureError(condition FailureCondition, message string) error {
	return &FailureError{
		Condition: condition,
		err:       errors.New(message),
	}
}

type JWTSigningKey any

// PublicSigningKey is a public verification key published in the JWKS. During
// rotation the deployment configures several: the currently-active signing key
// plus any recently-retired keys whose already-issued tokens have not yet
// expired and must still verify.
type PublicSigningKey struct {
	// Name is the key id (JWKS `kid`); it matches the `kid`/`tokid` header set
	// on tokens signed with this key.
	Name string
	// Method is the JWT signing algorithm this key verifies (e.g. "EdDSA",
	// "RS256"), published as the JWK `alg`.
	Method string
	// Key is the parsed public half of the key pair.
	Key crypto.PublicKey
}

// PublicSigningKeys is the full rotation set of public verification keys. It is
// parsed from a single JSON-array env var so the whole set can be supplied at
// once (see the VOLUME_TOKEN_SIGNING_PUBLIC_KEYS parser below).
type PublicSigningKeys []PublicSigningKey

type VolumesTokenConfig struct {
	// Enabled explicitly toggles volume content token signing. When true (the
	// default), all signing env vars are required. Set to false when volumes are
	// disabled so the signing env vars may be omitted entirely.
	Enabled bool `env:"VOLUME_TOKEN_ENABLED" envDefault:"true"`

	Issuer         string            `env:"VOLUME_TOKEN_ISSUER"`
	SigningMethod  jwt.SigningMethod `env:"VOLUME_TOKEN_SIGNING_METHOD"`
	SigningKey     JWTSigningKey     `env:"VOLUME_TOKEN_SIGNING_KEY"`
	SigningKeyName string            `env:"VOLUME_TOKEN_SIGNING_KEY_NAME"`
	Duration       time.Duration     `env:"VOLUME_TOKEN_DURATION"         envDefault:"1h"`

	// SigningPublicKeys is the set of public verification keys to publish at
	// /.well-known/jwks.json. It carries every key valid for verification during
	// rotation, including retired keys whose issued tokens have not yet expired.
	// New tokens are always signed with the single active key above; this set is
	// verification-only. When unset, the JWKS falls back to the public half of
	// the active signing key alone.
	SigningPublicKeys PublicSigningKeys `env:"VOLUME_TOKEN_SIGNING_PUBLIC_KEYS"`
}

// IsConfigured reports whether volume content token signing is enabled.
func (c VolumesTokenConfig) IsConfigured() bool {
	return c.Enabled
}

// PublicJWKS returns the JSON Web Key Set exposing the public verification keys
// for volume token signing, suitable for publishing at .well-known/jwks.json.
//
// It publishes the full rotation set (SigningPublicKeys) unioned with the public
// half of the active signing key, deduplicated by key id, so tokens signed by
// either the active key or a recently-retired key still verify. When the
// rotation set is unset it falls back to the active key alone.
//
// It returns ok=false when signing is disabled or when there is nothing safe to
// publish — notably when the only key is symmetric (HMAC): a symmetric secret IS
// the signing key, so publishing it would let anyone forge tokens. Only
// asymmetric keys (RSA/ECDSA/Ed25519) are ever published.
func (c VolumesTokenConfig) PublicJWKS() (jose.JSONWebKeySet, bool) {
	if !c.Enabled {
		return jose.JSONWebKeySet{}, false
	}

	keys := make([]jose.JSONWebKey, 0, len(c.SigningPublicKeys)+1)
	seen := make(map[string]struct{}, len(c.SigningPublicKeys)+1)

	// Explicitly configured rotation set (active + retired-but-unexpired keys).
	for _, k := range c.SigningPublicKeys {
		if _, ok := seen[k.Name]; ok {
			continue
		}
		seen[k.Name] = struct{}{}
		keys = append(keys, jose.JSONWebKey{
			Key:       k.Key,
			KeyID:     k.Name,
			Algorithm: k.Method,
			Use:       "sig",
		})
	}

	// Always publish the active key derived from its private half, so it is
	// present even when the rotation set env var is unset (e.g. local dev) or
	// happens to omit it. Skipped for symmetric (HMAC) keys, which have no
	// publishable public half.
	if _, ok := seen[c.SigningKeyName]; !ok {
		if pub, alg, ok := c.activePublicKey(); ok {
			keys = append(keys, jose.JSONWebKey{
				Key:       pub,
				KeyID:     c.SigningKeyName,
				Algorithm: alg,
				Use:       "sig",
			})
		}
	}

	if len(keys) == 0 {
		return jose.JSONWebKeySet{}, false
	}

	return jose.JSONWebKeySet{Keys: keys}, true
}

// activePublicKey derives the public half of the active signing key. It returns
// ok=false for symmetric (HMAC) or unknown key types, which must never be
// published.
func (c VolumesTokenConfig) activePublicKey() (crypto.PublicKey, string, bool) {
	var pub crypto.PublicKey
	switch k := c.SigningKey.(type) {
	case *rsa.PrivateKey:
		pub = k.Public()
	case *ecdsa.PrivateKey:
		pub = k.Public()
	case ed25519.PrivateKey:
		pub = k.Public()
	default:
		return nil, "", false
	}

	alg := ""
	if c.SigningMethod != nil {
		alg = c.SigningMethod.Alg()
	}

	return pub, alg, true
}

// validate ensures that when signing is enabled all required signing env vars
// are present. A partial config is a deployment mistake and should fail fast at
// startup.
func (c VolumesTokenConfig) validate() error {
	if !c.Enabled {
		return nil
	}

	var missing []string
	if c.Issuer == "" {
		missing = append(missing, "VOLUME_TOKEN_ISSUER")
	}
	if c.SigningMethod == nil {
		missing = append(missing, "VOLUME_TOKEN_SIGNING_METHOD")
	}
	if c.SigningKey == nil {
		missing = append(missing, "VOLUME_TOKEN_SIGNING_KEY")
	}
	if c.SigningKeyName == "" {
		missing = append(missing, "VOLUME_TOKEN_SIGNING_KEY_NAME")
	}

	if len(missing) > 0 {
		return fmt.Errorf("VOLUME_TOKEN_ENABLED is set but the following are missing: %s", strings.Join(missing, ", "))
	}

	return nil
}

var (
	ErrInvalidJWTSigningKey = errors.New("JWT signing key must be in the format '$TYPE:base64($VALUE)'")
	ErrUnknownKeyType       = errors.New("unknown JWT signing key type")

	parserFuncs = map[reflect.Type]env.ParserFunc{
		reflect.TypeFor[auth.ProviderConfig](): func(v string) (any, error) {
			return auth.ParseProviderConfig(v)
		},
		reflect.TypeFor[JWTSigningKey](): func(v string) (any, error) {
			keyPieces := strings.SplitN(v, ":", 2)
			if len(keyPieces) != 2 {
				return nil, ErrInvalidJWTSigningKey
			}

			keyType := keyPieces[0]
			keyValue, err := base64.StdEncoding.DecodeString(keyPieces[1])
			if err != nil {
				return nil, errors.New("JWT signing key must be base64 encoded")
			}

			switch strings.ToUpper(keyType) {
			case "ECDSA":
				return jwt.ParseECPrivateKeyFromPEM(keyValue)
			case "RSA":
				return jwt.ParseRSAPrivateKeyFromPEM(keyValue)
			case "HMAC":
				return keyValue, nil
			case "ED25519":
				return jwt.ParseEdPrivateKeyFromPEM(keyValue)
			default:
				return nil, fmt.Errorf("%s: %w", keyType, ErrUnknownKeyType)
			}
		},
		reflect.TypeFor[jwt.SigningMethod](): func(v string) (any, error) {
			method := jwt.GetSigningMethod(v)
			if method == nil {
				return nil, fmt.Errorf("unknown signing method: %s", v)
			}

			return method, nil
		},
		reflect.TypeFor[PublicSigningKeys](): parsePublicSigningKeys,
	}
)

// parsePublicSigningKeys parses the VOLUME_TOKEN_SIGNING_PUBLIC_KEYS env var: a
// JSON array whose shape matches the Terraform `signing_public_keys` output, so
// it can be wired straight through with `jsonencode`. Each entry's PEM public
// key is parsed here so a malformed key fails fast at startup rather than when
// the JWKS is first served.
func parsePublicSigningKeys(v string) (any, error) {
	if strings.TrimSpace(v) == "" {
		return PublicSigningKeys(nil), nil
	}

	var raw []struct {
		Name      string `json:"name"`
		Method    string `json:"method"`
		Algorithm string `json:"algorithm"`
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(v), &raw); err != nil {
		return nil, fmt.Errorf("VOLUME_TOKEN_SIGNING_PUBLIC_KEYS must be a JSON array: %w", err)
	}

	keys := make(PublicSigningKeys, 0, len(raw))
	for _, r := range raw {
		if r.Name == "" {
			return nil, errors.New("VOLUME_TOKEN_SIGNING_PUBLIC_KEYS entry is missing a name")
		}

		pub, err := parsePKIXPublicKeyPEM(r.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("public signing key %q: %w", r.Name, err)
		}

		keys = append(keys, PublicSigningKey{Name: r.Name, Method: r.Method, Key: pub})
	}

	return keys, nil
}

func parsePKIXPublicKeyPEM(pemStr string) (crypto.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM data found")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX public key: %w", err)
	}

	return pub, nil
}

func Parse() (Config, error) {
	config, err := env.ParseAsWithOptions[Config](env.Options{
		FuncMap: parserFuncs,
	})
	if err != nil {
		return Config{}, err
	}

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	if !slices.Contains([]string{ServiceDiscoveryProviderNomad, ServiceDiscoveryProviderKubernetes, ServiceDiscoveryProviderLocal}, config.ServiceDiscoveryProvider) {
		return config, newFailureError(
			FailureConditionInvalidServiceDiscoveryProvider,
			fmt.Sprintf("invalid service discovery provider: %s", config.ServiceDiscoveryProvider),
		)
	}

	if err := config.VolumesToken.validate(); err != nil {
		return config, err
	}

	return config, nil
}
