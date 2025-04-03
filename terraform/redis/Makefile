ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := $(PWD)/../../.env.${ENV}
TERRAFORM_STATE_BUCKET ?= $(GCP_PROJECT_ID)-terraform-state

-include ${ENV_FILE}

tf_vars := TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_prefix=$(PREFIX) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_gcp_zone=$(GCP_ZONE)

.PHONY: init
init:
	@ printf "Initializing Terraform\n"
	terraform init -input=false -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
	$(tf_vars) terraform import google_compute_subnetwork.default "projects/$(GCP_PROJECT_ID)/regions/$(GCP_REGION)/subnetworks/default"


.PHONY: plan
plan:
	@ printf "Planning Terraform\n"
	terraform fmt -recursive
	$(tf_vars) terraform plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode



.PHONY: apply
apply:
	@ printf "Applying Terraform\n"
	$(tf_vars) terraform apply \
		-input=false \
		-compact-warnings \
		-parallelism=20 \
		.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)
