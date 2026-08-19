#!/usr/bin/env bash
# Read-only preflight for the Kubernetes/Kata E2B backend. It deliberately
# creates no namespace, Pod, RBAC object, SCC binding, or MachineConfig.

set -euo pipefail

context="${OKD_CONTEXT:-indentia-ap}"
namespace="${OKD_NAMESPACE:-e2b}"
node_selector="${OKD_NODE_SELECTOR:-katacontainers.io/kata-runtime=true}"
runtime_classes="${KATA_RUNTIME_CLASSES:-kata-clh,kata-qemu}"
cpu_architecture="${SANDBOX_CPU_ARCHITECTURE:-x86_64}"
output="text"
oc_bin="${OC:-oc}"

usage() {
  cat <<'EOF'
Usage: preflight.sh [--context NAME] [--namespace NAME]
                    [--node-selector SELECTOR] [--runtime-classes CSV]
                    [--cpu-architecture x86_64|aarch64] [--json]

Performs read-only checks for the E2B Kubernetes backend. Both kata-clh and
kata-qemu are required by default. This command never creates cluster state.
EOF
}

while (($#)); do
  case "$1" in
    --context) context="$2"; shift 2 ;;
    --namespace) namespace="$2"; shift 2 ;;
    --node-selector) node_selector="$2"; shift 2 ;;
    --runtime-classes) runtime_classes="$2"; shift 2 ;;
    --cpu-architecture) cpu_architecture="$2"; shift 2 ;;
    --json) output="json"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

command -v "$oc_bin" >/dev/null || { echo "$oc_bin is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

results='[]'
blocking=0

record() {
  local name="$1" level="$2" passed="$3" detail="$4"
  if [[ "$passed" != true && "$level" == blocking ]]; then
    blocking=$((blocking + 1))
  fi
  results=$(jq -cn \
    --argjson previous "$results" \
    --arg name "$name" \
    --arg level "$level" \
    --argjson passed "$passed" \
    --arg detail "$detail" \
    '$previous + [{name:$name, level:$level, passed:$passed, detail:$detail}]')
}

ocx=("$oc_bin" --context "$context")

if ! server="$("${ocx[@]}" whoami --show-server 2>/dev/null)"; then
  record api-reachable blocking false "context '$context' is not authenticated or reachable"
  jq -n --arg context "$context" --argjson checks "$results" \
    '{provider:"okd-kata",context:$context,ready:false,checks:$checks}'
  exit 1
fi
record api-reachable blocking true "$server"

version="$("${ocx[@]}" version -o json 2>/dev/null || echo '{}')"
server_version=$(jq -r '.openshiftVersion // .serverVersion.gitVersion // "unknown"' <<<"$version")
record server-version info true "$server_version"

ready_nodes="$("${ocx[@]}" get nodes -l "$node_selector" -o json 2>/dev/null || echo '{"items":[]}')"
ready_count=$(jq '[.items[] | select(any(.status.conditions[]?; .type=="Ready" and .status=="True")) | select(.spec.unschedulable != true)] | length' <<<"$ready_nodes")
architectures=$(jq -r '[.items[].status.nodeInfo.architecture] | unique | join(",")' <<<"$ready_nodes")
if ((ready_count > 0)); then
  record candidate-nodes blocking true "$ready_count Ready+schedulable node(s), architectures=${architectures:-unknown}, selector=$node_selector"
else
  record candidate-nodes blocking false "no Ready+schedulable nodes for selector=$node_selector"
fi

case "$cpu_architecture" in
  x86_64) kubernetes_architecture=amd64 ;;
  aarch64) kubernetes_architecture=arm64 ;;
  *) kubernetes_architecture=invalid ;;
esac
if [[ "$architectures" == "$kubernetes_architecture" ]]; then
  record sandbox-cpu-architecture blocking true "controller advertises $cpu_architecture for Kubernetes architecture $architectures"
else
  record sandbox-cpu-architecture blocking false "configured=$cpu_architecture; candidate Kubernetes architectures=${architectures:-none}"
fi

runtime_json="$("${ocx[@]}" get runtimeclass -o json 2>/dev/null || echo '{"items":[]}')"
IFS=',' read -r -a requested_runtimes <<<"$runtime_classes"
for runtime_class_raw in "${requested_runtimes[@]}"; do
  runtime_class="${runtime_class_raw//[[:space:]]/}"
  if [[ "$runtime_class" != kata-clh && "$runtime_class" != kata-qemu ]]; then
    record "runtimeclass-$runtime_class" blocking false "controller only supports kata-clh and kata-qemu"
    continue
  fi
  handler=$(jq -r --arg name "$runtime_class" '.items[] | select(.metadata.name==$name) | .handler' <<<"$runtime_json" | head -1)
  if [[ -n "$handler" ]]; then
    record "runtimeclass-$runtime_class" blocking true "handler=$handler"
  else
    record "runtimeclass-$runtime_class" blocking false "not installed"
  fi
