ENV := $(shell cat ../../.last_used_env 2>/dev/null || echo "not-set")
ENV_FILE := ../../.env.${ENV}

-include ${ENV_FILE}

TF := $(shell which terraform)
GCLOUD := $(shell which gcloud)
override EXPECTED_TERRAFORM_VERSION := $(shell awk '$$1 == "terraform" { print $$2; exit }' ../../.tool-versions)
override TF_VAR_FILE := ./.terraform.${ENV}.tfvars
override TF_VAR_FILE_ARG := $(if $(wildcard $(TF_VAR_FILE)),-var-file=$(TF_VAR_FILE))
TERRAFORM_STATE_BUCKET ?= $(GCP_PROJECT_ID)-terraform-state
override TERRAFORM_STATE_PREFIX := terraform/orchestration/$(ENV)/state
TEMPLATE_BUCKET_LOCATION ?= $(GCP_REGION)
BUCKET_PREFIX ?= $(GCP_PROJECT_ID)-

SKIP_FMT ?= false
define tf_fmt
$(if $(filter true,$(SKIP_FMT)),,$(TF) fmt -recursive)
endef

# Set the terraform environment variable only if the environment variable is set
# Strip the passed variable name (it's space sensitive) and check if the variable is set, if yes return TF_VAR_<variable_name>=<value> with the variable name in lower case
define tfvar
$(if $(value $(strip $(1))), TF_VAR_$(shell echo $(strip $(1)) | tr A-Z a-z)=$($(strip $(1))))
endef

tf_vars := \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	$(call tfvar, BUILD_CLUSTERS_CONFIG) \
	$(call tfvar, CLIENT_CLUSTERS_CONFIG) \
	$(call tfvar, API_MACHINE_TYPE) \
	$(call tfvar, API_CLUSTER_SIZE) \
	$(call tfvar, API_USE_NAT) \
	$(call tfvar, API_NAT_IPS) \
	$(call tfvar, OS_LOGIN_OPERATOR_ACCESS_CONFIRMED) \
	$(call tfvar, NETWORK_HARDENING_ROLLOUT_STAGE) \
	$(call tfvar, NETWORK_HARDENING_ROLLOUT_WAIT_SECONDS) \
	$(call tfvar, MONAD_WORKER_AUTOSCALER_SHADOW_ENABLED) \
	$(call tfvar, MONAD_WORKER_AUTOSCALER_REVISION) \
	$(call tfvar, MONAD_WORKER_AUTOSCALER_TAMS_CAPACITY_URL) \
	$(call tfvar, MONAD_WORKER_AUTOSCALER_TAMS_AUDIENCE) \
	$(call tfvar, MONAD_WORKER_AUTOSCALER_ALLOCATIONS) \
	$(call tfvar, SERVER_MACHINE_TYPE) \
	$(call tfvar, SERVER_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_MACHINE_TYPE) \
	$(call tfvar, LOKI_MACHINE_TYPE) \
	$(call tfvar, LOKI_CLUSTER_SIZE) \
	$(call tfvar, GCP_PROJECT_ID) \
	$(call tfvar, GCP_REGION) \
	$(call tfvar, GCP_ZONE) \
	$(call tfvar, DOMAIN_NAME) \
	$(call tfvar, PREFIX) \
	$(call tfvar, CORE_IMAGE_REVISION) \
	$(call tfvar, ORCHESTRATOR_ENABLED) \
	$(call tfvar, API_INTERNAL_GRPC_PORT) \
	$(call tfvar, ALLOW_SANDBOX_INTERNAL_CIDRS) \
	$(call tfvar, API_SERVER_COUNT) \
	$(call tfvar, API_RESOURCES_CPU_COUNT) \
	$(call tfvar, API_RESOURCES_MEMORY_MB) \
	$(call tfvar, INGRESS_COUNT) \
	$(call tfvar, CLIENT_PROXY_COUNT) \
	$(call tfvar, CLIENT_PROXY_RESOURCES_CPU_COUNT) \
	$(call tfvar, CLIENT_PROXY_RESOURCES_MEMORY_MB) \
	$(call tfvar, CLICKHOUSE_RESOURCES_CPU_COUNT) \
	$(call tfvar, CLICKHOUSE_RESOURCES_MEMORY_MB) \
	$(call tfvar, CLIENT_PROXY_UPDATE_MAX_PARALLEL) \
	$(call tfvar, LOKI_RESOURCES_CPU_COUNT) \
	$(call tfvar, LOKI_RESOURCES_MEMORY_MB) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_CPU_COUNT) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_MEMORY_MB) \
	$(call tfvar, ENABLE_OTEL_ROUTER_LOGS) \
	$(call tfvar, OTEL_ROUTER_HTTP_PORT) \
	$(call tfvar, ENABLE_OTEL_ROUTER_METRICS) \
	$(call tfvar, OTEL_ROUTER_GRPC_PORT) \
	$(call tfvar, ENABLE_GCP_TELEMETRY_METRICS) \
	$(call tfvar, ENABLE_GCP_TELEMETRY_EXTERNAL_METRICS) \
	$(call tfvar, TEMPLATE_BUCKET_NAME) \
	$(call tfvar, TEMPLATE_BUCKET_LOCATION) \
	$(call tfvar, ENVD_TIMEOUT) \
	$(call tfvar, REDIS_MANAGED) \
	$(call tfvar, REDIS_SHARD_COUNT) \
	$(call tfvar, GCP_REDIS_NODE_TYPE) \
	$(call tfvar, GCP_REDIS_ENGINE_VERSION) \
	$(call tfvar, GRAFANA_MANAGED) \
	$(call tfvar, FILESTORE_CACHE_ENABLED) \
	$(call tfvar, FILESTORE_CACHE_TIER) \
	$(call tfvar, FILESTORE_CACHE_CAPACITY_GB) \
	$(call tfvar, FILESTORE_MAX_DISK_USAGE_TARGET) \
	$(call tfvar, REMOTE_REPOSITORY_ENABLED) \
	$(call tfvar, API_BOOT_DISK_TYPE) \
	$(call tfvar, SERVER_BOOT_DISK_TYPE) \
	$(call tfvar, SERVER_BOOT_DISK_SIZE_GB) \
	$(call tfvar, CLICKHOUSE_BOOT_DISK_TYPE) \
	$(call tfvar, CLICKHOUSE_STATEFUL_DISK_TYPE) \
	$(call tfvar, CLICKHOUSE_STATEFUL_DISK_SIZE_GB) \
	$(call tfvar, BUCKET_PREFIX) \
	$(call tfvar, LOKI_BOOT_DISK_TYPE) \
	$(call tfvar, LOKI_USE_V13_SCHEMA_FROM) \
	$(call tfvar, DASHBOARD_API_COUNT) \
	$(call tfvar, DEFAULT_PERSISTENT_VOLUME_TYPE) \
	$(call tfvar, PERSISTENT_VOLUME_TYPES) \
	$(call tfvar, DB_MAX_OPEN_CONNECTIONS) \
	$(call tfvar, DB_MIN_IDLE_CONNECTIONS) \
	$(call tfvar, AUTH_DB_MAX_OPEN_CONNECTIONS) \
	$(call tfvar, AUTH_DB_MIN_IDLE_CONNECTIONS) \
	$(call tfvar, CLIENT_PROXY_OIDC_ISSUER_URL) \
	$(call tfvar, ORY_SDK_URL) \
	$(call tfvar, ORY_ISSUER_URL) \
	$(call tfvar, GCS_GRPC_CONNECTION_POOL_SIZE) \
	$(call tfvar, ADDITIONAL_API_PATHS_HANDLED_BY_INGRESS) \
	$(call tfvar, ANYWHERE_CACHE_ENABLED)

.PHONY: init
init:
	@ printf "Refusing legacy init in the Monad fork. Use 'make foundation-init' then review 'make foundation-plan'.\n"
	@ exit 1

FOUNDATION_PLAN := .tfplan.foundation.$(ENV)
FOUNDATION_PLAN_MANIFEST := $(FOUNDATION_PLAN).manifest
FOUNDATION_DESTROY_PLAN := .tfplan.foundation-destroy.$(ENV)
override WORKLOAD_PLAN := .tfplan.workload.$(ENV)
override WORKLOAD_PLAN_MANIFEST := $(WORKLOAD_PLAN).manifest
override WORKLOAD_PREREQUISITE_PLAN := .tfplan.workload-prerequisite.$(ENV)
override WORKLOAD_PREREQUISITE_PLAN_MANIFEST := $(WORKLOAD_PREREQUISITE_PLAN).manifest
WORKLOAD_CLUSTER_STAGE ?=
WORKLOAD_CLUSTER_CHECKPOINT ?=
WORKLOAD_CLUSTER_RECOVERY_TOKEN ?=
WORKLOAD_CLUSTER_REBOUND_TOKEN ?=
override WORKLOAD_CLUSTER_PLAN := .tfplan.workload-cluster.$(WORKLOAD_CLUSTER_STAGE).$(ENV)
override WORKLOAD_CLUSTER_PLAN_MANIFEST := $(WORKLOAD_CLUSTER_PLAN).manifest
override WORKLOAD_CLUSTER_TOPOLOGY_TARGET := -target=module.cluster
override WORKLOAD_CLUSTER_STAGE_COMPLETION_network := module.cluster.terraform_data.network_hardening_rollout_completion_network
override WORKLOAD_CLUSTER_STAGE_COMPLETION_server := module.cluster.terraform_data.network_hardening_rollout_completion_server[0]
override WORKLOAD_CLUSTER_STAGE_COMPLETION_api := module.cluster.terraform_data.network_hardening_rollout_completion_api[0]
override WORKLOAD_CLUSTER_STAGE_COMPLETION_worker := module.cluster.terraform_data.network_hardening_rollout_completion_worker[0]
override WORKLOAD_CLUSTER_STAGE_COMPLETION_build := module.cluster.terraform_data.network_hardening_rollout_completion_build[0]
override WORKLOAD_CLUSTER_STAGE_MARKER_network := module.cluster.terraform_data.network_hardening_rollout_stage_network
override WORKLOAD_CLUSTER_STAGE_MARKER_server := module.cluster.terraform_data.network_hardening_rollout_stage_server[0]
override WORKLOAD_CLUSTER_STAGE_MARKER_api := module.cluster.terraform_data.network_hardening_rollout_stage_api[0]
override WORKLOAD_CLUSTER_STAGE_MARKER_worker := module.cluster.terraform_data.network_hardening_rollout_stage_worker[0]
override WORKLOAD_CLUSTER_STAGE_MARKER_build := module.cluster.terraform_data.network_hardening_rollout_stage_build[0]
override WORKLOAD_CLUSTER_COMPLETION_ADDRESS := $(WORKLOAD_CLUSTER_STAGE_COMPLETION_$(WORKLOAD_CLUSTER_STAGE))
override WORKLOAD_CLUSTER_MARKER_ADDRESS := $(WORKLOAD_CLUSTER_STAGE_MARKER_$(WORKLOAD_CLUSTER_STAGE))
override WORKLOAD_CLUSTER_COMPLETION_REPLACE := -replace='$(WORKLOAD_CLUSTER_COMPLETION_ADDRESS)'
override WORKLOAD_CLUSTER_COMMON_TARGETS := \
	-target='module.cluster.terraform_data.os_login_operator_access_guard' \
	-target='module.cluster.terraform_data.network_hardening_rollout_completion' \
	-target='module.cluster.terraform_data.network_hardening_rollout_stage' \
	-target='module.cluster.terraform_data.network_hardening_rollout_completion_network' \
	-target='module.cluster.terraform_data.network_hardening_rollout_stage_network' \
	-target='$(WORKLOAD_CLUSTER_COMPLETION_ADDRESS)' \
	-target='$(WORKLOAD_CLUSTER_MARKER_ADDRESS)'
