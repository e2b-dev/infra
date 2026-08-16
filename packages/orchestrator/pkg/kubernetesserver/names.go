package kubernetesserver

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	managedByLabel        = "app.kubernetes.io/managed-by"
	managedByValue        = "e2b-kubernetes-orchestrator"
	appNameLabel          = "app.kubernetes.io/name"
	componentLabel        = "app.kubernetes.io/component"
	controllerComponent   = "kubernetes-orchestrator"
	sandboxComponent      = "sandbox"
	sandboxAppName        = "e2b-sandbox"
	sandboxIDLabel        = "e2b.dev/sandbox-id"
	runtimeClassLabel     = "e2b.dev/runtime-class"
	identityAnnotation    = "e2b.dev/identity"
	teamIDAnnotation      = "e2b.dev/team-id"
	executionIDAnnotation = "e2b.dev/execution-id"
	startTimeAnnotation   = "e2b.dev/start-time"
	endTimeAnnotation     = "e2b.dev/end-time"
	vcpuAnnotation        = "e2b.dev/vcpu"
	ramMiBAnnotation      = "e2b.dev/ram-mib"
	buildIDAnnotation     = "e2b.dev/build-id"
	templateIDAnnotation  = "e2b.dev/template-id"
)

func resourceName(kind, sandboxID string) string {
	digest := sha256.Sum256([]byte(sandboxID))
	suffix := hex.EncodeToString(digest[:])[:10]

	var b strings.Builder
	for _, r := range strings.ToLower(sandboxID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	readable := strings.Trim(b.String(), "-")
	if readable == "" {
		readable = "sandbox"
	}

	return "e2b-" + kind + "-" + readable + "-" + suffix
}

func podName(sandboxID string) string {
	return resourceName("sbx", sandboxID)
}

func secretName(sandboxID string) string {
	return resourceName("secret", sandboxID)
}

func networkPolicyName(sandboxID string) string {
	return resourceName("net", sandboxID)
}

func sandboxSelector(sandboxID string) string {
	return sandboxIDLabel + "=" + sandboxID
}

func managedSandboxSelector() string {
	return managedByLabel + "=" + managedByValue + "," + appNameLabel + "=" + sandboxAppName
}
