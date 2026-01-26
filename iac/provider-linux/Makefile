ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := ../../.env.$(ENV)

-include ${ENV_FILE}

TF := $(shell which terraform)

ifdef DOCKER_IMAGE_PREFIX
ifeq ($(strip $(API_IMAGE)),)
API_IMAGE := $(DOCKER_IMAGE_PREFIX)/api:latest
endif
ifeq ($(strip $(DB_MIGRATOR_IMAGE)),)
DB_MIGRATOR_IMAGE := $(DOCKER_IMAGE_PREFIX)/db-migrator:latest
endif
ifeq ($(strip $(CLIENT_PROXY_IMAGE)),)
CLIENT_PROXY_IMAGE := $(DOCKER_IMAGE_PREFIX)/client-proxy:latest
endif
ifeq ($(strip $(DOCKER_REVERSE_PROXY_IMAGE)),)
DOCKER_REVERSE_PROXY_IMAGE := $(DOCKER_IMAGE_PREFIX)/docker-reverse-proxy:latest
endif
endif

define tfvar
$(if $(value $(strip $(1))), TF_VAR_$(shell echo $(strip $(1)) | tr A-Z a-z)=$($(strip $(1))))
endef

tf_vars := \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	$(call tfvar, DOMAIN_NAME) \
	$(call tfvar, DATACENTER) \
	$(call tfvar, NOMAD_ADDRESS) \
	$(call tfvar, NOMAD_ACL_TOKEN) \
	$(call tfvar, CONSUL_ACL_TOKEN) \
	$(call tfvar, SERVERS_JSON) \
	$(call tfvar, CLIENTS_JSON) \
	$(call tfvar, API_NODE_POOL) \
	$(call tfvar, INGRESS_COUNT) \
	$(call tfvar, API_MACHINE_COUNT) \
	$(call tfvar, API_RESOURCES_CPU_COUNT) \
	$(call tfvar, API_RESOURCES_MEMORY_MB) \
	$(call tfvar, API_PORT) \
	$(call tfvar, INGRESS_PORT) \
	$(call tfvar, EDGE_API_PORT) \
	$(call tfvar, EDGE_PROXY_PORT) \
	$(call tfvar, LOGS_PROXY_PORT) \
	$(call tfvar, LOKI_SERVICE_PORT) \
	$(call tfvar, GRAFANA_SERVICE_PORT) \
	$(call tfvar, GRAFANA_RESOURCES_MEMORY_MB) \
	$(call tfvar, GRAFANA_RESOURCES_CPU_COUNT) \
	$(call tfvar, ENABLE_NODES_UNINSTALL) \
	$(call tfvar, UNINSTALL_VERSION) \
	$(call tfvar, UNINSTALL_CONFIRM_PHRASE) \
	$(call tfvar, ENABLE_NOMAD_JOBS) \
	$(call tfvar, LOGS_HEALTH_PROXY_PORT) \
	$(call tfvar, EDGE_API_SECRET) \
	$(call tfvar, API_ADMIN_TOKEN) \
	$(call tfvar, POSTGRES_CONNECTION_STRING) \
	$(call tfvar, SUPABASE_JWT_SECRETS) \
	$(call tfvar, POSTHOG_API_KEY) \
	$(call tfvar, ANALYTICS_COLLECTOR_HOST) \
	$(call tfvar, ANALYTICS_COLLECTOR_API_TOKEN) \
	$(call tfvar, REDIS_URL) \
	$(call tfvar, REDIS_SECURE_CLUSTER_URL) \
	$(call tfvar, LAUNCH_DARKLY_API_KEY) \
	$(call tfvar, API_IMAGE) \
	$(call tfvar, DB_MIGRATOR_IMAGE) \
	$(call tfvar, CLIENT_PROXY_IMAGE) \
	$(call tfvar, DOCKER_REVERSE_PROXY_IMAGE) \
	$(call tfvar, ORCHESTRATOR_PORT) \
	$(call tfvar, TEMPLATE_MANAGER_PORT) \
	$(call tfvar, OTEL_COLLECTOR_GRPC_PORT) \
	$(call tfvar, API_SECRET) \
	$(call tfvar, ORCHESTRATOR_ARTIFACT_URL) \
	$(call tfvar, TEMPLATE_MANAGER_ARTIFACT_URL) \
	$(call tfvar, ENVD_ARTIFACT_URL) \
	$(call tfvar, ORCHESTRATOR_NODE_POOL) \
	$(call tfvar, BUILDER_NODE_POOL) \
	$(call tfvar, TEMPLATE_BUCKET_NAME) \
	$(call tfvar, BUILD_CACHE_BUCKET_NAME) \
	$(call tfvar, OTEL_TRACING_PRINT) \
	$(call tfvar, ENVD_TIMEOUT) \
	$(call tfvar, ALLOW_SANDBOX_INTERNET) \
	$(call tfvar, SHARED_CHUNK_CACHE_PATH) \
	$(call tfvar, DOCKERHUB_REMOTE_REPOSITORY_URL) \
	$(call tfvar, DOCKERHUB_REMOTE_REPOSITORY_PROVIDER) \
	$(call tfvar, REDIS_TLS_CA_BASE64) \
	$(call tfvar, REDIS_SECURE_CLUSTER_URL) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_MEMORY_MB) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_CPU_COUNT) \
	$(call tfvar, LOKI_RESOURCES_MEMORY_MB) \
	$(call tfvar, LOKI_RESOURCES_CPU_COUNT) \
	$(call tfvar, TEMPLATE_MANAGER_MACHINE_COUNT) \
	$(call tfvar, ORCHESTRATOR_PROXY_PORT) \
	$(call tfvar, FC_ARTIFACT_NODE_POOLS) \
	$(call tfvar, CLICKHOUSE_DATABASE) \
	$(call tfvar, CLICKHOUSE_SERVER_PORT) \
	$(call tfvar, CLICKHOUSE_RESOURCES_MEMORY_MB) \
	$(call tfvar, CLICKHOUSE_RESOURCES_CPU_COUNT) \
	$(call tfvar, CLICKHOUSE_METRICS_PORT) \
	$(call tfvar, SANDBOX_ACCESS_TOKEN_HASH_SEED) \
	$(call tfvar, SANDBOX_HYPERLOOP_PROXY_PORT) \
	$(call tfvar, USE_LOCAL_NAMESPACE_STORAGE) \
	$(call tfvar, USE_NFS_SHARE_STORAGE) \
	$(call tfvar, NFS_SERVER_IP) \
	$(call tfvar, ENABLE_NETWORK_POLICY) \
	$(call tfvar, NETWORK_OPEN_PORTS) \
	$(call tfvar, KERNEL_SOURCE_BASE_URL) \
	$(call tfvar, FIRECRACKER_SOURCE_BASE_URL)

