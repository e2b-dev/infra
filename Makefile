ENV := $(shell cat .last_used_env || echo "not-set")
-include .env.${ENV}

OTEL_TRACING_PRINT ?= false
IMAGE := e2b-orchestration/api

tf_vars := TF_VAR_client_machine_type=$(CLIENT_MACHINE_TYPE) \
	TF_VAR_client_cluster_size=$(CLIENT_CLUSTER_SIZE) \
	TF_VAR_server_machine_type=$(SERVER_MACHINE_TYPE) \
	TF_VAR_server_cluster_size=$(SERVER_CLUSTER_SIZE) \
	TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_gcp_zone=$(GCP_ZONE) \
	TF_VAR_domain_name=$(DOMAIN_NAME) \
	TF_VAR_prefix=$(PREFIX) \
	TF_VAR_terraform_state_bucket=$(TERRAFORM_STATE_BUCKET) \
	TF_VAR_otel_tracing_print=$(OTEL_TRACING_PRINT) \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT)

ifeq ($(EXCLUDE_GITHUB),1)
	ALL_MODULES := $(shell cat main.tf | grep "^module" | awk '{print $$2}' | grep -v -e "github_tf")
else
	ALL_MODULES := $(shell cat main.tf | grep "^module" | awk '{print $$2}')
endif

WITHOUT_JOBS := $(shell echo $(ALL_MODULES) | tr ' ' '\n' | grep -v -e "nomad" | awk '{print "-target=module." $$0 ""}' | xargs)
ALL_MODULES_ARGS := $(shell echo $(ALL_MODULES) | tr ' ' '\n' | awk '{print "-target=module." $$0 ""}' | xargs)
DESTROY_TARGETS := $(shell terraform state list | grep module | cut -d'.' -f1,2 | grep -v -e "fc_envs_disk" -e "buckets" | uniq | awk '{print "-target=" $$0 ""}' | xargs)

# Login for Packer and Docker (uses gcloud user creds)
# Login for Terraform (uses application default creds)
.PHONY: login-gcloud
login-gcloud:
	gcloud auth login
	gcloud config set project "$(GCP_PROJECT_ID)"
	gcloud --quiet auth configure-docker "$(GCP_REGION)-docker.pkg.dev"
	gcloud auth application-default login

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
	$(tf_vars) terraform plan -compact-warnings -detailed-exitcode $(ALL_MODULES_ARGS)

.PHONY: apply
apply:
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) \
	terraform apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 $(ALL_MODULES_ARGS)

.PHONY: plan-without-jobs
plan-without-jobs:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	$(tf_vars) \
	terraform plan \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
  	$(WITHOUT_JOBS)

.PHONY: apply-without-jobs
apply-without-jobs:
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) \
	terraform apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
  	$(WITHOUT_JOBS)

.PHONY: destroy
destroy:
	@ printf "Destroying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) \
	terraform destroy \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	$(DESTROY_TARGETS)


.PHONY: version
version:
	./scripts/increment-version.sh

.PHONY: build-all
build-all:
	$(MAKE) -C packages/envd build
	$(MAKE) -C packages/api build
	$(MAKE) -C packages/docker-reverse-proxy build
	$(MAKE) -C packages/orchestrator build
	$(MAKE) -C packages/template-manager build
	$(MAKE) -C packages/fc-kernels build
	$(MAKE) -C packages/fc-versions build

.PHONY: build-and-upload-all
build-and-upload-all:
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) make update-api
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/docker-reverse-proxy build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/template-manager build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/envd build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/fc-kernels build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/fc-versions build-and-upload

.PHONY: update-api
update-api:
	docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push -f api.Dockerfile .


.PHONY: switch-env
switch-env:
	@ touch .last_used_env
	@ printf "Switching from `tput setaf 1``tput bold`$(shell cat .last_used_env)`tput sgr0` to `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ echo $(ENV) > .last_used_env
	@ . .env.${ENV}
	terraform init -input=false -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
