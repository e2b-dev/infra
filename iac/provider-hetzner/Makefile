.PHONY: help init plan apply destroy validate fmt fmt-check

# Hetzner-Provider Terraform Workflow Helpers
#
# Usage:
#   1. Source the env file: source ../../.env.hetzner
#   2. Run: make init
#   3. Run: make plan
#   4. Run: make apply  (after review)

ENV_FILE ?= ../../.env.hetzner
TF_BACKEND_BUCKET ?= maxicore-tfstate
TF_BACKEND_REGION ?= fsn1
TF_BACKEND_ENDPOINT ?= https://fsn1.your-objectstorage.com

help:
	@echo "Hetzner-Provider — Terraform Workflow"
	@echo ""
	@echo "Required env (source $(ENV_FILE) before running):"
	@echo "  TF_VAR_hetzner_api_token"
	@echo "  TF_VAR_hetzner_object_storage_access_key"
	@echo "  TF_VAR_hetzner_object_storage_secret_key"
	@echo "  TF_VAR_domain_name"
	@echo "  TF_VAR_environment"
	@echo ""
	@echo "Targets:"
	@echo "  make init       — terraform init with Hetzner Object Storage backend"
	@echo "  make validate   — terraform validate (no provider download)"
	@echo "  make fmt        — terraform fmt -recursive"
	@echo "  make fmt-check  — terraform fmt -check (CI-friendly)"
	@echo "  make plan       — terraform plan"
	@echo "  make apply      — terraform apply (requires review)"
	@echo "  make destroy    — terraform destroy (requires allow_force_destroy=true)"

init:
	terraform init \
		-backend-config="bucket=$(TF_BACKEND_BUCKET)" \
		-backend-config="region=$(TF_BACKEND_REGION)" \
		-backend-config="endpoint=$(TF_BACKEND_ENDPOINT)" \
		-backend-config="access_key=$(TF_VAR_hetzner_object_storage_access_key)" \
		-backend-config="secret_key=$(TF_VAR_hetzner_object_storage_secret_key)"

validate:
	terraform validate

fmt:
	terraform fmt -recursive

fmt-check:
	terraform fmt -check -recursive

plan:
	terraform plan -out=plan.tfplan

apply:
	terraform apply plan.tfplan

destroy:
	terraform destroy
