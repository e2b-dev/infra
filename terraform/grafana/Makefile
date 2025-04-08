ENV := $(shell cat ../../.last_used_env || echo "not-set")
ENV_FILE := ../../.env.${ENV}
TERRAFORM_STATE_BUCKET ?= $(GCP_PROJECT_ID)-terraform-state

-include ${ENV_FILE}

tf_vars := TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_prefix=$(PREFIX) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_domain_name=$(DOMAIN_NAME)


.PHONY: init
init:
	@ printf "\nInitializing Grafana Terraform\n"
	terraform init -input=false -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"


.PHONY: plan
plan:
	@ printf "\nPlanning Grafana Terraform\n"
	terraform fmt -recursive
	$(tf_vars) terraform plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode



.PHONY: apply
apply:
	@ printf "\nApplying Grafana Terraform\n"
	$(tf_vars) terraform apply \
		-input=false \
		-compact-warnings \
		-parallelism=20 \
		.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)
