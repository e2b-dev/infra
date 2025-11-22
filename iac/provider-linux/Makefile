ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := ../../.env.$(ENV)

-include ${ENV_FILE}

TF := $(shell which terraform)

ifdef DOCKER_IMAGE_PREFIX
API_IMAGE ?= $(DOCKER_IMAGE_PREFIX)/api:latest
DB_MIGRATOR_IMAGE ?= $(DOCKER_IMAGE_PREFIX)/db-migrator:latest
CLIENT_PROXY_IMAGE ?= $(DOCKER_IMAGE_PREFIX)/client-proxy:latest
DOCKER_REVERSE_PROXY_IMAGE ?= $(DOCKER_IMAGE_PREFIX)/docker-reverse-proxy:latest
endif

define tfvar
$(if $(value $(strip $(1))), TF_VAR_$(shell echo $(strip $(1)) | tr A-Z a-z)=$($(strip $(1))))
endef

tf_vars := \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
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
	$(call tfvar, LOGS_HEALTH_PROXY_PORT) \
	$(call tfvar, EDGE_API_SECRET) \
	$(call tfvar, API_ADMIN_TOKEN) \
	$(call tfvar, POSTGRES_CONNECTION_STRING) \
	$(call tfvar, SUPABASE_JWT_SECRETS) \
	$(call tfvar, POSTHOG_API_KEY) \
	$(call tfvar, ANALYTICS_COLLECTOR_HOST) \
	$(call tfvar, ANALYTICS_COLLECTOR_API_TOKEN) \
	$(call tfvar, REDIS_URL) \
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
	$(call tfvar, ORCHESTRATOR_NODE_POOL) \
	$(call tfvar, BUILDER_NODE_POOL) \
	$(call tfvar, TEMPLATE_BUCKET_NAME) \
	$(call tfvar, BUILD_CACHE_BUCKET_NAME) \
	$(call tfvar, ENVD_TIMEOUT) \
	$(call tfvar, ALLOW_SANDBOX_INTERNET) \
	$(call tfvar, SHARED_CHUNK_CACHE_PATH) \
	$(call tfvar, DOCKERHUB_REMOTE_REPOSITORY_URL) \
	$(call tfvar, REDIS_TLS_CA_BASE64) \
	$(call tfvar, REDIS_SECURE_CLUSTER_URL) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_MEMORY_MB) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_CPU_COUNT) \
	$(call tfvar, LOKI_RESOURCES_MEMORY_MB) \
	$(call tfvar, LOKI_RESOURCES_CPU_COUNT) \
	$(call tfvar, TEMPLATE_MANAGER_MACHINE_COUNT) \
	$(call tfvar, ORCHESTRATOR_PROXY_PORT) \
	$(call tfvar, CLICKHOUSE_DATABASE) \
	$(call tfvar, CLICKHOUSE_SERVER_PORT) \
	$(call tfvar, CLICKHOUSE_RESOURCES_MEMORY_MB) \
	$(call tfvar, CLICKHOUSE_RESOURCES_CPU_COUNT) \
	$(call tfvar, CLICKHOUSE_METRICS_PORT)

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
	$(eval TARGET := $(shell cat main.tf | grep "^module" | awk '{print $$2}' | tr ' ' '\n' | grep -v -e "nomad" | awk '{print "-target=module." $$0 ""}' | xargs))
	$(tf_vars) \
	$(TF) plan \
	-out=.tfplan.$(ENV) \
	-input=false \
	-compact-warnings \
	$(TARGET)