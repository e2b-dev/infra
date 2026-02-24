ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := ../../.env.${ENV}

-include ${ENV_FILE}

TF := $(shell which terraform)
TF_VAR_FILE := ./.terraform.${ENV}.tfvars
TF_VAR_FILE_ARG := $(if $(wildcard $(TF_VAR_FILE)),-var-file=$(TF_VAR_FILE))
TERRAFORM_STATE_BUCKET ?= $(AWS_ACCOUNT_ID)-e2b-terraform-state
BUCKET_PREFIX ?= $(AWS_ACCOUNT_ID)-

define tfvar
$(if $(value $(strip $(1))), TF_VAR_$(shell echo $(strip $(1)) | tr A-Z a-z)=$($(strip $(1))))
endef

tf_vars := \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	$(call tfvar, BUILD_CLUSTERS_CONFIG) \
	$(call tfvar, CLIENT_CLUSTERS_CONFIG) \
	$(call tfvar, API_INSTANCE_TYPE) \
	$(call tfvar, API_CLUSTER_SIZE) \
	$(call tfvar, SERVER_INSTANCE_TYPE) \
	$(call tfvar, SERVER_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_CLUSTER_SIZE) \
	$(call tfvar, LOKI_CLUSTER_SIZE) \
	$(call tfvar, AWS_REGION) \
	$(call tfvar, AVAILABILITY_ZONES) \
	$(call tfvar, AMI_ID) \
	$(call tfvar, DOMAIN_NAME) \
	$(call tfvar, PREFIX) \
	$(call tfvar, ALLOW_SANDBOX_INTERNET) \
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
	$(call tfvar, TEMPLATE_BUCKET_NAME) \
	$(call tfvar, ENVD_TIMEOUT) \
	$(call tfvar, REDIS_MANAGED) \
	$(call tfvar, EFS_CACHE_ENABLED) \
	$(call tfvar, BUCKET_PREFIX) \
	$(call tfvar, LOKI_USE_V13_SCHEMA_FROM)

.PHONY: init
init:
	@ printf "Initializing Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ aws s3api create-bucket \
		--bucket $(TERRAFORM_STATE_BUCKET) \
		--region $(AWS_REGION) \
		--create-bucket-configuration LocationConstraint=$(AWS_REGION) 2>/dev/null || true

	# Enable versioning on the state bucket
	@ aws s3api put-bucket-versioning \
		--bucket $(TERRAFORM_STATE_BUCKET) \
		--versioning-configuration Status=Enabled

	# Enable server-side encryption
	@ aws s3api put-bucket-encryption \
		--bucket $(TERRAFORM_STATE_BUCKET) \
		--server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'

	# Block public access
	@ aws s3api put-public-access-block \
		--bucket $(TERRAFORM_STATE_BUCKET) \
		--public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

	@ $(TF) init -input=false -reconfigure \
		-backend-config=bucket=$(TERRAFORM_STATE_BUCKET) \
		-backend-config=region=$(AWS_REGION)
	@ $(tf_vars) $(TF) apply -target=module.init -auto-approve -input=false -compact-warnings

.PHONY: apply-init
apply-init:
	@ $(tf_vars) $(TF) apply $(TF_VAR_FILE_ARG) -target=module.init -auto-approve -input=false -compact-warnings

.PHONY: switch
switch:
	@ printf "Switching Terraform backend for different env ${ENV}\n"
	@ $(TF) init -input=false -upgrade -reconfigure \
		-backend-config=bucket=$(TERRAFORM_STATE_BUCKET) \
		-backend-config=region=$(AWS_REGION)

.PHONY: destroy
destroy:
	@ printf "Destroying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) destroy $(TF_VAR_FILE_ARG)

.PHONY: plan
plan:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings

.PHONY: terraform
terraform:
	@ printf "Terraform command for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) fmt -recursive
	@ $(tf_vars) $(TF) $(ARGS)

.PHONY: plan-only-jobs
plan-only-jobs:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad

.PHONY: plan-only-jobs/%
plan-only-jobs/%:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan $(TF_VAR_FILE_ARG) -out=.tfplan.$(ENV) -compact-warnings -target=module.nomad.nomad_job.$$(echo "$(notdir $@)" | tr '-' '_') -target=module.nomad.module.$$(echo "$(notdir $@)" | tr '-' '_');

.PHONY: plan-without-jobs
plan-without-jobs:
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
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)

.PHONY: import
import:
	@ printf "Importing resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) import $(TF_VAR_FILE_ARG) "$(TARGET)" "$(ID)" -no-color

.PHONY: provider-login
provider-login:
	aws ecr get-login-password --region $(AWS_REGION) --profile $(AWS_PROFILE) | docker login --username AWS --password-stdin $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
