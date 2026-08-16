package kubernetesserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const (
	secretTrafficTokenKey = "traffic-token"
	secretMaskHostKey     = "mask-request-host"
)

type envdInitPayload struct {
	AccessToken *string           `json:"accessToken,omitempty"`
	EnvVars     map[string]string `json:"envVars,omitempty"`
	LifecycleID string            `json:"lifecycleID"`
	Timestamp   time.Time         `json:"timestamp"`
}

func initPayloadForPod(raw []byte, pod *corev1.Pod) ([]byte, error) {
	if pod == nil || pod.UID == "" {
		return nil, fmt.Errorf("ready Pod has no UID")
	}

	var payload envdInitPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode envd init payload: %w", err)
	}
	payload.LifecycleID = string(pod.UID)
	payload.Timestamp = time.Now().UTC()

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode envd init payload: %w", err)
	}
	return encoded, nil
}

func buildInitPayload(req *orchestrator.SandboxCreateRequest) ([]byte, error) {
	cfg := req.GetSandbox()
	payload := envdInitPayload{
		EnvVars:     cloneMap(cfg.GetEnvVars()),
		LifecycleID: podName(cfg.GetSandboxId()),
		Timestamp:   time.Now().UTC(),
	}
	if cfg.EnvdAccessToken != nil {
		token := cfg.GetEnvdAccessToken()
		payload.AccessToken = &token
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal envd init payload: %w", err)
	}
	return body, nil
}

func requestIdentity(req *orchestrator.SandboxCreateRequest, runtimeClass, image string) (string, error) {
	requestBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal sandbox request for identity: %w", err)
	}

	value := append(requestBytes, 0)
	value = append(value, runtimeClass...)
	value = append(value, 0)
	value = append(value, image...)
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

func sandboxLabels(sandboxID, runtimeClass string) map[string]string {
	return map[string]string{
		appNameLabel:      sandboxAppName,
		componentLabel:    sandboxComponent,
		managedByLabel:    managedByValue,
		sandboxIDLabel:    sandboxID,
		runtimeClassLabel: runtimeClass,
	}
}

