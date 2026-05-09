.PHONY: help init plan apply destroy validate fmt fmt-check e2e-test test-modules

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

# ─────────────────────────── NX.2.8 E2E-Tests ───────────────────────────

e2e-test:
	@echo "═══ NX.2.8 E2E-Test Pipeline ═══"
	@echo ""
	@echo "[1/3] terraform fmt -check -recursive..."
	terraform fmt -check -recursive
	@echo ""
	@echo "[2/3] terraform init -backend=false (no remote state)..."
	rm -rf .terraform/
	terraform init -backend=false -input=false -upgrade
	@echo ""
	@echo "[3/3] terraform validate..."
	terraform validate
	@echo ""
	@echo "✅ Hetzner-Provider NX.2.1-NX.2.7 alle Module validate sauber."

test-modules:
	@echo "═══ Standalone Module fmt-check ═══"
	@for d in init modules/network modules/cloudflare modules/dns modules/cert modules/cloud-lb modules/redis modules/nodepool-api modules/nodepool-clickhouse modules/nodepool-client modules/nodepool-control-server nomad-cluster nomad-cluster/worker-cluster; do \
		echo "→ $$d"; \
		(cd "$$d" && terraform fmt -check) || exit 1; \
	done
	@echo ""
	@echo "✅ All modules pass fmt-check."
