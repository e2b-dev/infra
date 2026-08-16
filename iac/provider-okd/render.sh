#!/usr/bin/env bash
set -euo pipefail

controller_image="${CONTROLLER_IMAGE:-}"
sandbox_image_template="${SANDBOX_IMAGE_TEMPLATE:-}"
sandbox_cpu_architecture="${SANDBOX_CPU_ARCHITECTURE:-x86_64}"
namespace="${OKD_NAMESPACE:-e2b}"
node_selector="${OKD_NODE_SELECTOR:-katacontainers.io/kata-runtime=true}"
runtime_classes="${KATA_RUNTIME_CLASSES:-kata-clh,kata-qemu}"
default_runtime_class="${DEFAULT_KATA_RUNTIME_CLASS:-kata-clh}"
sandbox_capacity_cpu="${SANDBOX_CAPACITY_CPU:-64}"
sandbox_capacity_memory_mib="${SANDBOX_CAPACITY_MEMORY_MIB:-131072}"
sandbox_dns_nameservers="${SANDBOX_DNS_NAMESERVERS:-8.8.8.8}"
node_labels="${KATA_NODE_LABELS:-default,kubernetes}"
oc_bin="${OC:-oc}"

[[ -n "$controller_image" ]] || { echo "CONTROLLER_IMAGE is required" >&2; exit 2; }
[[ -n "$sandbox_image_template" ]] || { echo "SANDBOX_IMAGE_TEMPLATE is required" >&2; exit 2; }
[[ "$controller_image" =~ ^[A-Za-z0-9._/@:-]+$ ]] || { echo "CONTROLLER_IMAGE contains unsupported characters" >&2; exit 2; }
[[ "$sandbox_image_template" =~ ^[A-Za-z0-9._/@:{}-]+$ ]] || { echo "SANDBOX_IMAGE_TEMPLATE contains unsupported characters" >&2; exit 2; }
[[ "$sandbox_image_template" == *'{template_id}'* ]] || { echo "SANDBOX_IMAGE_TEMPLATE must contain {template_id}" >&2; exit 2; }
[[ "$sandbox_image_template" == *'{build_id}'* ]] || { echo "SANDBOX_IMAGE_TEMPLATE must contain {build_id}" >&2; exit 2; }
[[ "$sandbox_cpu_architecture" == x86_64 || "$sandbox_cpu_architecture" == aarch64 ]] || { echo "SANDBOX_CPU_ARCHITECTURE must be x86_64 or aarch64" >&2; exit 2; }
[[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ && ${#namespace} -le 63 ]] || { echo "OKD_NAMESPACE must be a Kubernetes DNS label" >&2; exit 2; }
[[ "$node_selector" =~ ^[A-Za-z0-9._/-]+=[A-Za-z0-9._-]+(,[A-Za-z0-9._/-]+=[A-Za-z0-9._-]+)*$ ]] || { echo "OKD_NODE_SELECTOR must be a comma-separated key=value selector" >&2; exit 2; }
[[ "$runtime_classes" =~ ^(kata-clh|kata-qemu)(,(kata-clh|kata-qemu))*$ ]] || { echo "KATA_RUNTIME_CLASSES supports only kata-clh and kata-qemu" >&2; exit 2; }
[[ "$default_runtime_class" == kata-clh || "$default_runtime_class" == kata-qemu ]] || { echo "DEFAULT_KATA_RUNTIME_CLASS must be kata-clh or kata-qemu" >&2; exit 2; }
[[ ",$runtime_classes," == *",$default_runtime_class,"* ]] || { echo "DEFAULT_KATA_RUNTIME_CLASS must be in KATA_RUNTIME_CLASSES" >&2; exit 2; }
[[ "$sandbox_capacity_cpu" =~ ^[1-9][0-9]*$ ]] || { echo "SANDBOX_CAPACITY_CPU must be a positive integer" >&2; exit 2; }
[[ "$sandbox_capacity_memory_mib" =~ ^[1-9][0-9]*$ ]] || { echo "SANDBOX_CAPACITY_MEMORY_MIB must be a positive integer" >&2; exit 2; }
[[ "$sandbox_dns_nameservers" =~ ^[0-9A-Fa-f:.,]+$ ]] || { echo "SANDBOX_DNS_NAMESERVERS must be a comma-separated IP list" >&2; exit 2; }
[[ "$node_labels" =~ ^[A-Za-z0-9._:-]+(,[A-Za-z0-9._:-]+)*$ ]] || { echo "KATA_NODE_LABELS must be a comma-separated label list" >&2; exit 2; }

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
"$oc_bin" kustomize "$script_dir/manifests" | sed \
  -e "s|controller.invalid/e2b-kubernetes-orchestrator:replace|$controller_image|g" \
  -e "s|sandbox.invalid/e2b/{template_id}:{build_id}|$sandbox_image_template|g" \
  -e "s|architecture.invalid|$sandbox_cpu_architecture|g" \
  -e "s|selector.invalid=true|$node_selector|g" \
  -e "s|runtimes.invalid|$runtime_classes|g" \
  -e "s|labels.invalid|$node_labels|g" \
  -e "s|runtime-default.invalid|$default_runtime_class|g" \
  -e "s|capacity-cpu.invalid|\"$sandbox_capacity_cpu\"|g" \
  -e "s|capacity-memory.invalid|\"$sandbox_capacity_memory_mib\"|g" \
  -e "s|dns.invalid|$sandbox_dns_nameservers|g" \
  -e "s|^  namespace: e2b$|  namespace: $namespace|g" \
  -e "s|^  name: e2b$|  name: $namespace|g" \
  -e "s|kubernetes.io/metadata.name: e2b$|kubernetes.io/metadata.name: $namespace|g"