func sandboxAnnotations(req *orchestrator.SandboxCreateRequest, identity string) map[string]string {
	cfg := req.GetSandbox()
	annotations := map[string]string{
		identityAnnotation:    identity,
		teamIDAnnotation:      cfg.GetTeamId(),
		executionIDAnnotation: cfg.GetExecutionId(),
		vcpuAnnotation:        strconv.FormatInt(cfg.GetVcpu(), 10),
		ramMiBAnnotation:      strconv.FormatInt(cfg.GetRamMb(), 10),
		buildIDAnnotation:     cfg.GetBuildId(),
		templateIDAnnotation:  cfg.GetTemplateId(),
	}
	if startTime := req.GetStartTime(); startTime != nil && startTime.IsValid() {
		annotations[startTimeAnnotation] = startTime.AsTime().UTC().Format(time.RFC3339Nano)
	} else {
		annotations[startTimeAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if endTime := req.GetEndTime(); endTime != nil && endTime.IsValid() {
		annotations[endTimeAnnotation] = endTime.AsTime().UTC().Format(time.RFC3339Nano)
	}
	return annotations
}

func buildPod(operator Config, req *orchestrator.SandboxCreateRequest, runtimeClass, image, identity string) (*corev1.Pod, error) {
	cfg := req.GetSandbox()
	cpu := resource.NewQuantity(cfg.GetVcpu(), resource.DecimalSI)
	memory := resource.NewQuantity(cfg.GetRamMb()*1024*1024, resource.BinarySI)
	storage := resource.NewQuantity(cfg.GetTotalDiskSizeMb()*1024*1024, resource.BinarySI)
	terminationGrace := int64(10)
	automountToken := false
	allowPrivilegeEscalation := false
	runAsNonRoot := false
	runAsUser := int64(0)
	port := operator.EnvdPort
	runtimeClassName := runtimeClass

	pullPolicy := corev1.PullPolicy(operator.ImagePullPolicy)
	switch pullPolicy {
	case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
	default:
		return nil, fmt.Errorf("unsupported image pull policy %q", operator.ImagePullPolicy)
	}

	requests := corev1.ResourceList{
		corev1.ResourceCPU:    *cpu,
		corev1.ResourceMemory: *memory,
	}
	limits := requests.DeepCopy()
	if cfg.GetTotalDiskSizeMb() > 0 {
		requests[corev1.ResourceEphemeralStorage] = *storage
		limits[corev1.ResourceEphemeralStorage] = *storage
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName(cfg.GetSandboxId()),
			Namespace:   operator.Namespace,
			Labels:      sandboxLabels(cfg.GetSandboxId(), runtimeClass),
			Annotations: sandboxAnnotations(req, identity),
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:              &runtimeClassName,
			RestartPolicy:                 corev1.RestartPolicyNever,
			DNSPolicy:                     corev1.DNSNone,
			DNSConfig:                     &corev1.PodDNSConfig{Nameservers: append([]string(nil), operator.DNSNameservers...)},
			TerminationGracePeriodSeconds: &terminationGrace,
			AutomountServiceAccountToken:  &automountToken,
			ServiceAccountName:            operator.ServiceAccountName,
			NodeSelector:                  cloneMap(operator.NodeSelector),
			SecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            "sandbox",
				Image:           image,
				ImagePullPolicy: pullPolicy,
				Command:         []string{operator.EnvdCommand},
				Args:            append([]string(nil), operator.EnvdArgs...),
				Ports: []corev1.ContainerPort{{
					Name:          "envd",
					ContainerPort: port,
					Protocol:      corev1.ProtocolTCP,
				}},
				Env: []corev1.EnvVar{
					{Name: "E2B_SANDBOX_ID", Value: cfg.GetSandboxId()},
					{Name: "E2B_EXECUTION_ID", Value: cfg.GetExecutionId()},
					{Name: "E2B_TEAM_ID", Value: cfg.GetTeamId()},
					{Name: "E2B_TEMPLATE_ID", Value: cfg.GetTemplateId()},
					{Name: "E2B_BUILD_ID", Value: cfg.GetBuildId()},
					{Name: "E2B_RUNTIME_CLASS", Value: runtimeClass},
				},
				Resources: corev1.ResourceRequirements{Requests: requests, Limits: limits},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivilegeEscalation,
					RunAsNonRoot:             &runAsNonRoot,
					RunAsUser:                &runAsUser,
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
						Path:   "/health",
						Port:   intstr.FromInt32(port),
						Scheme: corev1.URISchemeHTTP,
					}},
					InitialDelaySeconds: 1,
					PeriodSeconds:       1,
					TimeoutSeconds:      1,
					FailureThreshold:    60,
				},
			}},
		},
	}

	return pod, nil
}

func buildSecret(operator Config, req *orchestrator.SandboxCreateRequest, runtimeClass, identity string) (*corev1.Secret, error) {
	cfg := req.GetSandbox()
	data := make(map[string][]byte, 2)
	if token := cfg.GetNetwork().GetIngress().GetTrafficAccessToken(); token != "" {
		data[secretTrafficTokenKey] = []byte(token)
	}
	if mask := cfg.GetNetwork().GetIngress().GetMaskRequestHost(); mask != "" {
		data[secretMaskHostKey] = []byte(mask)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName(cfg.GetSandboxId()),
			Namespace:   operator.Namespace,
			Labels:      sandboxLabels(cfg.GetSandboxId(), runtimeClass),
			Annotations: map[string]string{identityAnnotation: identity},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}, nil
}

func buildNetworkPolicy(operator Config, cfg *orchestrator.SandboxConfig, runtimeClass, identity string) (*networkingv1.NetworkPolicy, error) {
	egressRules, restrictEgress, err := buildEgressRules(cfg)
	if err != nil {
		return nil, err
	}

	policyTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	if restrictEgress {
		policyTypes = append(policyTypes, networkingv1.PolicyTypeEgress)
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        networkPolicyName(cfg.GetSandboxId()),
			Namespace:   operator.Namespace,
			Labels:      sandboxLabels(cfg.GetSandboxId(), runtimeClass),
			Annotations: map[string]string{identityAnnotation: identity},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{sandboxIDLabel: cfg.GetSandboxId()}},
			PolicyTypes: policyTypes,
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{componentLabel: controllerComponent}},
				}},
			}},
			Egress: egressRules,
		},
	}, nil
}