override WORKLOAD_CLUSTER_STAGE_TARGETS_network := \
	-target='module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]' \
	-target='module.cluster.module.network.google_compute_firewall.internal_remote_connection_firewall_ingress' \
	-target='module.cluster.module.network.google_compute_firewall.remote_connection_firewall_ingress'
override WORKLOAD_CLUSTER_STAGE_TARGETS_server := \
	-target='module.cluster.google_compute_health_check.server_nomad_check' \
	-target='module.cluster.google_compute_instance_template.server' \
	-target='module.cluster.google_compute_region_instance_group_manager.server_pool'
override WORKLOAD_CLUSTER_STAGE_TARGETS_api := \
	-target='module.cluster.google_compute_instance_template.api' \
	-target='module.cluster.google_compute_instance_group_manager.api_pool'
override WORKLOAD_CLUSTER_STAGE_TARGETS_worker := \
	-target='module.cluster.module.client_cluster["default"].google_compute_instance_template.template' \
	-target='module.cluster.module.client_cluster["default"].google_compute_region_instance_group_manager.pool'
override WORKLOAD_CLUSTER_STAGE_TARGETS_build := \
	-target='module.cluster.google_compute_instance_template.clickhouse' \
	-target='module.cluster.google_compute_instance_group_manager.clickhouse_pool' \
	-target='module.cluster.google_compute_instance_template.loki' \
	-target='module.cluster.google_compute_instance_group_manager.loki_pool' \
	-target='module.cluster.module.build_cluster["default"].google_compute_instance_template.template' \
	-target='module.cluster.module.build_cluster["default"].google_compute_region_instance_group_manager.pool'
override WORKLOAD_CLUSTER_TARGETS := $(WORKLOAD_CLUSTER_COMMON_TARGETS) $(WORKLOAD_CLUSTER_STAGE_TARGETS_$(WORKLOAD_CLUSTER_STAGE))
override WORKLOAD_TOPOLOGY_POLICY := topology/minimal-workload-policy.json
override WORKLOAD_PACKER_TEMPLATE := nomad-cluster-disk-image/main.pkr.hcl
override WORKLOAD_IMAGE_REVISION := $(CORE_IMAGE_REVISION)
override WORKLOAD_JOB_BINARY_BUCKET := $(BUCKET_PREFIX)fc-env-pipeline
override WORKLOAD_ROLLOUT_LEASE := scripts/rollout-mutation-lease.sh
override FOUNDATION_CONFIRMATION := APPLY KEYLESS FOUNDATION
override ACL_STATE_SCRUB_CONFIRMATION := FORGET LEAKING ACL TOKEN GENERATORS
override WORKLOAD_CONFIRMATION := APPLY ONE WORKCELL CANARY
override WORKLOAD_PREREQUISITE_CONFIRMATION := APPLY WORKLOAD PREREQUISITES
override WORKLOAD_CLUSTER_CONFIRMATION := APPLY NETWORK HARDENING $(WORKLOAD_CLUSTER_STAGE)
override WORKLOAD_CLUSTER_RECOVERY_CONFIRMATION := RELEASE NETWORK HARDENING LEASE $(WORKLOAD_CLUSTER_STAGE)
override WORKLOAD_CLUSTER_REBIND_CONFIRMATION := REBIND NETWORK HARDENING LEASE $(WORKLOAD_CLUSTER_STAGE)
override WORKLOAD_PREREQUISITE_TARGETS := \
	-target='google_secret_manager_secret_version.postgres_connection_string' \
	-target='google_secret_manager_secret_version.postgres_read_replica_connection_string' \
	-target='google_sql_database_instance.invited_beta' \
	-target='google_sql_database.invited_beta' \
	-target='random_password.cloud_sql_invited_beta' \
	-target='google_sql_user.invited_beta' \
	-target='google_secret_manager_secret_version.cloud_sql_invited_beta_password' \
	-target='google_secret_manager_secret_version.sandbox_access_token_hash_seed' \
	-target='time_static.volume_token_generation' \
	-target='tls_private_key.volume_token[0]' \
	-target='module.init.google_service_account.api_controller_service_account' \
	-target='module.init.google_artifact_registry_repository_iam_member.api_controller_orchestration_reader' \
	-target='module.init.google_artifact_registry_repository_iam_member.api_controller_core_reader' \
	-target='module.init.google_storage_bucket_iam_member.api_controller' \
	-target='module.init.google_project_iam_member.api_controller' \
	-target='google_artifact_registry_repository_iam_member.custom_environments_repository_member' \
	-target='google_artifact_registry_repository_iam_member.custom_environments_repository_api_controller_member'
WORKLOAD_CLUSTER_WAIT_SECONDS ?= 1800
NETWORK_HARDENING_ROLLOUT_WAIT_SECONDS ?= $(WORKLOAD_CLUSTER_WAIT_SECONDS)

define foundation_metadata_context
FOUNDATION_ENV="$(ENV)" \
FOUNDATION_ENV_FILE="$(ENV_FILE)" \
FOUNDATION_TF_VAR_FILE="$(TF_VAR_FILE)" \
FOUNDATION_GCP_PROJECT_ID="$(GCP_PROJECT_ID)" \
FOUNDATION_GCP_REGION="$(GCP_REGION)" \
FOUNDATION_STATE_BUCKET="$(TERRAFORM_STATE_BUCKET)" \
FOUNDATION_STATE_PREFIX="$(TERRAFORM_STATE_PREFIX)"
endef

define workload_metadata_context
WORKLOAD_ENV="$(ENV)" \
WORKLOAD_ENV_FILE="$(ENV_FILE)" \
WORKLOAD_TF_VAR_FILE="$(TF_VAR_FILE)" \
WORKLOAD_GCP_PROJECT_ID="$(GCP_PROJECT_ID)" \
WORKLOAD_GCP_REGION="$(GCP_REGION)" \
WORKLOAD_GCP_ZONE="$(GCP_ZONE)" \
WORKLOAD_PREFIX="$(PREFIX)" \
WORKLOAD_CORE_IMAGE_REVISION="$(WORKLOAD_IMAGE_REVISION)" \
WORKLOAD_JOB_BINARY_BUCKET="$(WORKLOAD_JOB_BINARY_BUCKET)" \
WORKLOAD_STATE_BUCKET="$(TERRAFORM_STATE_BUCKET)" \
WORKLOAD_STATE_PREFIX="$(TERRAFORM_STATE_PREFIX)" \
WORKLOAD_TOPOLOGY_POLICY="$(WORKLOAD_TOPOLOGY_POLICY)" \
WORKLOAD_PACKER_TEMPLATE="$(WORKLOAD_PACKER_TEMPLATE)"
endef

define workload_cluster_metadata_context
$(workload_metadata_context) \
WORKLOAD_CLUSTER_STAGE="$(WORKLOAD_CLUSTER_STAGE)" \
WORKLOAD_CLUSTER_CHECKPOINT="$(WORKLOAD_CLUSTER_CHECKPOINT)"
endef

.PHONY: workload-plan-check workload-prerequisite-plan-check workload-cluster-plan-check workload-plan-assertions-test workload-release-test packer-reserve-check worker-startup-render-test
workload-plan-check:
	@ test -f "$(WORKLOAD_PLAN_MANIFEST)" || (printf "Missing workload plan provenance %s.\n" "$(WORKLOAD_PLAN_MANIFEST)"; exit 1)
	@ set -eu; \
		umask 077; \
		artifacts="$$(mktemp ".workload-plan-check.$(ENV).XXXXXX")"; \
		trap 'rm -f -- "$${artifacts}"' EXIT HUP INT TERM; \
		jq -eS '.release_artifacts' "$(WORKLOAD_PLAN_MANIFEST)" >"$${artifacts}"; \
		chmod 0600 "$${artifacts}"; \
		./scripts/assert-workload-plan.sh \
			"$(WORKLOAD_PLAN)" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)" "$(WORKLOAD_PACKER_TEMPLATE)" \
			"$${artifacts}"; \
		./scripts/assert-network-hardening-normal-plan.sh \
			"$(WORKLOAD_PLAN)" "$(TF)" "$(TERRAFORM_ENVIRONMENT)"

workload-prerequisite-plan-check:
	@ test -f "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" || (printf "Missing workload prerequisite plan provenance %s.\n" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)"; exit 1)
	@ set -eu; \
		umask 077; \
		artifacts="$$(mktemp ".workload-prerequisite-plan-check.$(ENV).XXXXXX")"; \
		trap 'rm -f -- "$${artifacts}"' EXIT HUP INT TERM; \
		jq -eS '.release_artifacts' "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" >"$${artifacts}"; \
		chmod 0600 "$${artifacts}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$(WORKLOAD_PREREQUISITE_PLAN)" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" \
			"$(TF)" "." "../.." "$${artifacts}"; \
		$(workload_metadata_context) ./scripts/assert-workload-prerequisite-plan.sh \
			"$(WORKLOAD_PREREQUISITE_PLAN)" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)"

