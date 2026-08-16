.PHONY: provider-login init plan plan-without-jobs plan-only-jobs render build apply switch

OC ?= oc
OKD_CONTEXT ?= indentia-ap
OKD_NAMESPACE ?= e2b
OKD_NODE_SELECTOR ?= katacontainers.io/kata-runtime=true
KATA_RUNTIME_CLASSES ?= kata-clh,kata-qemu
DEFAULT_KATA_RUNTIME_CLASS ?= kata-clh
CONTROLLER_IMAGE ?=
SANDBOX_IMAGE_TEMPLATE ?=
SANDBOX_CPU_ARCHITECTURE ?= x86_64
SANDBOX_CAPACITY_CPU ?= 64
SANDBOX_CAPACITY_MEMORY_MIB ?= 131072
SANDBOX_DNS_NAMESERVERS ?= 8.8.8.8
KATA_NODE_LABELS ?= default,kubernetes

provider-login:
	@$(OC) --context "$(OKD_CONTEXT)" whoami >/dev/null
	@$(OC) --context "$(OKD_CONTEXT)" whoami --show-server

init plan plan-without-jobs plan-only-jobs:
	@OC='$(OC)' ./preflight.sh --context "$(OKD_CONTEXT)" --namespace "$(OKD_NAMESPACE)" --node-selector "$(OKD_NODE_SELECTOR)" --runtime-classes "$(KATA_RUNTIME_CLASSES)" --cpu-architecture "$(SANDBOX_CPU_ARCHITECTURE)"
	@$(OC) kustomize manifests >/dev/null
	@echo "Kata controller manifests render successfully; no cluster state was changed."

render:
	@OC='$(OC)' OKD_NAMESPACE='$(OKD_NAMESPACE)' OKD_NODE_SELECTOR='$(OKD_NODE_SELECTOR)' KATA_RUNTIME_CLASSES='$(KATA_RUNTIME_CLASSES)' DEFAULT_KATA_RUNTIME_CLASS='$(DEFAULT_KATA_RUNTIME_CLASS)' CONTROLLER_IMAGE='$(CONTROLLER_IMAGE)' SANDBOX_IMAGE_TEMPLATE='$(SANDBOX_IMAGE_TEMPLATE)' SANDBOX_CPU_ARCHITECTURE='$(SANDBOX_CPU_ARCHITECTURE)' SANDBOX_CAPACITY_CPU='$(SANDBOX_CAPACITY_CPU)' SANDBOX_CAPACITY_MEMORY_MIB='$(SANDBOX_CAPACITY_MEMORY_MIB)' SANDBOX_DNS_NAMESERVERS='$(SANDBOX_DNS_NAMESERVERS)' KATA_NODE_LABELS='$(KATA_NODE_LABELS)' ./render.sh

build:
	@test -n "$(CONTROLLER_IMAGE)" || { echo "CONTROLLER_IMAGE is required" >&2; exit 2; }
	docker build -f Dockerfile -t "$(CONTROLLER_IMAGE)" ../..

apply:
	@test "$(OKD_APPLY)" = YES || { echo "Refusing cluster mutation: set OKD_APPLY=YES after reviewing 'make render'." >&2; exit 2; }
	@OC='$(OC)' ./preflight.sh --context "$(OKD_CONTEXT)" --namespace "$(OKD_NAMESPACE)" --node-selector "$(OKD_NODE_SELECTOR)" --runtime-classes "$(KATA_RUNTIME_CLASSES)" --cpu-architecture "$(SANDBOX_CPU_ARCHITECTURE)"
	@OC='$(OC)' OKD_NAMESPACE='$(OKD_NAMESPACE)' OKD_NODE_SELECTOR='$(OKD_NODE_SELECTOR)' KATA_RUNTIME_CLASSES='$(KATA_RUNTIME_CLASSES)' DEFAULT_KATA_RUNTIME_CLASS='$(DEFAULT_KATA_RUNTIME_CLASS)' CONTROLLER_IMAGE='$(CONTROLLER_IMAGE)' SANDBOX_IMAGE_TEMPLATE='$(SANDBOX_IMAGE_TEMPLATE)' SANDBOX_CPU_ARCHITECTURE='$(SANDBOX_CPU_ARCHITECTURE)' SANDBOX_CAPACITY_CPU='$(SANDBOX_CAPACITY_CPU)' SANDBOX_CAPACITY_MEMORY_MIB='$(SANDBOX_CAPACITY_MEMORY_MIB)' SANDBOX_DNS_NAMESERVERS='$(SANDBOX_DNS_NAMESERVERS)' KATA_NODE_LABELS='$(KATA_NODE_LABELS)' ./render.sh | $(OC) --context "$(OKD_CONTEXT)" apply -f -

switch:
	@echo "provider-okd does not mutate kubeconfig; pass OKD_CONTEXT=<context> explicitly."
