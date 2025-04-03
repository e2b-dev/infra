ENV := $(shell cat .last_used_env || echo "not-set")
ENV_FILE := $(PWD)/.env.${ENV}

-include ${ENV_FILE}

TF := $(shell which terraform)
TERRAFORM_STATE_BUCKET ?= $(GCP_PROJECT_ID)-terraform-state
OTEL_TRACING_PRINT ?= false
TEMPLATE_BUCKET_LOCATION ?= $(GCP_REGION)
CLIENT_CLUSTER_AUTO_SCALING_MAX ?= 0

tf_vars := TF_VAR_client_machine_type=$(CLIENT_MACHINE_TYPE) \
	TF_VAR_client_cluster_size=$(CLIENT_CLUSTER_SIZE) \
	TF_VAR_client_cluster_auto_scaling_max=$(CLIENT_CLUSTER_AUTO_SCALING_MAX) \
	TF_VAR_api_machine_type=$(API_MACHINE_TYPE) \
	TF_VAR_api_cluster_size=$(API_CLUSTER_SIZE) \
	TF_VAR_build_machine_type=$(BUILD_MACHINE_TYPE) \
	TF_VAR_build_cluster_size=$(BUILD_CLUSTER_SIZE) \
	TF_VAR_server_machine_type=$(SERVER_MACHINE_TYPE) \
	TF_VAR_server_cluster_size=$(SERVER_CLUSTER_SIZE) \
	TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_gcp_zone=$(GCP_ZONE) \
	TF_VAR_domain_name=$(DOMAIN_NAME) \
	TF_VAR_additional_domains=$(ADDITIONAL_DOMAINS) \
	TF_VAR_prefix=$(PREFIX) \
	TF_VAR_terraform_state_bucket=$(TERRAFORM_STATE_BUCKET) \
	TF_VAR_otel_tracing_print=$(OTEL_TRACING_PRINT) \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	TF_VAR_template_bucket_name=$(TEMPLATE_BUCKET_NAME) \
	TF_VAR_template_bucket_location=$(TEMPLATE_BUCKET_LOCATION) \
	TF_VAR_clickhouse_connection_string=$(CLICKHOUSE_CONNECTION_STRING) \
	TF_VAR_clickhouse_username=$(CLICKHOUSE_USERNAME) \
	TF_VAR_clickhouse_database=$(CLICKHOUSE_DATABASE)

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
	./scripts/confirm.sh $(ENV)
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
	@ hcp vault-secrets secrets read env_$(ENV) >/dev/null 2>&1 && echo "Environment found, writing to the .env.$(ENV) file" || { echo "Environment $(ENV) does not exist"; exit 1; }
	@ rm ".env.$(ENV)"
	@ hcp vault-secrets secrets open env_$(ENV) -o ".env.$(ENV)"
	@ DECODED=$$(cat ".env.$(ENV)" | base64 -d) && echo "$$DECODED" > ".env.$(ENV)"

# Updates production environment from .env file, this is used only for E2B.dev production
# Uses HCP CLI to update secrets from HCP Vault Secrets
.PHONY: update-prod-env
update-prod-env:
	@ hcp auth login
	@ hcp profile init --vault-secrets
	@ cat ".env.$(ENV)" | base64 -w 0 | tr -d '\n' | hcp vault-secrets secrets create env_$(ENV) --data-file=-

.PHONY: plan
plan:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(TF) fmt -recursive
	$(tf_vars) $(TF) plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode

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
	./scripts/confirm.sh $(ENV)
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
	./scripts/confirm.sh $(ENV)
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
build-and-upload/%:
	./scripts/confirm.sh $(ENV)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/$(notdir $@) build-and-upload

.PHONY: copy-public-builds
copy-public-builds:
	gsutil cp -r gs://e2b-prod-public-builds/envd-v0.0.1 gs://$(GCP_PROJECT_ID)-fc-env-pipeline/envd-v0.0.1
	gsutil cp -r gs://e2b-prod-public-builds/kernels/* gs://$(GCP_PROJECT_ID)-fc-kernels/
	gsutil cp -r gs://e2b-prod-public-builds/firecrackers/* gs://$(GCP_PROJECT_ID)-fc-versions/


.PHONY: migrate
migrate:
	$(MAKE) -C packages/db migrate-postgres/up
	# $(MAKE) -C packages/shared migrate-clickhouse/up


.PHONY: generate
generate:generate/api
generate:generate/orchestrator
generate:generate/template-manager
generate:generate/envd
generate:generate/db
generate:generate/orchestrator/internal
generate:generate/shared
generate/%:
	$(MAKE) -C packages/$(notdir $@) generate
	@printf "\n\n"


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
	./scripts/confirm.sh $(ENV)
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
	$(MAKE) -C packages/template-manager test

.PHONY: test-integration
test-integration:
	$(MAKE) -C tests/integration test

# $(MAKE) -C terraform/grafana init does not work b/c of the -include ${ENV_FILE} in the Makefile
# so we need to call the Makefile directly
# && cd - || cd - is used to handle the case where the command fails, we still want to cd -
.PHONY: grafana-init
grafana-init:
	@ printf "Initializing Grafana Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	cd terraform/grafana && make init && cd - || cd -

.PHONY: grafana-plan
grafana-plan:
	@ printf "Planning Grafana Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	cd terraform/grafana && make plan && cd - || cd -

.PHONY: grafana-apply
grafana-apply:
	@ printf "Applying Grafana Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	cd terraform/grafana && make apply && cd - || cd -

.PHONY: connect-orchestrator
connect-orchestrator:
	$(MAKE) -C tests/integration connect-orchestrator