workload-cluster-plan-check:
	@ $(MAKE) workload-cluster-stage-guard
	@ test -f "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" || (printf "Missing cluster plan provenance %s.\n" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"; exit 1)
	@ set -eu; \
		umask 077; \
		artifacts="$$(mktemp ".workload-cluster-plan-check.$(ENV).XXXXXX")"; \
		trap 'rm -f -- "$${artifacts}"' EXIT HUP INT TERM; \
		jq -eS '.release_artifacts' "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" >"$${artifacts}"; \
		chmod 0600 "$${artifacts}"; \
		$(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$(WORKLOAD_CLUSTER_PLAN)" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" \
			"$(TF)" "." "../.." "$${artifacts}"; \
		recovery_stage=""; \
		if [ -n "$(WORKLOAD_CLUSTER_RECOVERY_TOKEN)" ]; then \
			./scripts/assert-network-hardening-recovery-token.sh \
				"$(WORKLOAD_CLUSTER_RECOVERY_TOKEN)" "$(TERRAFORM_STATE_BUCKET)" \
				"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
				"$(WORKLOAD_CLUSTER_STAGE)" "$(ENV)" \
				"$(TERRAFORM_STATE_PREFIX)" ../.. >/dev/null; \
			recovery_stage="$(WORKLOAD_CLUSTER_STAGE)"; \
		fi; \
		./scripts/assert-network-hardening-stage-plan.sh \
			"$(WORKLOAD_CLUSTER_PLAN)" "$(TF)" "$(WORKLOAD_CLUSTER_STAGE)" \
			"$${recovery_stage}"

workload-plan-assertions-test:
	@ ./scripts/test-network-hardening-rollout.sh
	@ ./scripts/test-network-hardening-normal-plan.sh
	@ ./scripts/test-network-hardening-recovery-token.sh
	@ ./scripts/test-rebind-network-hardening-recovery-token.sh
	@ ./scripts/test-network-hardening-stage-wait.sh
	@ ./scripts/test-workload-plan-assertions.sh
	@ ./scripts/test-workload-prerequisite-plan.sh
	@ ./scripts/test-legacy-workload-mutation-disabled.sh
	@ ./scripts/test-rollout-mutation-lease.sh
	@ ./scripts/test-workload-quota-mode.sh
	@ ./scripts/test-workload-release.sh
	@ ./scripts/test-workload-cluster-wait.sh
	@ ../modules/job-template-manager/scripts/test-get-nomad-job-count.sh
	@ ../modules/job-template-manager/scripts/test-module.sh "$(TF)"
	@ $(MAKE) worker-startup-render-test
	@ $(MAKE) network-security-check

workload-release-test:
	@ ./scripts/test-rollout-mutation-lease.sh
	@ ./scripts/test-workload-quota-mode.sh
	@ ./scripts/test-workload-release.sh

worker-startup-render-test:
	@ ./scripts/test-worker-startup-render.sh "$(TF)"

.PHONY: network-security-check
network-security-check:
	@ ./scripts/test-network-security-guards.sh "$(TF)"
	@ go test ../../packages/shared/pkg/sandbox-network

packer-reserve-check:
	@ ./scripts/assert-packer-reserve.sh "$(WORKLOAD_TOPOLOGY_POLICY)" "$(WORKLOAD_PACKER_TEMPLATE)"

.PHONY: foundation-config-guard
foundation-config-guard:
	@ test "$(ENV)" != "not-set" || (printf "No environment selected. Run 'make set-env ENV=dev' from the repository root.\n"; exit 1)
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ test -f "$(ENV_FILE)" || (printf "Missing environment file $(ENV_FILE).\n"; exit 1)
	@ test -n "$(TERRAFORM_ENVIRONMENT)" && test "$(TERRAFORM_ENVIRONMENT)" = "$(ENV)" || (printf "TERRAFORM_ENVIRONMENT must exactly match the selected environment %s; found %s.\n" "$(ENV)" "$(TERRAFORM_ENVIRONMENT)"; exit 1)
	@ test -n "$(GCP_PROJECT_ID)" || (printf "GCP_PROJECT_ID is required.\n"; exit 1)
	@ test -n "$(GCP_REGION)" || (printf "GCP_REGION is required.\n"; exit 1)
	@ test -n "$(TERRAFORM_STATE_BUCKET)" && test "$(TERRAFORM_STATE_BUCKET)" != "-terraform-state" || (printf "A valid TERRAFORM_STATE_BUCKET is required.\n"; exit 1)
	@ test -n "$(TERRAFORM_STATE_PREFIX)" || (printf "A valid TERRAFORM_STATE_PREFIX is required.\n"; exit 1)
	@ ./scripts/assert-foundation-var-sources.sh "." "$(TF_VAR_FILE)"

.PHONY: foundation-toolchain-guard
foundation-toolchain-guard:
	@ test -n "$(EXPECTED_TERRAFORM_VERSION)" || (printf "No Terraform version is pinned in ../../.tool-versions.\n"; exit 1)
	@ test -n "$(TF)" && test -x "$(TF)" || (printf "Terraform is not installed or not executable.\n"; exit 1)
	@ actual_version="$$("$(TF)" version -json | jq -r '.terraform_version')"; \
	if [ "$$actual_version" != "$(EXPECTED_TERRAFORM_VERSION)" ]; then \
		printf "Terraform %s is required; found %s at %s.\n" "$(EXPECTED_TERRAFORM_VERSION)" "$$actual_version" "$(TF)"; \
		exit 1; \
	fi

.PHONY: keyless-runtime-check
keyless-runtime-check:
	@ ./scripts/test-keyless-runtime-guard.sh
	@ ./scripts/test-api-controller-identity.sh
	@ ./scripts/test-configure-docker-gcp.sh
	@ ./scripts/test-control-plane-secret-logging.sh
	@ ./scripts/test-nomad-restart-safety.sh
	@ ./scripts/test-server-voter-health.sh
	@ ./scripts/test-scrub-legacy-acl-token-state.sh

.PHONY: acl-token-state-guard
acl-token-state-guard:
	@ ./scripts/assert-no-legacy-acl-token-state.sh "$(TF)"

.PHONY: workload-config-guard workload-context-guard
workload-config-guard: foundation-config-guard
	@ test "$(TERRAFORM_STATE_BUCKET)" = "$(GCP_PROJECT_ID)-terraform-state" || (printf "Workload rollout lease requires the canonical project state bucket %s-terraform-state; found %s.\n" "$(GCP_PROJECT_ID)" "$(TERRAFORM_STATE_BUCKET)"; exit 1)
	@ test -f "$(TF_VAR_FILE)" && test ! -L "$(TF_VAR_FILE)" || (printf "Explicit workload var file must be a regular, non-symlink file: %s\n" "$(TF_VAR_FILE)"; exit 1)
	@ test ! -L "$(ENV_FILE)" || (printf "Workload environment file must not be a symlink: %s\n" "$(ENV_FILE)"; exit 1)
	@ test -n "$(GCP_ZONE)" || (printf "GCP_ZONE is required for the workload.\n"; exit 1)
	@ printf '%s' "$(GCP_ZONE)" | grep -Eq '^$(GCP_REGION)-[a-z]$$' || (printf "GCP_ZONE must belong to GCP_REGION %s; found %s.\n" "$(GCP_REGION)" "$(GCP_ZONE)"; exit 1)
	@ test -n "$(PREFIX)" || (printf "PREFIX is required for workload artifact discovery.\n"; exit 1)
	@ printf '%s' "$(WORKLOAD_IMAGE_REVISION)" | grep -Eq '^[0-9a-f]{12,40}$$' || (printf "CORE_IMAGE_REVISION must be an explicit 12-40 character lowercase Git SHA for the reviewed core image release.\n"; exit 1)
	@ printf '%s' "$(WORKLOAD_JOB_BINARY_BUCKET)" | grep -Eq '^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$$' || (printf "Derived workload job binary bucket is invalid: %s\n" "$(WORKLOAD_JOB_BINARY_BUCKET)"; exit 1)
	@ test -x "$(GCLOUD)" || (printf "gcloud is not installed or executable: %s\n" "$(GCLOUD)"; exit 1)
	@ test -f "$(WORKLOAD_TOPOLOGY_POLICY)" && test ! -L "$(WORKLOAD_TOPOLOGY_POLICY)" || (printf "Workload topology policy must be a regular, non-symlink file.\n"; exit 1)
	@ test -f "$(WORKLOAD_PACKER_TEMPLATE)" && test ! -L "$(WORKLOAD_PACKER_TEMPLATE)" || (printf "Workload Packer template must be a regular, non-symlink file.\n"; exit 1)
	@ test -x "$(WORKLOAD_ROLLOUT_LEASE)" || (printf "Shared rollout mutation lease helper is missing or not executable.\n"; exit 1)
	@ test -x ./scripts/assert-network-hardening-normal-plan.sh || (printf "Ordinary workload network-hardening stage guard is missing or not executable.\n"; exit 1)
	@ test -x ./scripts/assert-network-hardening-recovery-token.sh || (printf "Network-hardening recovery-token guard is missing or not executable.\n"; exit 1)
	@ test -z "$${TF_CLI_ARGS:-}" && test -z "$${TF_CLI_ARGS_plan:-}" && test -z "$${TF_CLI_ARGS_apply:-}" || (printf "Ambient TF_CLI_ARGS values are forbidden in the workload release workflow.\n"; exit 1)
	@ if env | grep -q '^TF_VAR_'; then printf "Ambient TF_VAR_* values are forbidden; use the selected environment and explicit var file.\n"; exit 1; fi

workload-context-guard:
	@ $(MAKE) workload-config-guard foundation-toolchain-guard keyless-runtime-check
	@ ./scripts/assert-foundation-backend.sh "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-state-bucket.sh \
		"$(TERRAFORM_STATE_BUCKET)" \
		"$(GCP_PROJECT_ID)" \
		"$(GCP_REGION)" \
		"$(GCLOUD)"
	@ ./scripts/assert-foundation-workspace.sh "$(TF)"
	@ ./scripts/assert-no-legacy-acl-token-state.sh "$(TF)"

.PHONY: workload-cluster-stage-guard
workload-cluster-stage-guard:
	@ printf '%s' "$(WORKLOAD_CLUSTER_STAGE)" | grep -Eq '^(network|server|api|worker|build)$$' || (printf "WORKLOAD_CLUSTER_STAGE must be one of network, server, api, worker, or build.\n"; exit 1)
	@ test "$(OS_LOGIN_OPERATOR_ACCESS_CONFIRMED)" = "true" || (printf "OS_LOGIN_OPERATOR_ACCESS_CONFIRMED=true is required after proving IAP and OS Login access.\n"; exit 1)
	@ ./scripts/assert-network-hardening-checkpoint.sh \
		"$(WORKLOAD_CLUSTER_STAGE)" "$(WORKLOAD_CLUSTER_CHECKPOINT)" \
		"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCP_ZONE)" "$(PREFIX)" "../.."

.PHONY: workload-cluster-plan workload-cluster-apply workload-cluster-rebind-recovery-source workload-cluster-recover-lease workload-cluster-wait workload-prerequisite-plan workload-prerequisite-apply workload-plan workload-apply
workload-cluster-plan:
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ $(MAKE) workload-context-guard workload-cluster-stage-guard
	@ $(call tf_fmt)
	@ set -eu; \
		temp_dir="$$(mktemp -d ".workload-cluster-plan.$(ENV).XXXXXX")"; \
		temp_plan="$${temp_dir}/plan"; \
		topology_plan="$${temp_dir}/topology.plan"; \
		temp_manifest="$${temp_dir}/manifest"; \
		before_artifacts="$${temp_dir}/artifacts-before.json"; \
		after_artifacts="$${temp_dir}/artifacts-after.json"; \
		lease_token="$${temp_dir}/lease-token.json"; \
		lease_acquired=false; \
		lease_borrowed=false; \
		recovery_token="$(WORKLOAD_CLUSTER_RECOVERY_TOKEN)"; \
		recovery_stage=""; \
		cleanup() { \
			status=$$?; \
			trap - EXIT HUP INT TERM; \
			if [ "$${lease_acquired}" = true ]; then \
				if [ "$${lease_borrowed}" = true ]; then \
					printf "Preserving the borrowed recovery lease after cluster-plan exit.\n" >&2; \
				elif ! "$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; then \
					printf "Shared rollout lease release failed; inspect it before another rollout.\n" >&2; \
					if [ "$${status}" -eq 0 ]; then status=1; fi; \
				fi; \
			fi; \
			rm -rf -- "$${temp_dir}"; \
			exit "$${status}"; \
		}; \
		trap cleanup EXIT HUP INT TERM; \
		umask 077; \
		holder_digest="$$( \
			{ \
				git -C ../.. rev-parse --verify HEAD; \
				printf '%s\n' "$(ENV)" "$(WORKLOAD_CLUSTER_STAGE)" \
					"$$(shasum -a 256 "$(WORKLOAD_CLUSTER_CHECKPOINT)" | awk '{print $$1}')" \
					"$$$$" "$$(date -u +%s)"; \
			} | shasum -a 256 | awk '{print $$1}' \
		)"; \
		if [ -n "$${recovery_token}" ]; then \
			./scripts/assert-network-hardening-recovery-token.sh \
				"$${recovery_token}" "$(TERRAFORM_STATE_BUCKET)" \
				"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
				"$(WORKLOAD_CLUSTER_STAGE)" "$(ENV)" \
				"$(TERRAFORM_STATE_PREFIX)" ../.. >/dev/null; \
			lease_token="$${recovery_token}"; \
			lease_borrowed=true; \
			recovery_stage="$(WORKLOAD_CLUSTER_STAGE)"; \
		else \
			"$(WORKLOAD_ROLLOUT_LEASE)" acquire \
				"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
				"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
				"cluster-plan:$${holder_digest}" "$${lease_token}"; \
		fi; \
		lease_acquired=true; \
		rm -f -- "$(WORKLOAD_CLUSTER_PLAN)" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${before_artifacts}"; \
		chmod 0600 "$${before_artifacts}"; \
		before_fingerprint="$$($(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh fingerprint \
			"$(TF)" "." "../.." "$${before_artifacts}")"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" assert-held \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${lease_token}"; \
		$(tf_vars) TF_VAR_network_hardening_rollout_stage="$(WORKLOAD_CLUSTER_STAGE)" \
			$(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_CLUSTER_TOPOLOGY_TARGET) \
			-out="$${topology_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${topology_plan}"; \
		$(tf_vars) TF_VAR_network_hardening_rollout_stage="$(WORKLOAD_CLUSTER_STAGE)" \
			$(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_CLUSTER_TARGETS) \
			$(WORKLOAD_CLUSTER_COMPLETION_REPLACE) \
			-out="$${temp_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${temp_plan}"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${after_artifacts}"; \
		chmod 0600 "$${after_artifacts}"; \
		after_fingerprint="$$($(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh fingerprint \
			"$(TF)" "." "../.." "$${after_artifacts}")"; \
		if [ "$${before_fingerprint}" != "$${after_fingerprint}" ]; then \
			printf "Refusing cluster bootstrap plan: configuration or release artifacts changed while Terraform was planning.\n" >&2; \
			exit 1; \
		fi; \
		./scripts/assert-workload-plan.sh \
			"$${topology_plan}" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)" "$(WORKLOAD_PACKER_TEMPLATE)" \
			"$${after_artifacts}" cluster; \
		./scripts/assert-network-hardening-stage-plan.sh \
			"$${temp_plan}" "$(TF)" "$(WORKLOAD_CLUSTER_STAGE)" \
			"$${recovery_stage}"; \
		quota_mode="$$(./scripts/select-workload-quota-mode.sh \
			"$${topology_plan}" "$(TF)" "$(WORKLOAD_TOPOLOGY_POLICY)")"; \
		./scripts/assert-workload-quota.sh \
			"$(WORKLOAD_TOPOLOGY_POLICY)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)" \
			"$${quota_mode}"; \
		$(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh write \
			"$${temp_plan}" "$${temp_manifest}" "$(TF)" "." "../.." \
			"$${after_artifacts}" "$${after_fingerprint}"; \
		mv -f -- "$${temp_manifest}" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"; \
		mv -f -- "$${temp_plan}" "$(WORKLOAD_CLUSTER_PLAN)"; \
		rm -f -- "$${before_artifacts}" "$${after_artifacts}" "$${topology_plan}"; \
		if [ "$${lease_borrowed}" = false ]; then \
			"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; \
		else \
			printf "Saved recovery plan while preserving the existing shared rollout lease.\n"; \
		fi; \
		lease_acquired=false; \
		rmdir "$${temp_dir}"; \
		trap - EXIT HUP INT TERM
	@ printf "Saved %s-stage cluster plan and checkpoint-bound provenance are ready for review: %s and %s\n" "$(WORKLOAD_CLUSTER_STAGE)" "$(WORKLOAD_CLUSTER_PLAN)" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"

workload-cluster-apply:
	@ if [ "$(CONFIRM)" != "$(WORKLOAD_CLUSTER_CONFIRMATION)" ]; then \
		printf "Refusing cluster bootstrap apply. Re-run with CONFIRM='$(WORKLOAD_CLUSTER_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ $(MAKE) workload-context-guard workload-cluster-stage-guard
	@ set -eu; \
		temp_dir="$$(mktemp -d ".workload-cluster-apply.$(ENV).XXXXXX")"; \
		topology_plan="$${temp_dir}/topology.plan"; \
		before_artifacts="$${temp_dir}/artifacts-before.json"; \
		after_artifacts="$${temp_dir}/artifacts-after.json"; \
		apply_plan="$${temp_dir}/reviewed.plan"; \
		apply_manifest="$${temp_dir}/reviewed.plan.manifest"; \
		consumed_plan="$${temp_dir}/consumed.plan"; \
		consumed_manifest="$${temp_dir}/consumed.plan.manifest"; \
		lease_token="$${temp_dir}/lease-token.json"; \
		lease_acquired=false; \
		lease_borrowed=false; \
		recovery_token="$(WORKLOAD_CLUSTER_RECOVERY_TOKEN)"; \
		recovery_stage=""; \
		mutation_started=false; \
		convergence_proven=false; \
		plan_moved=false; \
		manifest_moved=false; \
		cleanup() { \
			status=$$?; \
			restore_failed=false; \
			lease_preserved=false; \
			trap - EXIT HUP INT TERM; \
			if [ "$${plan_moved}" = true ]; then \
				if [ ! -e "$(WORKLOAD_CLUSTER_PLAN)" ] \
					&& mv "$${consumed_plan}" "$(WORKLOAD_CLUSTER_PLAN)"; then \
					plan_moved=false; \
				else \
					printf "Could not restore consumed cluster plan; preserving private recovery directory %s.\n" "$${temp_dir}" >&2; \
					restore_failed=true; \
					status=1; \
				fi; \
			fi; \
			if [ "$${manifest_moved}" = true ]; then \
				if [ ! -e "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" ] \
					&& mv "$${consumed_manifest}" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"; then \
					manifest_moved=false; \
				else \
					printf "Could not restore consumed cluster manifest; preserving private recovery directory %s.\n" "$${temp_dir}" >&2; \
					restore_failed=true; \
					status=1; \
				fi; \
			fi; \
			rm -f -- "$${topology_plan}"; \
			if [ "$${lease_acquired}" = true ]; then \
				if [ "$${lease_borrowed}" = true ] && [ "$${mutation_started}" = false ]; then \
					printf "Recovery apply did not start a mutation; preserving its borrowed shared lease and token %s.\n" "$${lease_token}" >&2; \
				elif [ "$${mutation_started}" = true ] && [ "$${convergence_proven}" = false ]; then \
					printf "Cluster mutation started but convergence was not proven; preserving the shared lease and private recovery directory %s.\n" "$${temp_dir}" >&2; \
					printf "After proving the original apply is no longer running, use a fresh stage checkpoint and recover with WORKLOAD_CLUSTER_RECOVERY_TOKEN=%s CONFIRM='%s'.\n" "$${lease_token}" "$(WORKLOAD_CLUSTER_RECOVERY_CONFIRMATION)" >&2; \
					lease_preserved=true; \
					status=1; \
				elif ! "$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; then \
					printf "Shared rollout lease release failed; inspect it before another rollout.\n" >&2; \
					lease_preserved=true; \
					if [ "$${status}" -eq 0 ]; then status=1; fi; \
				fi; \
			fi; \
			if [ "$${restore_failed}" = false ] && [ "$${lease_preserved}" = false ]; then \
				rm -rf -- "$${temp_dir}"; \
			fi; \
			exit "$${status}"; \
		}; \
		trap cleanup EXIT HUP INT TERM; \
		umask 077; \
		source_head="$$(git -C ../.. rev-parse --verify HEAD)"; \
		holder_digest="$$( \
			{ \
				printf '%s\n' "$${source_head}"; \
				printf '%s\n' "$(ENV)" "$(WORKLOAD_CLUSTER_STAGE)" \
					"$$(shasum -a 256 "$(WORKLOAD_CLUSTER_CHECKPOINT)" | awk '{print $$1}')" \
					"$$$$" "$$(date -u +%s)"; \
			} | shasum -a 256 | awk '{print $$1}' \
		)"; \
		if [ -n "$${recovery_token}" ]; then \
			./scripts/assert-network-hardening-recovery-token.sh \
				"$${recovery_token}" "$(TERRAFORM_STATE_BUCKET)" \
				"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
				"$(WORKLOAD_CLUSTER_STAGE)" "$(ENV)" \
				"$(TERRAFORM_STATE_PREFIX)" ../.. >/dev/null; \
			lease_token="$${recovery_token}"; \
			lease_borrowed=true; \
			recovery_stage="$(WORKLOAD_CLUSTER_STAGE)"; \
		else \
			"$(WORKLOAD_ROLLOUT_LEASE)" acquire \
				"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
				"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
				"cluster-apply:$(WORKLOAD_CLUSTER_STAGE):$(ENV):$(TERRAFORM_STATE_PREFIX):$${source_head}:$${holder_digest}" "$${lease_token}"; \
		fi; \
		lease_acquired=true; \
		test -f "$(WORKLOAD_CLUSTER_PLAN)" || { \
			printf "Missing reviewed cluster plan %s. Run 'make workload-cluster-plan' first.\n" "$(WORKLOAD_CLUSTER_PLAN)" >&2; \
			exit 1; \
		}; \
		test -f "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" || { \
			printf "Missing cluster plan provenance %s. Run 'make workload-cluster-plan' first.\n" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" >&2; \
			exit 1; \
		}; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${before_artifacts}"; \
		chmod 0600 "$${before_artifacts}"; \
		$(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$(WORKLOAD_CLUSTER_PLAN)" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" "$(TF)" "." "../.." \
			"$${before_artifacts}"; \
		cp "$(WORKLOAD_CLUSTER_PLAN)" "$${apply_plan}"; \
		cp "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" "$${apply_manifest}"; \
		chmod 0600 "$${apply_plan}" "$${apply_manifest}"; \
		$(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$${apply_plan}" "$${apply_manifest}" "$(TF)" "." "../.." \
			"$${before_artifacts}"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" assert-held \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${lease_token}"; \
		$(tf_vars) TF_VAR_network_hardening_rollout_stage="$(WORKLOAD_CLUSTER_STAGE)" \
			$(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_CLUSTER_TOPOLOGY_TARGET) \
			-out="$${topology_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${topology_plan}"; \
		./scripts/assert-workload-plan.sh \
			"$${topology_plan}" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)" "$(WORKLOAD_PACKER_TEMPLATE)" \
			"$${before_artifacts}" cluster; \
		./scripts/assert-network-hardening-stage-plan.sh \
			"$${apply_plan}" "$(TF)" "$(WORKLOAD_CLUSTER_STAGE)" \
			"$${recovery_stage}"; \
		quota_mode="$$(./scripts/select-workload-quota-mode.sh \
			"$${topology_plan}" "$(TF)" "$(WORKLOAD_TOPOLOGY_POLICY)")"; \
		./scripts/assert-workload-quota.sh \
			"$(WORKLOAD_TOPOLOGY_POLICY)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)" \
			"$${quota_mode}"; \
		rm -f -- "$${topology_plan}"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${after_artifacts}"; \
		chmod 0600 "$${after_artifacts}"; \
		$(workload_cluster_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$${apply_plan}" "$${apply_manifest}" "$(TF)" "." "../.." \
			"$${after_artifacts}"; \
		./scripts/assert-network-hardening-checkpoint.sh \
			"$(WORKLOAD_CLUSTER_STAGE)" "$(WORKLOAD_CLUSTER_CHECKPOINT)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCP_ZONE)" "$(PREFIX)" "../.."; \
		"$(WORKLOAD_ROLLOUT_LEASE)" assert-held \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${lease_token}"; \
		mutation_started=true; \
		$(TF) apply -input=false "$${apply_plan}"; \
		GCP_PROJECT_ID="$(GCP_PROJECT_ID)" \
			GCP_REGION="$(GCP_REGION)" \
			GCP_ZONE="$(GCP_ZONE)" \
			DOMAIN_NAME="$(DOMAIN_NAME)" \
			PREFIX="$(PREFIX)" \
			NETWORK_HARDENING_ROLLOUT_STAGE="$(WORKLOAD_CLUSTER_STAGE)" \
			NETWORK_HARDENING_WAIT_SECONDS="$(NETWORK_HARDENING_ROLLOUT_WAIT_SECONDS)" \
			GCLOUD_BIN="$(GCLOUD)" \
			./scripts/wait-network-hardening-stage.sh; \
		set +e; \
		$(tf_vars) TF_VAR_network_hardening_rollout_stage="$(WORKLOAD_CLUSTER_STAGE)" \
			$(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_CLUSTER_TARGETS) \
			-out="$${temp_dir}/post-apply.plan" \
			-input=false \
			-compact-warnings \
			-detailed-exitcode \
			>/dev/null; \
		post_apply_status=$$?; \
		set -e; \
		if [ "$${post_apply_status}" -ne 0 ]; then \
			printf "Post-apply cluster convergence check was not clean (Terraform exit %s).\n" \
					"$${post_apply_status}" >&2; \
			exit 1; \
		fi; \
		convergence_proven=true; \
		if ! cmp -s "$(WORKLOAD_CLUSTER_PLAN)" "$${apply_plan}" \
			|| ! cmp -s "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" "$${apply_manifest}"; then \
			printf "Applied reviewed bytes, but the published cluster plan changed concurrently; preserving the replacement.\n" >&2; \
			exit 1; \
		fi; \
		mv "$(WORKLOAD_CLUSTER_PLAN)" "$${consumed_plan}"; \
		plan_moved=true; \
		mv "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)" "$${consumed_manifest}"; \
		manifest_moved=true; \
		"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; \
		lease_acquired=false; \
		plan_moved=false; \
		manifest_moved=false; \
		rm -rf -- "$${temp_dir}"; \
		trap - EXIT HUP INT TERM

workload-cluster-rebind-recovery-source:
	@ if [ "$(CONFIRM)" != "$(WORKLOAD_CLUSTER_REBIND_CONFIRMATION)" ]; then \
		printf "Refusing cluster recovery source rebind. Re-run with CONFIRM='$(WORKLOAD_CLUSTER_REBIND_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ $(MAKE) workload-context-guard workload-cluster-stage-guard
	@ set -eu; \
		old_token="$(WORKLOAD_CLUSTER_RECOVERY_TOKEN)"; \
		new_token="$(WORKLOAD_CLUSTER_REBOUND_TOKEN)"; \
		test -n "$${old_token}" || { \
			printf "WORKLOAD_CLUSTER_RECOVERY_TOKEN must name the preserved generation-bound token.\n" >&2; \
			exit 1; \
		}; \
		test -n "$${new_token}" || { \
			printf "WORKLOAD_CLUSTER_REBOUND_TOKEN must name a distinct, nonexistent private token path.\n" >&2; \
			exit 1; \
		}; \
		./scripts/rebind-network-hardening-recovery-token.sh \
			"$${old_token}" "$${new_token}" \
			"$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
			"$(WORKLOAD_CLUSTER_STAGE)" "$(ENV)" \
			"$(TERRAFORM_STATE_PREFIX)" ../.. "$(GCLOUD)"

workload-cluster-recover-lease:
	@ if [ "$(CONFIRM)" != "$(WORKLOAD_CLUSTER_RECOVERY_CONFIRMATION)" ]; then \
		printf "Refusing cluster lease recovery. Re-run with CONFIRM='$(WORKLOAD_CLUSTER_RECOVERY_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ $(MAKE) workload-context-guard workload-cluster-stage-guard
	@ set -eu; \
		token="$(WORKLOAD_CLUSTER_RECOVERY_TOKEN)"; \
		test -n "$${token}" || { \
			printf "WORKLOAD_CLUSTER_RECOVERY_TOKEN must name the preserved generation-bound token.\n" >&2; \
			exit 1; \
		}; \
		./scripts/assert-network-hardening-recovery-token.sh \
			"$${token}" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
			"$(WORKLOAD_CLUSTER_STAGE)" "$(ENV)" \
			"$(TERRAFORM_STATE_PREFIX)" ../.. >/dev/null; \
		"$(WORKLOAD_ROLLOUT_LEASE)" assert-held \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${token}"; \
		temp_dir="$$(mktemp -d ".workload-cluster-recover.$(ENV).XXXXXX")"; \
		cleanup() { status=$$?; trap - EXIT HUP INT TERM; rm -rf -- "$${temp_dir}"; exit "$${status}"; }; \
		trap cleanup EXIT HUP INT TERM; \
		GCP_PROJECT_ID="$(GCP_PROJECT_ID)" \
			GCP_REGION="$(GCP_REGION)" \
			GCP_ZONE="$(GCP_ZONE)" \
			DOMAIN_NAME="$(DOMAIN_NAME)" \
			PREFIX="$(PREFIX)" \
			NETWORK_HARDENING_ROLLOUT_STAGE="$(WORKLOAD_CLUSTER_STAGE)" \
			NETWORK_HARDENING_WAIT_SECONDS="$(NETWORK_HARDENING_ROLLOUT_WAIT_SECONDS)" \
			GCLOUD_BIN="$(GCLOUD)" \
			./scripts/wait-network-hardening-stage.sh; \
		set +e; \
		$(tf_vars) TF_VAR_network_hardening_rollout_stage="$(WORKLOAD_CLUSTER_STAGE)" \
			$(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_CLUSTER_TARGETS) \
			-out="$${temp_dir}/post-recovery.plan" \
			-input=false \
			-compact-warnings \
			-detailed-exitcode \
			>/dev/null; \
		post_recovery_status=$$?; \
		set -e; \
		if [ "$${post_recovery_status}" -ne 0 ]; then \
			printf "Recovery convergence is live-stable but Terraform is not clean (exit %s); preserving the lease. Create a fresh same-stage plan and apply it with WORKLOAD_CLUSTER_RECOVERY_TOKEN=%s.\n" "$${post_recovery_status}" "$${token}" >&2; \
			exit 1; \
		fi; \
		"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${token}"; \
		rm -rf -- "$${temp_dir}"; \
		trap - EXIT HUP INT TERM; \
		printf "Recovered the converged, Terraform-clean %s stage and released its exact shared lease.\n" "$(WORKLOAD_CLUSTER_STAGE)"

workload-cluster-wait:
	@ $(MAKE) workload-context-guard
	@ ./scripts/wait-workload-cluster.sh \
		"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCP_ZONE)" \
		"$(PREFIX)" "$(DOMAIN_NAME)" "$(GCLOUD)" \
		"$(WORKLOAD_CLUSTER_WAIT_SECONDS)"

workload-prerequisite-plan:
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ $(MAKE) workload-context-guard
	@ $(call tf_fmt)
	@ set -eu; \
		temp_dir="$$(mktemp -d ".workload-prerequisite-plan.$(ENV).XXXXXX")"; \
		temp_plan="$${temp_dir}/plan"; \
		temp_manifest="$${temp_dir}/manifest"; \
		before_artifacts="$${temp_dir}/artifacts-before.json"; \
		after_artifacts="$${temp_dir}/artifacts-after.json"; \
		lease_token="$${temp_dir}/lease-token.json"; \
		lease_acquired=false; \
		cleanup() { \
			status=$$?; \
			trap - EXIT HUP INT TERM; \
			if [ "$${lease_acquired}" = true ]; then \
				if ! "$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; then \
					printf "Shared rollout lease release failed; inspect it before another rollout.\n" >&2; \
					if [ "$${status}" -eq 0 ]; then status=1; fi; \
				fi; \
			fi; \
			rm -rf -- "$${temp_dir}"; \
			exit "$${status}"; \
		}; \
		trap cleanup EXIT HUP INT TERM; \
		umask 077; \
		holder_digest="$$( \
			{ \
				git -C ../.. rev-parse --verify HEAD; \
				printf '%s\n' "$(ENV)" "$$$$" "$$(date -u +%s)"; \
			} | shasum -a 256 | awk '{print $$1}' \
		)"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" acquire \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
			"prerequisite-plan:$${holder_digest}" "$${lease_token}"; \
		lease_acquired=true; \
		rm -f -- "$(WORKLOAD_PREREQUISITE_PLAN)" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${before_artifacts}"; \
		chmod 0600 "$${before_artifacts}"; \
		before_fingerprint="$$($(workload_metadata_context) ./scripts/workload-plan-metadata.sh fingerprint \
			"$(TF)" "." "../.." "$${before_artifacts}")"; \
		$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_PREREQUISITE_TARGETS) \
			-out="$${temp_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${temp_plan}"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${after_artifacts}"; \
		chmod 0600 "$${after_artifacts}"; \
		after_fingerprint="$$($(workload_metadata_context) ./scripts/workload-plan-metadata.sh fingerprint \
			"$(TF)" "." "../.." "$${after_artifacts}")"; \
		if [ "$${before_fingerprint}" != "$${after_fingerprint}" ]; then \
			printf "Refusing workload prerequisite plan: configuration or release artifacts changed while Terraform was planning.\n" >&2; \
			exit 1; \
		fi; \
		$(workload_metadata_context) ./scripts/assert-workload-prerequisite-plan.sh \
			"$${temp_plan}" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)"; \
		./scripts/assert-workload-quota.sh \
			"$(WORKLOAD_TOPOLOGY_POLICY)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)" \
			"post-cluster"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh write \
			"$${temp_plan}" "$${temp_manifest}" "$(TF)" "." "../.." \
			"$${after_artifacts}" "$${after_fingerprint}"; \
		mv -f -- "$${temp_manifest}" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)"; \
		mv -f -- "$${temp_plan}" "$(WORKLOAD_PREREQUISITE_PLAN)"; \
		rm -f -- "$${before_artifacts}" "$${after_artifacts}"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; \
		lease_acquired=false; \
		rmdir "$${temp_dir}"; \
		trap - EXIT HUP INT TERM
	@ printf "Saved workload prerequisite plan and provenance are ready for review: %s and %s\n" "$(WORKLOAD_PREREQUISITE_PLAN)" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)"

workload-prerequisite-apply:
	@ if [ "$(CONFIRM)" != "$(WORKLOAD_PREREQUISITE_CONFIRMATION)" ]; then \
		printf "Refusing workload prerequisite apply. Re-run with CONFIRM='$(WORKLOAD_PREREQUISITE_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ $(MAKE) workload-context-guard
	@ set -eu; \
		temp_dir="$$(mktemp -d ".workload-prerequisite-apply.$(ENV).XXXXXX")"; \
		before_artifacts="$${temp_dir}/artifacts-before.json"; \
		after_artifacts="$${temp_dir}/artifacts-after.json"; \
		apply_plan="$${temp_dir}/reviewed.plan"; \
		apply_manifest="$${temp_dir}/reviewed.plan.manifest"; \
		consumed_plan="$${temp_dir}/consumed.plan"; \
		consumed_manifest="$${temp_dir}/consumed.plan.manifest"; \
		lease_token="$${temp_dir}/lease-token.json"; \
		lease_acquired=false; \
		plan_moved=false; \
		manifest_moved=false; \
		cleanup() { \
			status=$$?; \
			restore_failed=false; \
			trap - EXIT HUP INT TERM; \
			if [ "$${plan_moved}" = true ]; then \
				if [ ! -e "$(WORKLOAD_PREREQUISITE_PLAN)" ] \
					&& mv "$${consumed_plan}" "$(WORKLOAD_PREREQUISITE_PLAN)"; then \
					plan_moved=false; \
				else \
					printf "Could not restore consumed prerequisite plan; preserving private recovery directory %s.\n" "$${temp_dir}" >&2; \
					restore_failed=true; \
					status=1; \
				fi; \
			fi; \
			if [ "$${manifest_moved}" = true ]; then \
				if [ ! -e "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" ] \
					&& mv "$${consumed_manifest}" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)"; then \
					manifest_moved=false; \
				else \
					printf "Could not restore consumed prerequisite manifest; preserving private recovery directory %s.\n" "$${temp_dir}" >&2; \
					restore_failed=true; \
					status=1; \
				fi; \
			fi; \
			if [ "$${lease_acquired}" = true ]; then \
				if ! "$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; then \
					printf "Shared rollout lease release failed; inspect it before another rollout.\n" >&2; \
					if [ "$${status}" -eq 0 ]; then status=1; fi; \
				fi; \
			fi; \
			if [ "$${restore_failed}" = false ]; then \
				rm -rf -- "$${temp_dir}"; \
			fi; \
			exit "$${status}"; \
		}; \
		trap cleanup EXIT HUP INT TERM; \
		umask 077; \
		holder_digest="$$( \
			{ \
				git -C ../.. rev-parse --verify HEAD; \
				printf '%s\n' "$(ENV)" "$$$$" "$$(date -u +%s)"; \
			} | shasum -a 256 | awk '{print $$1}' \
		)"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" acquire \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
			"prerequisite-apply:$${holder_digest}" "$${lease_token}"; \
		lease_acquired=true; \
		test -f "$(WORKLOAD_PREREQUISITE_PLAN)" || { \
			printf "Missing reviewed workload prerequisite plan %s. Run 'make workload-prerequisite-plan' first.\n" "$(WORKLOAD_PREREQUISITE_PLAN)" >&2; \
			exit 1; \
		}; \
		test -f "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" || { \
			printf "Missing workload prerequisite plan provenance %s. Run 'make workload-prerequisite-plan' first.\n" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" >&2; \
			exit 1; \
		}; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${before_artifacts}"; \
		chmod 0600 "$${before_artifacts}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$(WORKLOAD_PREREQUISITE_PLAN)" "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" \
			"$(TF)" "." "../.." "$${before_artifacts}"; \
		cp "$(WORKLOAD_PREREQUISITE_PLAN)" "$${apply_plan}"; \
		cp "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" "$${apply_manifest}"; \
		chmod 0600 "$${apply_plan}" "$${apply_manifest}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$${apply_plan}" "$${apply_manifest}" "$(TF)" "." "../.." \
			"$${before_artifacts}"; \
		$(workload_metadata_context) ./scripts/assert-workload-prerequisite-plan.sh \
			"$${apply_plan}" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)"; \
		./scripts/assert-workload-quota.sh \
			"$(WORKLOAD_TOPOLOGY_POLICY)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)" \
			"post-cluster"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${after_artifacts}"; \
		chmod 0600 "$${after_artifacts}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$${apply_plan}" "$${apply_manifest}" "$(TF)" "." "../.." \
			"$${after_artifacts}"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" assert-held \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${lease_token}"; \
		$(TF) apply -input=false "$${apply_plan}"; \
		set +e; \
		$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) \
			$(WORKLOAD_PREREQUISITE_TARGETS) \
			-out="$${temp_dir}/post-apply.plan" \
			-input=false \
			-compact-warnings \
			-detailed-exitcode \
			>/dev/null; \
		post_apply_status=$$?; \
		set -e; \
		if [ "$${post_apply_status}" -ne 0 ]; then \
			printf "Post-apply workload prerequisite convergence check was not clean (Terraform exit %s).\n" \
					"$${post_apply_status}" >&2; \
			exit 1; \
		fi; \
		if ! cmp -s "$(WORKLOAD_PREREQUISITE_PLAN)" "$${apply_plan}" \
			|| ! cmp -s "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" "$${apply_manifest}"; then \
			printf "Applied reviewed bytes, but the published prerequisite plan changed concurrently; preserving the replacement.\n" >&2; \
			exit 1; \
		fi; \
		mv "$(WORKLOAD_PREREQUISITE_PLAN)" "$${consumed_plan}"; \
		plan_moved=true; \
		mv "$(WORKLOAD_PREREQUISITE_PLAN_MANIFEST)" "$${consumed_manifest}"; \
		manifest_moved=true; \
		"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; \
		lease_acquired=false; \
		plan_moved=false; \
		manifest_moved=false; \
		rm -rf -- "$${temp_dir}"; \
		trap - EXIT HUP INT TERM

workload-plan:
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ $(MAKE) workload-context-guard
	@ $(call tf_fmt)
	@ set -eu; \
		temp_dir="$$(mktemp -d ".workload-plan.$(ENV).XXXXXX")"; \
		temp_plan="$${temp_dir}/plan"; \
		temp_manifest="$${temp_dir}/manifest"; \
		before_artifacts="$${temp_dir}/artifacts-before.json"; \
		after_artifacts="$${temp_dir}/artifacts-after.json"; \
		lease_token="$${temp_dir}/lease-token.json"; \
		lease_acquired=false; \
		cleanup() { \
			status=$$?; \
			trap - EXIT HUP INT TERM; \
			if [ "$${lease_acquired}" = true ]; then \
				if ! "$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; then \
					printf "Shared rollout lease release failed; inspect it before another rollout.\n" >&2; \
					if [ "$${status}" -eq 0 ]; then status=1; fi; \
				fi; \
			fi; \
			rm -rf -- "$${temp_dir}"; \
			exit "$${status}"; \
		}; \
		trap cleanup EXIT HUP INT TERM; \
		umask 077; \
		holder_digest="$$( \
			{ \
				git -C ../.. rev-parse --verify HEAD; \
				printf '%s\n' "$(ENV)" "$$$$" "$$(date -u +%s)"; \
			} | shasum -a 256 | awk '{print $$1}' \
		)"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" acquire \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
			"plan:$${holder_digest}" "$${lease_token}"; \
		lease_acquired=true; \
		rm -f -- "$(WORKLOAD_PLAN)" "$(WORKLOAD_PLAN_MANIFEST)"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${before_artifacts}"; \
		chmod 0600 "$${before_artifacts}"; \
		before_fingerprint="$$($(workload_metadata_context) ./scripts/workload-plan-metadata.sh fingerprint \
			"$(TF)" "." "../.." "$${before_artifacts}")"; \
		$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) \
			-out="$${temp_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${temp_plan}"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${after_artifacts}"; \
		chmod 0600 "$${after_artifacts}"; \
		after_fingerprint="$$($(workload_metadata_context) ./scripts/workload-plan-metadata.sh fingerprint \
			"$(TF)" "." "../.." "$${after_artifacts}")"; \
		if [ "$${before_fingerprint}" != "$${after_fingerprint}" ]; then \
			printf "Refusing workload plan: configuration or release artifacts changed while Terraform was planning.\n" >&2; \
			exit 1; \
		fi; \
		./scripts/assert-workload-plan.sh \
			"$${temp_plan}" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)" "$(WORKLOAD_PACKER_TEMPLATE)" \
			"$${after_artifacts}"; \
		./scripts/assert-network-hardening-normal-plan.sh \
			"$${temp_plan}" "$(TF)" "$(TERRAFORM_ENVIRONMENT)"; \
		./scripts/assert-workload-quota.sh \
			"$(WORKLOAD_TOPOLOGY_POLICY)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)" \
			"post-cluster"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh write \
			"$${temp_plan}" "$${temp_manifest}" "$(TF)" "." "../.." \
			"$${after_artifacts}" "$${after_fingerprint}"; \
		mv -f -- "$${temp_manifest}" "$(WORKLOAD_PLAN_MANIFEST)"; \
		mv -f -- "$${temp_plan}" "$(WORKLOAD_PLAN)"; \
		rm -f -- "$${before_artifacts}" "$${after_artifacts}"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; \
		lease_acquired=false; \
		rmdir "$${temp_dir}"; \
		trap - EXIT HUP INT TERM
	@ printf "Saved one-workcell plan and provenance are ready for review: %s and %s\n" "$(WORKLOAD_PLAN)" "$(WORKLOAD_PLAN_MANIFEST)"

workload-apply:
	@ if [ "$(CONFIRM)" != "$(WORKLOAD_CONFIRMATION)" ]; then \
		printf "Refusing workload apply. Re-run with CONFIRM='$(WORKLOAD_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ $(MAKE) workload-context-guard
	@ set -eu; \
		temp_dir="$$(mktemp -d ".workload-apply.$(ENV).XXXXXX")"; \
		before_artifacts="$${temp_dir}/artifacts-before.json"; \
		after_artifacts="$${temp_dir}/artifacts-after.json"; \
		apply_plan="$${temp_dir}/reviewed.plan"; \
		apply_manifest="$${temp_dir}/reviewed.plan.manifest"; \
		consumed_plan="$${temp_dir}/consumed.plan"; \
		consumed_manifest="$${temp_dir}/consumed.plan.manifest"; \
		lease_token="$${temp_dir}/lease-token.json"; \
		lease_acquired=false; \
		plan_moved=false; \
		manifest_moved=false; \
		cleanup() { \
			status=$$?; \
			restore_failed=false; \
			trap - EXIT HUP INT TERM; \
			if [ "$${plan_moved}" = true ]; then \
				if [ ! -e "$(WORKLOAD_PLAN)" ] \
					&& mv "$${consumed_plan}" "$(WORKLOAD_PLAN)"; then \
					plan_moved=false; \
				else \
					printf "Could not restore consumed workload plan; preserving private recovery directory %s.\n" "$${temp_dir}" >&2; \
					restore_failed=true; \
					status=1; \
				fi; \
			fi; \
			if [ "$${manifest_moved}" = true ]; then \
				if [ ! -e "$(WORKLOAD_PLAN_MANIFEST)" ] \
					&& mv "$${consumed_manifest}" "$(WORKLOAD_PLAN_MANIFEST)"; then \
					manifest_moved=false; \
				else \
					printf "Could not restore consumed workload manifest; preserving private recovery directory %s.\n" "$${temp_dir}" >&2; \
					restore_failed=true; \
					status=1; \
				fi; \
			fi; \
			if [ "$${lease_acquired}" = true ]; then \
				if ! "$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; then \
					printf "Shared rollout lease release failed; inspect it before another rollout.\n" >&2; \
					if [ "$${status}" -eq 0 ]; then status=1; fi; \
				fi; \
			fi; \
			if [ "$${restore_failed}" = false ]; then \
				rm -rf -- "$${temp_dir}"; \
			fi; \
			exit "$${status}"; \
		}; \
		trap cleanup EXIT HUP INT TERM; \
		umask 077; \
		holder_digest="$$( \
			{ \
				git -C ../.. rev-parse --verify HEAD; \
				printf '%s\n' "$(ENV)" "$$$$" "$$(date -u +%s)"; \
			} | shasum -a 256 | awk '{print $$1}' \
		)"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" acquire \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" \
			"apply:$${holder_digest}" "$${lease_token}"; \
		lease_acquired=true; \
		test -f "$(WORKLOAD_PLAN)" || { \
			printf "Missing reviewed workload plan %s. Run 'make workload-plan' first.\n" "$(WORKLOAD_PLAN)" >&2; \
			exit 1; \
		}; \
		test -f "$(WORKLOAD_PLAN_MANIFEST)" || { \
			printf "Missing workload plan provenance %s. Run 'make workload-plan' first.\n" "$(WORKLOAD_PLAN_MANIFEST)" >&2; \
			exit 1; \
		}; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${before_artifacts}"; \
		chmod 0600 "$${before_artifacts}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$(WORKLOAD_PLAN)" "$(WORKLOAD_PLAN_MANIFEST)" "$(TF)" "." "../.." \
			"$${before_artifacts}"; \
		cp "$(WORKLOAD_PLAN)" "$${apply_plan}"; \
		cp "$(WORKLOAD_PLAN_MANIFEST)" "$${apply_manifest}"; \
		chmod 0600 "$${apply_plan}" "$${apply_manifest}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$${apply_plan}" "$${apply_manifest}" "$(TF)" "." "../.." \
			"$${before_artifacts}"; \
		./scripts/assert-workload-plan.sh \
			"$${apply_plan}" "$(TF)" \
			"$(WORKLOAD_TOPOLOGY_POLICY)" "$(WORKLOAD_PACKER_TEMPLATE)" \
			"$${before_artifacts}"; \
		./scripts/assert-network-hardening-normal-plan.sh \
			"$${apply_plan}" "$(TF)" "$(TERRAFORM_ENVIRONMENT)"; \
		./scripts/assert-workload-quota.sh \
			"$(WORKLOAD_TOPOLOGY_POLICY)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)" \
			"post-cluster"; \
		./scripts/assert-workload-artifacts.sh \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PREFIX)" \
			"$(WORKLOAD_IMAGE_REVISION)" "$(WORKLOAD_JOB_BINARY_BUCKET)" \
			"$(GCLOUD)" \
			>"$${after_artifacts}"; \
		chmod 0600 "$${after_artifacts}"; \
		$(workload_metadata_context) ./scripts/workload-plan-metadata.sh verify \
			"$${apply_plan}" "$${apply_manifest}" "$(TF)" "." "../.." \
			"$${after_artifacts}"; \
		"$(WORKLOAD_ROLLOUT_LEASE)" assert-held \
			"$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${lease_token}"; \
		$(TF) apply -input=false "$${apply_plan}"; \
		set +e; \
		$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) \
			-out="$${temp_dir}/post-apply.plan" \
			-input=false \
			-compact-warnings \
			-detailed-exitcode \
			>/dev/null; \
		post_apply_status=$$?; \
		set -e; \
		if [ "$${post_apply_status}" -ne 0 ]; then \
			printf "Post-apply convergence check was not clean (Terraform exit %s).\n" \
					"$${post_apply_status}" >&2; \
			exit 1; \
		fi; \
		if ! cmp -s "$(WORKLOAD_PLAN)" "$${apply_plan}" \
			|| ! cmp -s "$(WORKLOAD_PLAN_MANIFEST)" "$${apply_manifest}"; then \
			printf "Applied reviewed bytes, but the published plan changed concurrently; preserving the replacement.\n" >&2; \
			exit 1; \
		fi; \
		mv "$(WORKLOAD_PLAN)" "$${consumed_plan}"; \
		plan_moved=true; \
		mv "$(WORKLOAD_PLAN_MANIFEST)" "$${consumed_manifest}"; \
		manifest_moved=true; \
		"$(WORKLOAD_ROLLOUT_LEASE)" release "$(GCLOUD)" "$${lease_token}"; \
		lease_acquired=false; \
		plan_moved=false; \
		manifest_moved=false; \
		rm -rf -- "$${temp_dir}"; \
		trap - EXIT HUP INT TERM

.PHONY: foundation-init
foundation-init: foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ printf "Initializing keyless foundation state for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ ./scripts/ensure-foundation-state-bucket.sh \
		"$(TERRAFORM_STATE_BUCKET)" \
		"$(GCP_PROJECT_ID)" \
		"$(GCP_REGION)" \
		"$(GCLOUD)"
	@ $(TF) init -input=false -reconfigure \
		-backend-config=bucket="$(TERRAFORM_STATE_BUCKET)" \
		-backend-config=prefix="$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-backend.sh "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-workspace.sh "$(TF)"
	@ ./scripts/assert-foundation-state.sh "$(TF)"

.PHONY: foundation-scrub-legacy-acl-token-state
foundation-scrub-legacy-acl-token-state: foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ if [ "$(CONFIRM)" != "$(ACL_STATE_SCRUB_CONFIRMATION)" ]; then \
		printf "Refusing state-only ACL migration. Re-run with CONFIRM='$(ACL_STATE_SCRUB_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ ./scripts/assert-foundation-backend.sh "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-state-bucket.sh \
		"$(TERRAFORM_STATE_BUCKET)" \
		"$(GCP_PROJECT_ID)" \
		"$(GCP_REGION)" \
		"$(GCLOUD)"
	@ ./scripts/assert-foundation-workspace.sh "$(TF)"
	@ ./scripts/scrub-legacy-acl-token-state.sh "$(TF)"

.PHONY: foundation-plan
foundation-plan:
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ rm -f -- "$(FOUNDATION_PLAN)" "$(FOUNDATION_PLAN_MANIFEST)"
	@ $(MAKE) foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ printf "Planning keyless foundation only for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ ./scripts/assert-foundation-backend.sh "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-state-bucket.sh \
		"$(TERRAFORM_STATE_BUCKET)" \
		"$(GCP_PROJECT_ID)" \
		"$(GCP_REGION)" \
		"$(GCLOUD)"
	@ ./scripts/assert-foundation-workspace.sh "$(TF)"
	@ ./scripts/assert-foundation-state.sh "$(TF)"
	@ $(call tf_fmt)
	@ set -eu; \
		before_fingerprint="$$($(foundation_metadata_context) ./scripts/foundation-plan-metadata.sh fingerprint "$(TF)" "." "../..")"; \
		temp_dir="$$(mktemp -d ".foundation-plan.$(ENV).XXXXXX")"; \
		temp_plan="$${temp_dir}/plan"; \
		temp_manifest="$${temp_dir}/manifest"; \
		trap 'rm -rf -- "$${temp_dir}"' EXIT HUP INT TERM; \
		umask 077; \
		$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) \
			-var=anywhere_cache_enabled=false \
			-target=module.init \
			-out="$${temp_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${temp_plan}"; \
		after_fingerprint="$$($(foundation_metadata_context) ./scripts/foundation-plan-metadata.sh fingerprint "$(TF)" "." "../..")"; \
		if [ "$${before_fingerprint}" != "$${after_fingerprint}" ]; then \
			printf "Refusing foundation plan: configuration changed while Terraform was planning.\n" >&2; \
			exit 1; \
		fi; \
		./scripts/assert-foundation-plan.sh \
			"$${temp_plan}" "$(TF)" apply \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(ENV)"; \
		$(foundation_metadata_context) ./scripts/foundation-plan-metadata.sh write \
			"$${temp_plan}" "$${temp_manifest}" "$(TF)" "." "../.." "$${after_fingerprint}"; \
		mv -f -- "$${temp_plan}" "$(FOUNDATION_PLAN)"; \
		mv -f -- "$${temp_manifest}" "$(FOUNDATION_PLAN_MANIFEST)"; \
		rmdir "$${temp_dir}"; \
		trap - EXIT HUP INT TERM

.PHONY: foundation-apply
foundation-apply: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ if [ "$(CONFIRM)" != "$(FOUNDATION_CONFIRMATION)" ]; then \
		printf "Refusing foundation apply. Re-run with CONFIRM='$(FOUNDATION_CONFIRMATION)'.\n"; \
		exit 1; \
	fi
	@ test -f $(FOUNDATION_PLAN) || (printf "Missing reviewed plan $(FOUNDATION_PLAN). Run 'make foundation-plan' first.\n"; exit 1)
	@ test -f $(FOUNDATION_PLAN_MANIFEST) || (printf "Missing plan provenance $(FOUNDATION_PLAN_MANIFEST). Run 'make foundation-plan' first.\n"; exit 1)
	@ ./scripts/assert-foundation-backend.sh "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-state-bucket.sh \
		"$(TERRAFORM_STATE_BUCKET)" \
		"$(GCP_PROJECT_ID)" \
		"$(GCP_REGION)" \
		"$(GCLOUD)"
	@ ./scripts/assert-foundation-workspace.sh "$(TF)"
	@ ./scripts/assert-foundation-state.sh "$(TF)"
	@ $(foundation_metadata_context) ./scripts/foundation-plan-metadata.sh verify \
		"$(FOUNDATION_PLAN)" "$(FOUNDATION_PLAN_MANIFEST)" "$(TF)" "." "../.."
	@ ./scripts/assert-foundation-plan.sh \
		"$(FOUNDATION_PLAN)" "$(TF)" apply \
		"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(ENV)"
	@ $(TF) apply -input=false $(FOUNDATION_PLAN)
	@ rm "$(FOUNDATION_PLAN)" "$(FOUNDATION_PLAN_MANIFEST)"

.PHONY: foundation-destroy-plan
foundation-destroy-plan:
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ rm -f -- "$(FOUNDATION_DESTROY_PLAN)"
	@ $(MAKE) foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ printf "Planning review-only foundation destroy for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ ./scripts/assert-foundation-backend.sh "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)"
	@ ./scripts/assert-foundation-state-bucket.sh \
		"$(TERRAFORM_STATE_BUCKET)" \
		"$(GCP_PROJECT_ID)" \
		"$(GCP_REGION)" \
		"$(GCLOUD)"
	@ ./scripts/assert-foundation-workspace.sh "$(TF)"
	@ ./scripts/assert-foundation-state.sh "$(TF)"
	@ set -eu; \
		temp_dir="$$(mktemp -d ".foundation-destroy-plan.$(ENV).XXXXXX")"; \
		temp_plan="$${temp_dir}/plan"; \
		trap 'rm -rf -- "$${temp_dir}"' EXIT HUP INT TERM; \
		umask 077; \
		$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) \
			-var=anywhere_cache_enabled=false \
			-target=module.init \
			-destroy \
			-out="$${temp_plan}" \
			-input=false \
			-compact-warnings; \
		chmod 0600 "$${temp_plan}"; \
		./scripts/assert-foundation-plan.sh \
			"$${temp_plan}" "$(TF)" destroy \
			"$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(ENV)"; \
		mv -f -- "$${temp_plan}" "$(FOUNDATION_DESTROY_PLAN)"; \
		rmdir "$${temp_dir}"; \
		trap - EXIT HUP INT TERM
	@ printf "Destroy plan is review-only; this branch intentionally provides no apply target.\n"

