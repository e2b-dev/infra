ENV := $(shell cat .last_used_env || echo "not-set")
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
	$(call tfvar, SERVER_MACHINE_TYPE) \
	$(call tfvar, SERVER_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_CLUSTER_SIZE) \
	$(call tfvar, CLICKHOUSE_MACHINE_TYPE) \
	$(call tfvar, GCP_PROJECT_ID) \
	$(call tfvar, GCP_REGION) \
	$(call tfvar, GCP_ZONE) \
	$(call tfvar, DOMAIN_NAME) \
	$(call tfvar, ADDITIONAL_DOMAINS) \
	$(call tfvar, PREFIX) \
	$(call tfvar, TERRAFORM_STATE_BUCKET) \
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
	$(call tfvar, REDIS_MANAGED) \
	$(call tfvar, GRAFANA_MANAGED)

# Login for Packer and Docker (uses gcloud user creds)
# Login for Terraform (uses application default creds)
.PHONY: login-gcloud
login-gcloud:
	gcloud --quiet auth login
	gcloud config set project "$(GCP_PROJECT_ID)"
	gcloud --quiet auth configure-docker "$(GCP_REGION)-docker.pkg.dev"
	gcloud --quiet auth application-default login

.PHONY: init
init:
	@ printf "Initializing Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	gcloud storage buckets create gs://$(TERRAFORM_STATE_BUCKET) --location $(GCP_REGION) --project $(GCP_PROJECT_ID) --default-storage-class STANDARD  --uniform-bucket-level-access > /dev/null 2>&1 || true
	$(TF) init -input=false -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
	$(tf_vars) $(TF) apply -target=module.init -target=module.buckets -auto-approve -input=false -compact-warnings
	$(MAKE) -C packages/cluster-disk-image init build
	gcloud auth configure-docker "${GCP_REGION}-docker.pkg.dev" --quiet

# Setup production environment variables, this is used only for E2B.dev production
# Uses HCP CLI to read secrets from HCP Vault Secrets
.PHONY: download-prod-env
download-prod-env:
	@ hcp auth login
	@ hcp profile init --vault-secrets
	@  ./scripts/download-prod-env.sh ${ENV}

# Updates production environment from .env file, this is used only for E2B.dev production
# Uses HCP CLI to update secrets from HCP Vault Secrets
.PHONY: update-prod-env
update-prod-env:
	@ hcp auth login
	@ hcp profile init --vault-secrets
	@ ./scripts/update-prod-env.sh ${ENV}

.PHONY: plan
plan:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ $(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode; \
	status=$$?; \
	if [ $$status -eq 0 ]; then \
		echo "No changes."; \
	elif [ $$status -eq 2 ]; then \
		echo "Changes detected."; \
	else \
		echo "Error during plan."; \
		exit $$status; \
	fi

# Deploy all jobs in Nomad
.PHONY: plan-only-jobs
plan-only-jobs:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode -target=module.nomad;

# Deploy a specific job name in Nomad
# When job name is specified, all '-' are replaced with '_' in the job name
.PHONY: plan-only-jobs/%
plan-only-jobs/%:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	@ $(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode -target=module.nomad.nomad_job.$$(echo "$(notdir $@)" | tr '-' '_');

.PHONY: apply
apply:
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	$(tf_vars) \
	$(TF) apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)

.PHONY: plan-without-jobs
plan-without-jobs:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(eval TARGET := $(shell cat main.tf | grep "^module" | awk '{print $$2}' | tr ' ' '\n' | grep -v -e "nomad" | awk '{print "-target=module." $$0 ""}' | xargs))
	$(tf_vars) \
	$(TF) plan \
	-out=.tfplan.$(ENV) \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	$(TARGET)

.PHONY: destroy
destroy:
	@ printf "Destroying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	$(tf_vars) \
	$(TF) destroy \
	-compact-warnings \
	-parallelism=20 \
	$$(terraform state list | grep module | cut -d'.' -f1,2 | grep -v -e "buckets" | uniq | awk '{print "-target=" $$0 ""}' | xargs)

.PHONY: version
version:
	./scripts/increment-version.sh

.PHONY: build
build/%:
	$(MAKE) -C packages/$(notdir $@) build

.PHONY: build-and-upload
build-and-upload:build-and-upload/api
build-and-upload:build-and-upload/client-proxy
build-and-upload:build-and-upload/docker-reverse-proxy
build-and-upload:build-and-upload/orchestrator
build-and-upload:build-and-upload/template-manager
build-and-upload:build-and-upload/envd
build-and-upload:build-and-upload/clickhouse-migrator
build-and-upload/template-manager:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload/template-manager
build-and-upload/orchestrator:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload/orchestrator
build-and-upload/clickhouse-migrator:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/clickhouse build-and-upload
build-and-upload/%:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/$(notdir $@) build-and-upload

.PHONY: copy-public-builds
copy-public-builds:
	gsutil cp -r gs://e2b-prod-public-builds/kernels/* gs://$(GCP_PROJECT_ID)-fc-kernels/
	gsutil cp -r gs://e2b-prod-public-builds/firecrackers/* gs://$(GCP_PROJECT_ID)-fc-versions/


.PHONY: generate
generate: generate/api generate/orchestrator generate/client-proxy generate/envd generate/db
generate/%:
	@echo "Generating code for *$(notdir $@)*"
	$(MAKE) -C packages/$(notdir $@) generate
	@printf "\n\n"

.PHONY: migrate
migrate:
	$(MAKE) -C packages/db migrate/up
	# $(MAKE) -C packages/shared migrate-clickhouse/up

.PHONY: switch-env
switch-env:
	@ touch .last_used_env
	@ printf "Switching from `tput setaf 1``tput bold`$(shell cat .last_used_env)`tput sgr0` to `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ echo $(ENV) > .last_used_env
	@ . ${ENV_FILE}
	terraform init -input=false -upgrade -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"

# Shortcut to importing resources into Terraform state (e.g. after creating resources manually or switching between different branches for the same environment)
.PHONY: import
import:
	@ printf "Importing resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	$(tf_vars) $(TF) import $(TARGET) $(ID)

.PHONY: setup-ssh
setup-ssh:
	@ printf "Setting up SSH for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n"
	@ gcloud compute config-ssh --remove
	@ gcloud compute config-ssh --project $(GCP_PROJECT_ID) --quiet
	@ printf "SSH setup complete\n"

.PHONY: test
test:
	$(MAKE) -C packages/api test
	$(MAKE) -C packages/client-proxy test
	$(MAKE) -C packages/docker-reverse-proxy test
	$(MAKE) -C packages/envd test
	$(MAKE) -C packages/orchestrator test
	$(MAKE) -C packages/shared test

.PHONY: test-integration
test-integration:
	$(MAKE) -C tests/integration test

.PHONY: connect-orchestrator
connect-orchestrator:
	$(MAKE) -C tests/integration connect-orchestrator

.PHONY: fmt
fmt:
	@./scripts/golangci-lint-install.sh "2.1.6"
	golangci-lint fmt