done

pods="$("${ocx[@]}" get pods -A -o json 2>/dev/null || echo '{"items":[]}')"
for runtime_class_raw in "${requested_runtimes[@]}"; do
  runtime_class="${runtime_class_raw//[[:space:]]/}"
  ready_runtime_count=$(jq --arg runtime "$runtime_class" '[.items[] | select(.spec.runtimeClassName==$runtime) | select(.status.phase=="Running") | select(any(.status.conditions[]?; .type=="Ready" and .status=="True"))] | length' <<<"$pods")
  if ((ready_runtime_count > 0)); then
    record "runtime-evidence-$runtime_class" evidence true "$ready_runtime_count Running+Ready Pod(s)"
  else
    record "runtime-evidence-$runtime_class" evidence false "no current Running+Ready Pod proves this RuntimeClass; run a canary before production cutover"
  fi
done

for resource in pods secrets serviceaccounts networkpolicies.networking.k8s.io statefulsets.apps roles.rbac.authorization.k8s.io rolebindings.rbac.authorization.k8s.io; do
  if "${ocx[@]}" auth can-i create "$resource" -n "$namespace" 2>/dev/null | grep -qx yes; then
    record "create-${resource//./-}" blocking true "current identity may create $resource in $namespace"
  else
    record "create-${resource//./-}" blocking false "current identity may not create $resource in $namespace"
  fi
done

sandbox_service_account="system:serviceaccount:${namespace}:e2b-sandbox"
if "${ocx[@]}" auth can-i use securitycontextconstraints.security.openshift.io/anyuid --as="$sandbox_service_account" 2>/dev/null | grep -qx yes; then
  record sandbox-anyuid blocking true "$sandbox_service_account may already use the non-privileged anyuid SCC"
elif "${ocx[@]}" auth can-i bind clusterrole/system:openshift:scc:anyuid -n "$namespace" 2>/dev/null | grep -qx yes; then
  record sandbox-anyuid blocking true "current identity may bind anyuid to the dedicated tokenless sandbox ServiceAccount"
else
  record sandbox-anyuid blocking false "envd requires UID 0 inside Kata, but anyuid is neither bound nor bindable"
fi

if "${ocx[@]}" api-resources --api-group=networking.k8s.io -o name 2>/dev/null | grep -qx networkpolicies.networking.k8s.io; then
  record network-policy-api blocking true "networking.k8s.io/v1 available"
else
  record network-policy-api blocking false "NetworkPolicy API unavailable"
fi

mcp_json="$("${ocx[@]}" get machineconfigpool -o json 2>/dev/null || echo '{"items":[]}')"
degraded_pools=$(jq -r '[.items[] | select(any(.status.conditions[]?; .type=="Degraded" and .status=="True")) | .metadata.name] | join(",")' <<<"$mcp_json")
updating_pools=$(jq -r '[.items[] | select(any(.status.conditions[]?; .type=="Updating" and .status=="True")) | .metadata.name] | join(",")' <<<"$mcp_json")
if [[ -z "$degraded_pools" && -z "$updating_pools" ]]; then
  record machine-config-pools blocking true "all pools stable"
else
  record machine-config-pools blocking false "degraded=${degraded_pools:-none}; updating=${updating_pools:-none}"
fi

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
if grep -q 'ServiceDiscoveryProviderKubernetes' "$repo_root/packages/api/internal/cfg/model.go" && \
   [[ -f "$repo_root/packages/api/internal/orchestrator/discovery/kubernetes.go" ]]; then
  record kubernetes-discovery-source blocking true "API supports SERVICE_DISCOVERY_PROVIDER=kubernetes"
else
  record kubernetes-discovery-source blocking false "Kubernetes discovery seam is missing"
fi
if [[ -f "$repo_root/packages/orchestrator/cmd/kubernetes-orchestrator/main.go" ]] && \
   [[ -f "$repo_root/packages/orchestrator/pkg/kubernetesserver/server.go" ]]; then
  record kata-controller-source blocking true "Kubernetes SandboxService implementation present"
else
  record kata-controller-source blocking false "Kubernetes SandboxService implementation missing"
fi

ready=false
if ((blocking == 0)); then ready=true; fi

if [[ "$output" == json ]]; then
  jq -n --arg context "$context" --arg server "$server" --arg namespace "$namespace" --arg selector "$node_selector" \
    --argjson ready "$ready" --argjson checks "$results" \
    '{provider:"okd-kata",context:$context,server:$server,namespace:$namespace,nodeSelector:$selector,ready:$ready,checks:$checks}'
else
  printf 'E2B OKD/Kata preflight: context=%s server=%s namespace=%s\n' "$context" "$server" "$namespace"
  jq -r '.[] | "[\(if .passed then "PASS" else "FAIL" end)] \(.level) \(.name): \(.detail)"' <<<"$results"
  printf 'READY=%s\n' "$ready"
fi

[[ "$ready" == true ]]
