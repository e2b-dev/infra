ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := $(PWD)/.env.${ENV}

-include ${ENV_FILE}

TF := $(shell which terraform)
TERRAFORM_STATE_BUCKET ?= $(GCP_PROJECT_ID)-terraform-state
TEMPLATE_BUCKET_LOCATION ?= $(GCP_REGION)

# Set the terraform environment variable only if the environment variable is set
# Strip the passed variable name (it's space sensitive) and check if the variable is set, if yes return TF_VAR_<variable_name>=<value> with the variable name in lower case
define tfvar
$(if $(value $(strip $(1))), TF_VAR_$(shell echo $(strip $(1)) | tr A-Z a-z)=$($(strip $(1))))
endef

tf_vars := 	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	$(call tfvar, CLIENT_MACHINE_TYPE) \
	$(call tfvar, CLIENT_CLUSTER_SIZE) \
	$(call tfvar, CLIENT_CLUSTER_SIZE_MAX) \
	$(call tfvar, CLIENT_CLUSTER_CACHE_DISK_SIZE_GB) \
	$(call tfvar, API_MACHINE_TYPE) \
	$(call tfvar, API_CLUSTER_SIZE) \
	$(call tfvar, BUILD_MACHINE_TYPE) \
	$(call tfvar, BUILD_CLUSTER_SIZE) \
	$(call tfvar, BUILD_CLUSTER_ROOT_DISK_SIZE_GB) \
	$(call tfvar, BUILD_CLUSTER_CACHE_DISK_SIZE_GB) \
	$(call tfvar, SERVER_MACHINE_TYPE) \
	$(call tfvar, SERVER_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_MACHINE_TYPE) \
	$(call tfvar, GCP_PROJECT_ID) \
	$(call tfvar, GCP_REGION) \
	$(call tfvar, GCP_ZONE) \
	$(call tfvar, DOMAIN_NAME) \
	$(call tfvar, ADDITIONAL_DOMAINS) \
	$(call tfvar, ADDITIONAL_API_SERVICES_JSON) \
	$(call tfvar, PREFIX) \
	$(call tfvar, OTEL_TRACING_PRINT) \
	$(call tfvar, ALLOW_SANDBOX_INTERNET) \
	$(call tfvar, CLIENT_PROXY_COUNT) \
	$(call tfvar, CLIENT_PROXY_CPU_COUNT) \
	$(call tfvar, CLIENT_PROXY_RESOURCES_MEMORY_MB) \
	$(call tfvar, CLICKHOUSE_RESOURCES_CPU_COUNT) \
	$(call tfvar, CLICKHOUSE_RESOURCES_MEMORY_MB) \
	$(call tfvar, LOKI_RESOURCES_CPU_COUNT) \
	$(call tfvar, LOKI_RESOURCES_MEMORY_MB) \
	$(call tfvar, OTEL_TRACING_PRINT) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_CPU_COUNT) \
	$(call tfvar, OTEL_COLLECTOR_RESOURCES_MEMORY_MB) \
	$(call tfvar, TEMPLATE_BUCKET_NAME) \
	$(call tfvar, TEMPLATE_BUCKET_LOCATION) \
	$(call tfvar, ENVD_TIMEOUT) \
	$(call tfvar, REDIS_MANAGED) \
	$(call tfvar, GRAFANA_MANAGED) \
	$(call tfvar, FILESTORE_CACHE_ENABLED) \
	$(call tfvar, FILESTORE_CACHE_TIER) \
	$(call tfvar, FILESTORE_CACHE_CAPACITY_GB) \
	$(call tfvar, BUILD_CLUSTER_CACHE_DISK_TYPE) \
	$(call tfvar, CLIENT_CLUSTER_CACHE_DISK_TYPE) \
	$(call tfvar, MIN_CPU_PLATFORM)

.PHONY: init
init:
	@ printf "Initializing Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	../../scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	gcloud storage buckets create gs://$(TERRAFORM_STATE_BUCKET) --location $(GCP_REGION) --project $(GCP_PROJECT_ID) --default-storage-class STANDARD  --uniform-bucket-level-access > /dev/null 2>&1 || true

	# Enable object versioning (keeps deleted/replaced objects as older versions)
	gcloud storage buckets update gs://$(TERRAFORM_STATE_BUCKET) --versioning --soft-delete-duration=30d

	# Create a temporary file for lifecycle rules
	$(eval LIFECYCLE_FILE := $(shell mktemp))

	# Set lifecycle rules to delete non-live objects after 30 days or more than 50 newer versions
	echo '{"rule":[{"action":{"type":"Delete"},"condition":{"isLive":false,"age":30}},{"action":{"type":"Delete"},"condition":{"numNewerVersions":50}}]}' > $(LIFECYCLE_FILE)
	gcloud storage buckets update gs://$(TERRAFORM_STATE_BUCKET) --lifecycle-file=$(LIFECYCLE_FILE)

	# Remove the temporary lifecycle file
	@ rm -f $(LIFECYCLE_FILE)

	#$(TF) init -input=false -reconfigure -backend-config=bucket=$(TERRAFORM_STATE_BUCKET)
	#$(tf_vars) $(TF) apply -target=module.init -target=module.buckets -auto-approve -input=false -compact-warnings
	# todo: remove target when you are already migrating (needed for moved resources)
	$(tf_vars) $(TF) apply -target=module.init -target=module.buckets -target=module.cluster -input=false -compact-warnings

	#$(tf_vars) $(TF) plan -out=plan.out -input=false -compact-warnings
	#TERRAFORM_STATE_BUCKET="$(TERRAFORM_STATE_BUCKET)" $(MAKE) -C packages/cluster-disk-image init build
	#gcloud auth configure-docker "${GCP_REGION}-docker.pkg.dev" --quiet
