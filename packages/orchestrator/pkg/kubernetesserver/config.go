package kubernetesserver

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	RuntimeClassCLH  = "kata-clh"
	RuntimeClassQEMU = "kata-qemu"

	// RuntimeClassMetadataKey lets an E2B caller select one of the operator-
	// allowed Kata RuntimeClasses without expanding the public sandbox API.
	RuntimeClassMetadataKey = consts.OrchestratorRuntimeClassMetadataKey

	defaultGRPCPort   = uint16(5008)
	defaultProxyPort  = uint16(5007)
	defaultHealthPort = uint16(5018)
	defaultEnvdPort   = int32(49983)
)

var supportedRuntimeClasses = map[string]struct{}{
	RuntimeClassCLH:  {},
	RuntimeClassQEMU: {},
}

// Config is the operator-owned interface of the Kubernetes sandbox module.
// Per-sandbox details remain behind the SandboxService gRPC interface.
type Config struct {
	Namespace string

	AllowedRuntimeClasses []string
	DefaultRuntimeClass   string

	SandboxImageTemplate string
	EnvdCommand          string
	EnvdArgs             []string
	EnvdPort             int32

	NodeSelector       map[string]string
	ServiceAccountName string
	ImagePullPolicy    string
	DNSNameservers     []string

	CreateTimeout time.Duration
	DeleteTimeout time.Duration

	GRPCPort   uint16
	ProxyPort  uint16
	HealthPort uint16

	NodeID         string
	NodeLabels     []string
	ServiceVersion string
	ServiceCommit  string

	CapacityCPU       uint32
	CapacityMemoryMiB uint64

	CPUArchitecture string
	CPUFamily       string
	CPUModel        string
	CPUModelName    string
}

func ConfigFromEnv(version, commit string) (Config, error) {
	cfg := Config{
		Namespace:             envOrDefault("POD_NAMESPACE", "e2b"),
		AllowedRuntimeClasses: splitCSV(envOrDefault("KATA_RUNTIME_CLASSES", RuntimeClassCLH+","+RuntimeClassQEMU)),
		DefaultRuntimeClass:   envOrDefault("DEFAULT_KATA_RUNTIME_CLASS", RuntimeClassCLH),
		SandboxImageTemplate:  os.Getenv("SANDBOX_IMAGE_TEMPLATE"),
		EnvdCommand:           envOrDefault("SANDBOX_ENVD_COMMAND", "/usr/bin/envd"),
		EnvdArgs:              strings.Fields(envOrDefault("SANDBOX_ENVD_ARGS", "-isnotfc -no-cgroups -verbose")),
		EnvdPort:              defaultEnvdPort,
		ServiceAccountName:    envOrDefault("SANDBOX_SERVICE_ACCOUNT", "e2b-sandbox"),
		ImagePullPolicy:       envOrDefault("SANDBOX_IMAGE_PULL_POLICY", "IfNotPresent"),
		DNSNameservers:        splitCSV(envOrDefault("SANDBOX_DNS_NAMESERVERS", consts.SandboxDefaultNameserver)),
		CreateTimeout:         2 * time.Minute,
		DeleteTimeout:         30 * time.Second,
		GRPCPort:              defaultGRPCPort,
		ProxyPort:             defaultProxyPort,
		HealthPort:            defaultHealthPort,
		NodeID:                envOrDefault("NODE_ID", envOrDefault("HOSTNAME", "kubernetes-orchestrator")),
		NodeLabels:            splitCSV(os.Getenv("NODE_LABELS")),
		ServiceVersion:        version,
		ServiceCommit:         commit,
		CPUArchitecture:       envOrDefault("SANDBOX_CPU_ARCHITECTURE", normalizedArchitecture(runtime.GOARCH)),
		CPUFamily:             os.Getenv("SANDBOX_CPU_FAMILY"),
		CPUModel:              os.Getenv("SANDBOX_CPU_MODEL"),
		CPUModelName:          os.Getenv("SANDBOX_CPU_MODEL_NAME"),
	}

	var err error
	if cfg.NodeSelector, err = parseMap(os.Getenv("SANDBOX_NODE_SELECTOR")); err != nil {
		return Config{}, fmt.Errorf("parse SANDBOX_NODE_SELECTOR: %w", err)
	}
	if cfg.EnvdPort, err = envInt32("SANDBOX_ENVD_PORT", cfg.EnvdPort); err != nil {
		return Config{}, err
	}
	if cfg.GRPCPort, err = envUint16("GRPC_PORT", cfg.GRPCPort); err != nil {
		return Config{}, err
	}
	if cfg.ProxyPort, err = envUint16("PROXY_PORT", cfg.ProxyPort); err != nil {
		return Config{}, err
	}
	if cfg.HealthPort, err = envUint16("HEALTH_PORT", cfg.HealthPort); err != nil {
		return Config{}, err
	}
	if cfg.CreateTimeout, err = envDuration("SANDBOX_CREATE_TIMEOUT", cfg.CreateTimeout); err != nil {
		return Config{}, err
	}
	if cfg.DeleteTimeout, err = envDuration("SANDBOX_DELETE_TIMEOUT", cfg.DeleteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.CapacityCPU, err = envUint32("SANDBOX_CAPACITY_CPU", 0); err != nil {
		return Config{}, err
	}
	if cfg.CapacityMemoryMiB, err = envUint64("SANDBOX_CAPACITY_MEMORY_MIB", 0); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if c.SandboxImageTemplate == "" {
		return fmt.Errorf("SANDBOX_IMAGE_TEMPLATE is required")
	}
	if !strings.Contains(c.SandboxImageTemplate, "{template_id}") || !strings.Contains(c.SandboxImageTemplate, "{build_id}") {
		return fmt.Errorf("SANDBOX_IMAGE_TEMPLATE must contain {template_id} and {build_id}")
	}
	if c.EnvdCommand == "" {
		return fmt.Errorf("sandbox envd command is required")
	}
	if c.EnvdPort <= 0 || c.EnvdPort > 65535 {
		return fmt.Errorf("sandbox envd port %d is invalid", c.EnvdPort)
	}
	if c.GRPCPort == 0 || c.ProxyPort == 0 || c.HealthPort == 0 {
		return fmt.Errorf("gRPC, proxy, and health ports must be non-zero")
	}
	if c.NodeID == "" {
		return fmt.Errorf("node ID is required")
	}
	if c.ServiceAccountName == "" {
		return fmt.Errorf("sandbox service account is required")
	}
	if len(c.DNSNameservers) == 0 || len(c.DNSNameservers) > 3 {
		return fmt.Errorf("SANDBOX_DNS_NAMESERVERS must contain between one and three public IP addresses")
	}
	for _, nameserver := range c.DNSNameservers {
		ip := net.ParseIP(nameserver)
		if isProtectedSandboxIP(ip) {
			return fmt.Errorf("sandbox DNS nameserver %q must be a public IP address", nameserver)
		}
	}
	switch c.ImagePullPolicy {
	case "Always", "IfNotPresent", "Never":
	default:
		return fmt.Errorf("unsupported image pull policy %q", c.ImagePullPolicy)
	}
	if c.CapacityCPU == 0 || c.CapacityMemoryMiB == 0 {
		return fmt.Errorf("SANDBOX_CAPACITY_CPU and SANDBOX_CAPACITY_MEMORY_MIB must both be greater than zero")
	}
	if len(c.AllowedRuntimeClasses) == 0 {
		return fmt.Errorf("at least one Kata RuntimeClass is required")
	}

	allowed := make(map[string]struct{}, len(c.AllowedRuntimeClasses))
	for _, name := range c.AllowedRuntimeClasses {
		if _, ok := supportedRuntimeClasses[name]; !ok {
			return fmt.Errorf("unsupported RuntimeClass %q; supported values are %s and %s", name, RuntimeClassCLH, RuntimeClassQEMU)
		}
		allowed[name] = struct{}{}
	}
	if _, ok := allowed[c.DefaultRuntimeClass]; !ok {
		return fmt.Errorf("default RuntimeClass %q is not in KATA_RUNTIME_CLASSES", c.DefaultRuntimeClass)
	}

	return nil
}

func isProtectedSandboxIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {
		return true
	}
	if ipv4 := ip.To4(); ipv4 != nil && ipv4[0] == 0 {
		return true
	}
	for _, cidr := range consts.SandboxDeniedCIDRs {
		_, protected, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid shared sandbox CIDR %q: %v", cidr, err))
		}
		if protected.Contains(ip) {
			return true
		}
	}
	return false
}