# Pass-through for Docker image prefix and artifact HTTP server
tf_vars += \
	$(call tfvar, DOCKER_IMAGE_PREFIX) \
	$(call tfvar, DOCKER_HTTP_PROXY) \
	$(call tfvar, DOCKER_HTTPS_PROXY) \
	$(call tfvar, DOCKER_NO_PROXY) \
	$(call tfvar, ARTIFACT_SCP_HOST) \
	$(call tfvar, ARTIFACT_SCP_USER) \
	$(call tfvar, ARTIFACT_SCP_DIR) \
	$(call tfvar, ARTIFACT_SCP_SSH_KEY) \
	$(call tfvar, ARTIFACT_SCP_PORT)
tf_vars += \
	$(call tfvar, ENABLE_ARTIFACT_SCP_SERVER)

.PHONY: init
init:
	@ printf "Initializing Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) init -input=false -reconfigure

.PHONY: plan
plan:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings

.PHONY: apply
apply:
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(tf_vars) \
	$(TF) apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)

.PHONY: plan-only-jobs
plan-only-jobs:
	@ printf "Planning Terraform (jobs only) for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad

.PHONY: plan-only-jobs/%
plan-only-jobs/%:
	@ printf "Planning Terraform (job: $(notdir $@)) for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad.nomad_job.$(subst -,_,$(notdir $@))

