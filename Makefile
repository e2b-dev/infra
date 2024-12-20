ENV := $(shell cat .last_used_env || echo "not-set")
-include .env.${ENV}

OTEL_TRACING_PRINT ?= false
EXCLUDE_GITHUB ?= 1

tf_vars := TF_VAR_client_machine_type=$(CLIENT_MACHINE_TYPE) \
	TF_VAR_client_cluster_size=$(CLIENT_CLUSTER_SIZE) \
	TF_VAR_api_machine_type=$(API_MACHINE_TYPE) \
	TF_VAR_api_cluster_size=$(API_CLUSTER_SIZE) \
	TF_VAR_server_machine_type=$(SERVER_MACHINE_TYPE) \
	TF_VAR_server_cluster_size=$(SERVER_CLUSTER_SIZE) \
	TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_gcp_zone=$(GCP_ZONE) \
	TF_VAR_domain_name=$(DOMAIN_NAME) \
	TF_VAR_prefix=$(PREFIX) \
	TF_VAR_terraform_state_bucket=$(TERRAFORM_STATE_BUCKET) \
	TF_VAR_otel_tracing_print=$(OTEL_TRACING_PRINT) \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	TF_VAR_template_bucket_name=$(TEMPLATE_BUCKET_NAME)

ifeq ($(EXCLUDE_GITHUB),1)
	ALL_MODULES := $(shell cat main.tf | grep "^module" | awk '{print $$2}' | grep -v -e "github_tf")
else
	ALL_MODULES := $(shell cat main.tf | grep "^module" | awk '{print $$2}')
endif

# Login for Packer and Docker (uses gcloud user creds)
# Login for Terraform (uses application default creds)
.PHONY: login-gcloud
login-gcloud:
	gcloud --quiet auth login
	gcloud config set project "$(GCP_PROJECT_ID)"
	gcloud --quiet auth configure-docker "$(GCP_REGION)-docker.pkg.dev"
	gcloud --quiet auth application-default login
	gcloud auth configure-docker "us-west-1-docker.pkg.dev"

.PHONY: init
init:
	@ printf "Initializing Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	terraform init -input=false -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
	$(MAKE) -C packages/cluster-disk-image init
	$(tf_vars) terraform apply -target=module.init -target=module.buckets -auto-approve -input=false -compact-warnings
	gcloud auth configure-docker "${GCP_REGION}-docker.pkg.dev" --quiet

.PHONY: plan
plan:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	terraform fmt -recursive
	$(tf_vars) terraform plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode $$(echo $(ALL_MODULES) | tr ' ' '\n' | awk '{print "-target=module." $$0 ""}' | xargs)

.PHONY: apply
apply:
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) \
	terraform apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)

.PHONY: plan-without-jobs
plan-without-jobs:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(tf_vars) \
	terraform plan \
	-out=.tfplan.$(ENV) \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
  	$$(echo $(ALL_MODULES) | tr ' ' '\n' | grep -v -e "nomad" | awk '{print "-target=module." $$0 ""}' | xargs)

.PHONY: destroy
destroy:
	@ printf "Destroying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) \
	terraform destroy \
	-compact-warnings \
	-parallelism=20 \
	$$(terraform state list | grep module | cut -d'.' -f1,2 | grep -v -e "buckets" | uniq | awk '{print "-target=" $$0 ""}' | xargs)


.PHONY: version
version:
	./scripts/increment-version.sh

.PHONY: build-and-upload
build-and-upload:
	$(MAKE) -C packages/cluster-disk-image build
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/api build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/docker-reverse-proxy build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/template-manager build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/envd build-and-upload

.PHONY: copy-public-builds
copy-public-builds:
	gsutil cp -r gs://e2b-prod-public-builds/envd-v0.0.1 gs://$(GCP_PROJECT_ID)-fc-env-pipeline/envd-v0.0.1
	gsutil cp -r gs://e2b-prod-public-builds/kernels/* gs://$(GCP_PROJECT_ID)-fc-kernels/
	gsutil cp -r gs://e2b-prod-public-builds/firecrackers/* gs://$(GCP_PROJECT_ID)-fc-versions/


migrate:
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/shared migrate

.PHONY: switch-env
switch-env:
	@ touch .last_used_env
	@ printf "Switching from `tput setaf 1``tput bold`$(shell cat .last_used_env)`tput sgr0` to `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ echo $(ENV) > .last_used_env
	@ . .env.${ENV}
	terraform init -input=false -upgrade -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"

# Shortcut to importing resources into Terraform state (e.g. after creating resources manually or switching between different branches for the same environment)
.PHONY: import
import:
	@ printf "Importing resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) terraform import $(TARGET) $(ID)

.PHONY: setup-ssh
setup-ssh:
	@ printf "Setting up SSH for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n"
	@ gcloud compute config-ssh --remove
	@ gcloud compute config-ssh --project $(GCP_PROJECT_ID) --quiet
	@ printf "SSH setup complete\n"