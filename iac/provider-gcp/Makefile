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
override FOUNDATION_CONFIRMATION := APPLY KEYLESS FOUNDATION

define foundation_metadata_context
FOUNDATION_ENV="$(ENV)" \
FOUNDATION_ENV_FILE="$(ENV_FILE)" \
FOUNDATION_TF_VAR_FILE="$(TF_VAR_FILE)" \
FOUNDATION_GCP_PROJECT_ID="$(GCP_PROJECT_ID)" \
FOUNDATION_GCP_REGION="$(GCP_REGION)" \
FOUNDATION_STATE_BUCKET="$(TERRAFORM_STATE_BUCKET)" \
FOUNDATION_STATE_PREFIX="$(TERRAFORM_STATE_PREFIX)"
endef

.PHONY: runtime-blocked
runtime-blocked:
	@ printf "Refusing workload Terraform in the F1.0 foundation branch. Only foundation-init, foundation-plan, and confirmed foundation-apply are supported.\n"
	@ exit 1

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

.PHONY: foundation-init
foundation-init: foundation-config-guard foundation-toolchain-guard
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

.PHONY: foundation-plan
foundation-plan:
	@ printf '%s' "$(ENV)" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$$' || (printf "Invalid environment name: %s\n" "$(ENV)"; exit 1)
	@ rm -f -- "$(FOUNDATION_PLAN)" "$(FOUNDATION_PLAN_MANIFEST)"
	@ $(MAKE) foundation-config-guard foundation-toolchain-guard
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
foundation-apply: foundation-config-guard foundation-toolchain-guard
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
	@ $(MAKE) foundation-config-guard foundation-toolchain-guard
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
state-migrate: runtime-blocked
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
apply-init: runtime-blocked
	@ $(tf_vars) $(TF) apply $(TF_VAR_FILE_ARG) -target=module.init -auto-approve -input=false -compact-warnings

.PHONY: switch
switch: runtime-blocked
	@ printf "Switching Terraform backend for different env ${ENV}\n"
	@ $(TF) init -input=false -upgrade -reconfigure -backend-config=bucket=$(TERRAFORM_STATE_BUCKET)

.PHONY: destroy
destroy: runtime-blocked
	@ printf "Destroying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) destroy $(TF_VAR_FILE_ARG)

.PHONY: plan
plan: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(call tf_fmt)
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings

.PHONY: terraform
terraform: runtime-blocked
	@ printf "Terraform command for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(call tf_fmt)
	@ $(tf_vars) $(TF) $(ARGS)

# Deploy all jobs in Nomad
# When job name is specified, all '-' are replaced with '_' in the job name
# Clickhouse jobs are scoped in Clickhouse submodule so we need special handling for them
plan_only_jobs_moved_targets = -target="random_password.clickhouse_password" -target="random_password.clickhouse_server_secret" -target="module.nomad.random_password.clickhouse_password" -target="module.nomad.random_password.clickhouse_server_secret"

.PHONY: plan-only-jobs plan-only-jobs/clickhouse plan-only-jobs/clickhouse_migrator plan-only-jobs/clickhouse_backup plan-only-jobs/clickhouse_backup_restore
plan-only-jobs/clickhouse: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse" $(plan_only_jobs_moved_targets)
plan-only-jobs/clickhouse_migrator: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse_migrator" $(plan_only_jobs_moved_targets)
plan-only-jobs/clickhouse_backup: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse_backup" $(plan_only_jobs_moved_targets)
plan-only-jobs/clickhouse_backup_restore: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	$(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target="module.nomad.module.clickhouse.nomad_job.clickhouse_backup_restore" $(plan_only_jobs_moved_targets)
plan-only-jobs/%: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad.nomad_job.$(subst -,_,$(notdir $@)) -target=module.nomad.module.$(subst -,_,$(notdir $@)) $(plan_only_jobs_moved_targets)
plan-only-jobs: runtime-blocked
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(call tf_fmt)
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad $(plan_only_jobs_moved_targets)

.PHONY: plan-without-jobs
plan-without-jobs: runtime-blocked
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
apply: runtime-blocked
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)

# Shortcut to importing resources into Terraform state (e.g. after creating resources manually or switching between different branches for the same environment)
.PHONY: import
import: runtime-blocked
	@ printf "Importing resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) import $(TF_VAR_FILE_ARG) "$(TARGET)" "$(ID)" -no-color

.PHONY: move
move: runtime-blocked
	@ printf "Moving resource for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) state mv "$(SOURCE)" "$(TARGET)" -no-color

# Login for Packer and Docker (uses gcloud user creds)
# Login for Terraform (uses application default creds)
.PHONY: provider-login
provider-login:
	gcloud --quiet auth login --update-adc
	gcloud config set project "$(GCP_PROJECT_ID)"
	gcloud --quiet auth configure-docker "$(GCP_REGION)-docker.pkg.dev"