func buildEgressRules(cfg *orchestrator.SandboxConfig) ([]networkingv1.NetworkPolicyEgressRule, bool, error) {
	network := cfg.GetNetwork()
	egress := network.GetEgress()
	if egress.GetEgressProxyAddress() != "" || egress.GetEgressProxyUsername() != "" || egress.GetEgressProxyPassword() != "" {
		return nil, false, fmt.Errorf("BYOP egress proxy is not supported by the Kubernetes backend")
	}
	if len(egress.GetAllowedDomains()) > 0 || len(egress.GetRules()) > 0 {
		return nil, false, fmt.Errorf("domain-based egress is not supported by Kubernetes NetworkPolicy")
	}

	if cfg.AllowInternetAccess != nil && !cfg.GetAllowInternetAccess() {
		return nil, true, nil
	}

	allowed := egress.GetAllowedCidrs()
	denied := egress.GetDeniedCidrs()
	if len(allowed) > 0 && len(denied) > 0 {
		return nil, false, fmt.Errorf("combined allow and deny CIDR lists are not supported by the Kubernetes backend")
	}

	if len(allowed) > 0 {
		allowedNetworks, err := normalizeCIDRs(allowed, "allowed")
		if err != nil {
			return nil, false, err
		}
		protectedNetworks, err := normalizeCIDRs(consts.SandboxDeniedCIDRs, "protected")
		if err != nil {
			return nil, false, err
		}

		rules := make([]networkingv1.NetworkPolicyEgressRule, 0, len(allowedNetworks))
		for _, network := range allowedNetworks {
			for _, protected := range protectedNetworks {
				if cidrsOverlap(network, protected) {
					return nil, false, fmt.Errorf("allowed CIDR %q overlaps protected sandbox CIDR %q", network.String(), protected.String())
				}
			}
			rules = append(rules, networkingv1.NetworkPolicyEgressRule{
				To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: network.String()}}},
			})
		}
		return rules, true, nil
	}

	deniedNetworks, err := normalizeCIDRs(append(append([]string(nil), consts.SandboxDeniedCIDRs...), denied...), "denied")
	if err != nil {
		return nil, false, err
	}

	var deniedV4, deniedV6 []string
	var denyAllV4, denyAllV6 bool
	for _, network := range deniedNetworks {
		if network.IP.To4() != nil {
			if network.String() == "0.0.0.0/0" {
				denyAllV4 = true
			} else {
				deniedV4 = append(deniedV4, network.String())
			}
		} else {
			if network.String() == "::/0" {
				denyAllV6 = true
			} else {
				deniedV6 = append(deniedV6, network.String())
			}
		}
	}

	peers := make([]networkingv1.NetworkPolicyPeer, 0, 2)
	if denyAllV4 {
		// No IPv4 peer means no IPv4 egress is admitted.
	} else if len(deniedV4) > 0 {
		peers = append(peers, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: deniedV4}})
	} else {
		peers = append(peers, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"}})
	}
	if denyAllV6 {
		// No IPv6 peer means no IPv6 egress is admitted.
	} else if len(deniedV6) > 0 {
		peers = append(peers, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: deniedV6}})
	} else {
		peers = append(peers, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: "::/0"}})
	}
	if len(peers) == 0 {
		return nil, true, nil
	}

	return []networkingv1.NetworkPolicyEgressRule{{To: peers}}, true, nil
}

func normalizeCIDRs(values []string, kind string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid %s CIDR %q: %w", kind, value, err)
		}
		networks = append(networks, network)
	}

	sort.Slice(networks, func(i, j int) bool {
		onesI, bitsI := networks[i].Mask.Size()
		onesJ, bitsJ := networks[j].Mask.Size()
		if bitsI != bitsJ {
			return bitsI < bitsJ
		}
		if onesI != onesJ {
			return onesI < onesJ
		}
		return networks[i].String() < networks[j].String()
	})

	result := make([]*net.IPNet, 0, len(networks))
	for _, network := range networks {
		covered := false
		for _, existing := range result {
			if sameAddressFamily(existing, network) && existing.Contains(network.IP) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, network)
		}
	}

	return result, nil
}

func cidrsOverlap(left, right *net.IPNet) bool {
	return sameAddressFamily(left, right) && (left.Contains(right.IP) || right.Contains(left.IP))
}

func sameAddressFamily(left, right *net.IPNet) bool {
	return (left.IP.To4() != nil) == (right.IP.To4() != nil)
}

func cloneMap[K comparable, V any](input map[K]V) map[K]V {
	if input == nil {
		return nil
	}
	result := make(map[K]V, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