.PHONY: plan-without-jobs
plan-without-jobs:
	@ printf "Planning Terraform (without jobs) for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) TF_VAR_enable_nomad_jobs=false $(TF) plan -out=.tfplan.$(ENV) -input=false -compact-warnings -refresh=false

# Extract hosts from environment JSON variables using external script
# Create a temporary script to avoid Makefile quoting issues
CLIENT_HOSTS := $(shell python3 -c "import json, re; j='$(CLIENTS_JSON)'; j=re.sub(r'\\\\\n', '', j); print(' '.join([h['host'] for h in json.loads(j)]))" 2>/dev/null || echo "192.168.123.82 192.168.123.84 192.168.123.85")
SERVER_HOSTS := $(shell python3 -c "import json, re; j='$(SERVERS_JSON)'; j=re.sub(r'\\\\\n', '', j); print(' '.join([h['host'] for h in json.loads(j)]))" 2>/dev/null || echo "192.168.123.82 192.168.123.83 192.168.123.84")
ALL_HOSTS := $(sort $(CLIENT_HOSTS) $(SERVER_HOSTS))

# Generate target resources for all hosts  
define firewall_targets
$(foreach host,$(ALL_HOSTS),-target='module.machines.null_resource.nodes_firewall["$(host)"]')
endef

# Firewall-specific targets for network policy management
.PHONY: plan-firewall
plan-firewall:
	@ printf "Planning firewall resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ printf "Found hosts: $(ALL_HOSTS)\n\n"
	@ $(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV).firewall -compact-warnings $(call firewall_targets)

.PHONY: apply-firewall
apply-firewall:
	@ printf "Applying firewall resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(tf_vars) \
	$(TF) apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	.tfplan.$(ENV).firewall
	@ rm .tfplan.$(ENV).firewall

.PHONY: taint-firewall
taint-firewall:
	@ printf "Tainting firewall resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ printf "Found hosts: $(ALL_HOSTS)\n\n"
	@ $(foreach host,$(ALL_HOSTS), \
		$(tf_vars) $(TF) taint 'module.machines.null_resource.nodes_firewall["$(host)"]' || true; \
	)

# Single node firewall targets for granular control
.PHONY: plan-firewall/%
plan-firewall/%:
	@ printf "Planning firewall for node: `tput setaf 2``tput bold`$(notdir $@)`tput sgr0` for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV).firewall.$(notdir $@) -compact-warnings \
		-target='module.machines.null_resource.nodes_firewall["$(notdir $@)"]'

.PHONY: apply-firewall/%
apply-firewall/%:
	@ printf "Applying firewall for node: `tput setaf 2``tput bold`$(notdir $@)`tput sgr0` for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(tf_vars) \
	$(TF) apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	.tfplan.$(ENV).firewall.$(notdir $@)
	@ rm .tfplan.$(ENV).firewall.$(notdir $@)

.PHONY: taint-firewall/%
taint-firewall/%:
	@ printf "Tainting firewall for node: `tput setaf 2``tput bold`$(notdir $@)`tput sgr0` for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) taint 'module.machines.null_resource.nodes_firewall["$(notdir $@)"]' || true

# Helper target to show detected hosts
.PHONY: show-firewall-hosts
show-firewall-hosts:
	@ printf "Firewall hosts detected for env `tput setaf 2``tput bold`$(ENV)`tput sgr0`:\n\n"
	@ printf "Clients: $(CLIENT_HOSTS)\n"
	@ printf "Servers: $(SERVER_HOSTS)\n"
	@ printf "All hosts: $(ALL_HOSTS)\n"

.PHONY: uninstall
uninstall:
	@ printf "Uninstalling provider-linux for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ # Remove Nomad jobs from state to avoid connection refused errors during destruction
	@ $(TF) state list | grep "module.nomad.nomad_job" | xargs -I {} $(TF) state rm {} || true
	@ $(tf_vars) TF_VAR_enable_nodes_uninstall=true $(TF) apply -auto-approve -input=false -compact-warnings
