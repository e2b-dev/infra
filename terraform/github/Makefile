ENV := $(shell cat ../.last_used_env || echo "not-set")
-include ../.env.${ENV}

tf_vars := 	TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_gcp_zone=$(GCP_ZONE) \
	TF_VAR_domain_name=$(DOMAIN_NAME) \
	TF_VAR_prefix=$(PREFIX) \
	TF_VAR_terraform_state_bucket=$(TERRAFORM_STATE_BUCKET) \
	TF_VAR_environment=$(TERRAFORM_ENVIRONMENT) \
	TF_VAR_template_bucket_name=$(TEMPLATE_BUCKET_NAME) \
	TF_VAR_template_bucket_location=$(TEMPLATE_BUCKET_LOCATION)



.PHONY: init
init:
	@ printf "Initializing Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	../scripts/confirm.sh $(ENV)
	terraform init -input=false -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"

.PHONY: plan
plan:
	@ printf "Planning Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	terraform fmt -recursive
	$(tf_vars) terraform plan -out=.tfplan.$(ENV) -compact-warnings -detailed-exitcode

.PHONY: apply
apply:
	@ printf "Applying Terraform for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	../scripts/confirm.sh $(ENV)
	$(tf_vars) \
	terraform apply \
	-auto-approve \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	.tfplan.$(ENV)
	@ rm .tfplan.$(ENV)


# Shortcut to importing resources into Terraform state (e.g. after creating resources manually or switching between different branches for the same environment)
.PHONY: import
import:
	@ printf "Importing resources for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	./scripts/confirm.sh $(ENV)
	$(tf_vars) terraform import $(TARGET) $(ID)