func (c Config) runtimeClass(metadata map[string]string) (string, error) {
	selected := c.DefaultRuntimeClass
	if requested := metadata[RuntimeClassMetadataKey]; requested != "" {
		selected = requested
	}
	for _, allowed := range c.AllowedRuntimeClasses {
		if selected == allowed {
			return selected, nil
		}
	}

	return "", fmt.Errorf("RuntimeClass %q is not allowed by this orchestrator", selected)
}

func (c Config) sandboxImage(templateID, buildID string) (string, error) {
	if !safeImageToken(templateID) || !safeImageToken(buildID) {
		return "", fmt.Errorf("template_id and build_id must contain only image-safe characters")
	}

	image := strings.NewReplacer(
		"{template_id}", templateID,
		"{build_id}", buildID,
	).Replace(c.SandboxImageTemplate)
	if strings.ContainsAny(image, "{} \t\r\n") {
		return "", fmt.Errorf("resolved sandbox image %q is invalid", image)
	}
	if _, err := reference.ParseNormalizedNamed(image); err != nil {
		return "", fmt.Errorf("resolved sandbox image %q is invalid: %w", image, err)
	}

	return image, nil
}

func safeImageToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizedArchitecture(value string) string {
	switch value {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return value
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseMap(value string) (map[string]string, error) {
	result := make(map[string]string)
	for _, entry := range splitCSV(value) {
		key, val, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(val) == "" {
			return nil, fmt.Errorf("expected key=value, got %q", entry)
		}
		result[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return result, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration: %q", name, value)
	}
	return parsed, nil
}

func envUint16(name string, fallback uint16) (uint16, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a non-zero uint16: %q", name, value)
	}
	return uint16(parsed), nil
}

func envInt32(name string, fallback int32) (int32, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive int32: %q", name, value)
	}
	return int32(parsed), nil
}

func envUint32(name string, fallback uint32) (uint32, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a uint32: %q", name, value)
	}
	return uint32(parsed), nil
}

func envUint64(name string, fallback uint64) (uint64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a uint64: %q", name, value)
	}
	return parsed, nil
}
