ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := ../../.env.${ENV}

-include ${ENV_FILE}

TF := $(shell which terraform)
TERRAFORM_STATE_BUCKET ?= $(AWS_ACCOUNT_ID)-terraform-state
TEMPLATE_BUCKET_LOCATION ?= $(AWS_REGION)

BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-

# Set the terraform environment variable only if the environment variable is set
# Strip the passed variable name (it's space sensitive) and check if the variable is set, if yes return TF_VAR_<variable_name>=<value> with the variable name in lower case
define tfvar
$(if $(value $(strip $(1))), TF_VAR_$(shell echo $(strip $(1)) | tr A-Z a-z)=$($(strip $(1))))
endef

tf_vars := AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	$(call tfvar, PREFIX) \
	$(call tfvar, DOMAIN_NAME) \
	$(call tfvar, BUCKET_PREFIX) \
	$(call tfvar, CLIENT_CLUSTER_SIZE) \
	$(call tfvar, CLIENT_SERVER_MACHINE_TYPE) \
	$(call tfvar, CLIENT_SERVER_NESTED_VIRTUALIZATION) \
	$(call tfvar, CLICKHOUSE_CLUSTER_SIZE) \
	$(call tfvar, REDIS_INSTANCE_TYPE) \
	$(call tfvar, REDIS_MANAGED) \
	$(call tfvar, API_CLUSTER_SIZE) \
	$(call tfvar, API_SERVER_MACHINE_TYPE) \
	$(call tfvar, CLIENT_PROXY_COUNT) \
	$(call tfvar, INGRESS_COUNT) \
	$(call tfvar, BUILD_CLUSTER_SIZE) \
	$(call tfvar, BUILD_SERVER_MACHINE_TYPE) \
	$(call tfvar, BUILD_SERVER_NESTED_VIRTUALIZATION) \
	$(call tfvar, AWS_ACCOUNT_ID) \
	$(call tfvar, AWS_REGION) \
	$(call tfvar, DB_MAX_OPEN_CONNECTIONS) \
	$(call tfvar, DB_MIN_IDLE_CONNECTIONS) \
	$(call tfvar, AUTH_DB_MAX_OPEN_CONNECTIONS) \
	$(call tfvar, AUTH_DB_MIN_IDLE_CONNECTIONS)

.PHONY: provider-login
provider-login:
	aws ecr get-login-password --region $(AWS_REGION) --profile $(AWS_PROFILE) | docker login --username AWS --password-stdin $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com

.PHONY: switch
switch:
	$(tf_vars) $(TF) init -input=false -upgrade -reconfigure -backend-config="bucket=$(TERRAFORM_STATE_BUCKET)"

.PHONY: init
init:
	# Create S3 bucket for Terraform state if it doesn't exist
	# us-east-1 requires omitting LocationConstraint; all other regions require it
	@if [ "$(TEMPLATE_BUCKET_LOCATION)" = "us-east-1" ]; then \
		aws s3api create-bucket --bucket $(TERRAFORM_STATE_BUCKET) --region $(TEMPLATE_BUCKET_LOCATION) --profile $(AWS_PROFILE) 2>/dev/null || true; \
	else \
		aws s3api create-bucket --bucket $(TERRAFORM_STATE_BUCKET) --region $(TEMPLATE_BUCKET_LOCATION) --profile $(AWS_PROFILE) --create-bucket-configuration LocationConstraint=$(TEMPLATE_BUCKET_LOCATION) 2>/dev/null || true; \
	fi
	$(tf_vars) $(TF) init -upgrade -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
	$(tf_vars) $(TF) apply -target=module.init -input=false -compact-warnings

.PHONY: plan
plan:
	$(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -input=false -compact-warnings -parallelism=20

.PHONY: plan-without-jobs
plan-without-jobs:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(eval TARGET := $(shell cat main.tf | grep "^module" | awk '{print $$2}' | tr ' ' '\n' | grep -v -e "nomad" | awk '{print "-target=module." $$0 ""}' | xargs))
	$(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -input=false -compact-warnings -parallelism=20 $(TARGET)

.PHONY: apply
apply:
	$(tf_vars) $(TF) apply -auto-approve -input=false -compact-warnings -parallelism=20 .tfplan.$(ENV)

.PHONY: state-migrate
state-migrate:
	@ printf "Running Terraform state migrations for env: $(ENV)\n\n"

.PHONY: apply-init
apply-init:
	$(tf_vars) $(TF) apply -target=module.init -auto-approve -input=false -compact-warnings

.PHONY: move
move:
	@ printf "Moving resource for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(tf_vars) $(TF) state mv "$(SOURCE)" "$(TARGET)" -no-color

.PHONY: destroy
destroy:
	$(tf_vars) $(TF) destroy