.PHONY: state-migrate
state-migrate: foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ printf "Running Terraform state migrations for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) state mv 'random_password.api_admin_secret' 'module.init.random_password.api_admin_secret' || true
	@ $(tf_vars) $(TF) state mv 'google_secret_manager_secret.api_admin_token' 'module.init.google_secret_manager_secret.api_admin_token' || true
	@ $(tf_vars) $(TF) state mv 'google_secret_manager_secret_version.api_admin_token_value' 'module.init.google_secret_manager_secret_version.api_admin_token_value' || true
	@ $(tf_vars) $(TF) state mv 'random_password.dashboard_api_admin_secret' 'module.init.random_password.dashboard_api_admin_secret' || true
	@ $(tf_vars) $(TF) state mv 'google_secret_manager_secret.dashboard_api_admin_token' 'module.init.google_secret_manager_secret.dashboard_api_admin_token' || true
	@ $(tf_vars) $(TF) state mv 'google_secret_manager_secret_version.dashboard_api_admin_token_value' 'module.init.google_secret_manager_secret_version.dashboard_api_admin_token_value' || true
	@ $(tf_vars) $(TF) state mv 'module.nomad.random_password.clickhouse_password' 'random_password.clickhouse_password' || true
	@ $(tf_vars) $(TF) state mv 'module.nomad.random_password.clickhouse_server_secret' 'random_password.clickhouse_server_secret' || true

.PHONY: apply-init
apply-init:
	@ printf "Refusing unreviewed foundation apply. Use foundation-plan and confirmed foundation-apply.\n"
	@ exit 1

.PHONY: switch
switch:
	@ printf "Refusing legacy backend switch. Select the environment and run foundation-init so bucket and prefix bindings are verified.\n"
	@ exit 1

.PHONY: destroy
destroy:
	@ printf "Refusing legacy workload destroy. No reviewed workload destroy/apply workflow exists yet.\n"
	@ exit 1

.PHONY: plan
plan:
	@ printf "Refusing legacy workload plan. Use the reviewed workload release workflow once it lands.\n"
	@ exit 1

.PHONY: terraform
terraform: foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Terraform command for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(call tf_fmt)
	@ $(tf_vars) $(TF) $(ARGS)

# Deploy all jobs in Nomad
# When job name is specified, all '-' are replaced with '_' in the job name
# Clickhouse jobs are scoped in Clickhouse submodule so we need special handling for them
plan_only_jobs_moved_targets = -target="random_password.clickhouse_password" -target="random_password.clickhouse_server_secret" -target="module.nomad.random_password.clickhouse_password" -target="module.nomad.random_password.clickhouse_server_secret"

.PHONY: plan-only-jobs plan-only-jobs/clickhouse plan-only-jobs/clickhouse_migrator plan-only-jobs/clickhouse_backup plan-only-jobs/clickhouse_backup_restore
plan-only-jobs/clickhouse: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse" $(plan_only_jobs_moved_targets)
plan-only-jobs/clickhouse_migrator: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse_migrator" $(plan_only_jobs_moved_targets)
plan-only-jobs/clickhouse_backup: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse_backup" $(plan_only_jobs_moved_targets)
plan-only-jobs/clickhouse_backup_restore: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse_backup_restore" $(plan_only_jobs_moved_targets)
plan-only-jobs/%: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad.nomad_job.$(subst -,_,$(notdir $@)) -target=module.nomad.module.$(subst -,_,$(notdir $@)) $(plan_only_jobs_moved_targets)
plan-only-jobs: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad $(plan_only_jobs_moved_targets)

.PHONY: plan-without-jobs
plan-without-jobs: foundation-config-guard foundation-toolchain-guard keyless-runtime-check acl-token-state-guard
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(eval TARGET := $(shell cat main.tf | grep "^module" | awk '{print $$2}' | tr ' ' '\n' | grep -v -e "nomad" | awk '{print "-target=module." $$0 ""}' | xargs))
	@ $(tf_vars) \
	$(TF) plan \
	$(TF_VAR_FILE_ARG) \
	-out=.tfplan.$(ENV) \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	$(TARGET)

.PHONY: apply
apply:
	@ printf "Refusing legacy workload apply. Apply requires a reviewed saved plan, provenance, topology check, and exact confirmation.\n"
	@ exit 1

# Shortcut to importing resources into Terraform state (e.g. after creating resources manually or switching between different branches for the same environment)
.PHONY: import
import: foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ printf "Importing resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) import $(TF_VAR_FILE_ARG) "$(TARGET)" "$(ID)" -no-color

.PHONY: move
move: foundation-config-guard foundation-toolchain-guard keyless-runtime-check
	@ printf "Moving resource for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) state mv "$(SOURCE)" "$(TARGET)" -no-color

# Login for Packer and Docker (uses gcloud user creds)
# Login for Terraform (uses application default creds)
.PHONY: provider-login
provider-login:
	gcloud --quiet auth login --update-adc
	gcloud config set project "$(GCP_PROJECT_ID)"
	gcloud --quiet auth configure-docker "$(GCP_REGION)-docker.pkg.dev"
