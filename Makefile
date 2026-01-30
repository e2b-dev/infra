ENV := $(shell cat .last_used_env || echo "not-set")
ENV_FILE := $(PWD)/.env.${ENV}

-include ${ENV_FILE}

# Select provider (defaults to gcp when not specified)
PROVIDER ?= gcp
PROVIDER_DIR := iac/provider-$(PROVIDER)

# Docker tag for image pushes (defaults to latest)
DOCKER_TAG ?= latest

# Login for Packer and Docker (uses gcloud user creds)
# Login for Terraform (uses application default creds)
.PHONY: login
login:
	@if [ "$(PROVIDER)" = "gcp" ]; then \
	  gcloud --quiet auth login; \
	  gcloud config set project "$(GCP_PROJECT_ID)"; \
	  gcloud --quiet auth configure-docker "$(GCP_REGION)-docker.pkg.dev"; \
	  gcloud --quiet auth application-default login; \
	else \
	  echo "No login required for provider '$(PROVIDER)'"; \
	fi

.PHONY: init
init:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	$(MAKE) -C $(PROVIDER_DIR) init

# Setup production environment variables, this is used only for E2B.dev production
# Uses Infisical CLI to read secrets from Infisical Vault
# To update them, use the Infisical UI directly
# On a first use, you need to run `infisical login` and `infisical init`
.PHONY: download-prod-env
download-prod-env:
	@  ./scripts/download-prod-env.sh ${ENV}

.PHONY: plan
plan:
	$(MAKE) -C $(PROVIDER_DIR) plan

# Deploy all jobs in Nomad
.PHONY: plan-only-jobs
plan-only-jobs:
	$(MAKE) -C $(PROVIDER_DIR) plan-only-jobs

# Deploy a specific job name in Nomad
# When job name is specified, all '-' are replaced with '_' in job name
.PHONY: plan-only-jobs/%
plan-only-jobs/%:
	$(MAKE) -C $(PROVIDER_DIR) plan-only-jobs/$(subst -,_,$(notdir $@))

# Firewall management targets
.PHONY: plan-firewall
plan-firewall:
	$(MAKE) -C $(PROVIDER_DIR) plan-firewall

.PHONY: apply-firewall
apply-firewall:
	$(MAKE) -C $(PROVIDER_DIR) apply-firewall

.PHONY: taint-firewall
taint-firewall:
	$(MAKE) -C $(PROVIDER_DIR) taint-firewall

# Single node firewall management
.PHONY: plan-firewall/%
plan-firewall/%:
	$(MAKE) -C $(PROVIDER_DIR) plan-firewall/$(notdir $@)

.PHONY: apply-firewall/%
apply-firewall/%:
	$(MAKE) -C $(PROVIDER_DIR) apply-firewall/$(notdir $@)

.PHONY: taint-firewall/%
taint-firewall/%:
	$(MAKE) -C $(PROVIDER_DIR) taint-firewall/$(notdir $@)

# Helper to show detected hosts
.PHONY: show-firewall-hosts
show-firewall-hosts:
	$(MAKE) -C $(PROVIDER_DIR) show-firewall-hosts

.PHONY: plan-without-jobs
plan-without-jobs:
	$(MAKE) -C $(PROVIDER_DIR) plan-without-jobs

.PHONY: apply
apply:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	$(MAKE) -C $(PROVIDER_DIR) apply

# Shortcut to importing resources into Terraform state (e.g. after creating resources manually or switching between different branches for the same environment)
.PHONY: import
import:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	$(MAKE) -C $(PROVIDER_DIR) import

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
build-and-upload:build-and-upload/clean-nfs-cache
build-and-upload:build-and-upload/orchestrator
build-and-upload:build-and-upload/template-manager
build-and-upload:build-and-upload/envd
build-and-upload:build-and-upload/clickhouse-migrator
build-and-upload/clean-nfs-cache:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload/clean-nfs-cache
build-and-upload/template-manager:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload/template-manager
build-and-upload/orchestrator:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/orchestrator build-and-upload/orchestrator
build-and-upload/api:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/api build-and-upload
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/db build-and-upload
build-and-upload/clickhouse-migrator:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/clickhouse build-and-upload
build-and-upload/%:
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) $(MAKE) -C packages/$(notdir $@) build-and-upload

.PHONY: build-and-upload-linux
build-and-upload-linux:
	@if [ "$(PROVIDER)" != "linux" ]; then echo "build-and-upload-linux is only applicable for provider=linux"; exit 0; fi
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/api build-and-upload-linux
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/client-proxy build-and-upload-linux
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/docker-reverse-proxy build-and-upload-linux
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/db build-and-upload-linux
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/orchestrator build-and-upload/orchestrator
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/orchestrator build-and-upload/template-manager
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/clickhouse build-and-upload-linux
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/envd build-and-upload
build-and-upload-linux/%:
	@if [ "$(PROVIDER)" != "linux" ]; then echo "build-and-upload-linux is only applicable for provider=linux"; exit 0; fi
	./scripts/confirm.sh $(TERRAFORM_ENVIRONMENT)
	DOCKER_TAG=$(DOCKER_TAG) $(MAKE) -C packages/$(notdir $@) build-and-upload-linux

.PHONY: copy-public-builds
copy-public-builds:
	@if [ "$(PROVIDER)" != "gcp" ]; then echo "copy-public-builds is only applicable for provider=gcp"; exit 0; fi
	gsutil cp -r gs://e2b-prod-public-builds/kernels/* gs://$(GCP_PROJECT_ID)-fc-kernels/
	gsutil cp -r gs://e2b-prod-public-builds/firecrackers/* gs://$(GCP_PROJECT_ID)-fc-versions/

.PHONY: download-public-kernels
download-public-kernels:
	mkdir -p ./packages/fc-kernels
	gsutil cp -r gs://e2b-prod-public-builds/kernels/* ./packages/fc-kernels/

.PHONY: generate
generate: generate/api generate/orchestrator generate/client-proxy generate/envd generate/db generate/shared generate-tests generate-mocks
generate/%:
	@echo "Generating code for *$(notdir $@)*"
	$(MAKE) -C packages/$(notdir $@) generate
	@printf "\n\n"

.PHONY: generate-tests
generate-tests: generate-tests/integration
generate-tests/%:
		@echo "Generating code for *$(notdir $@)*"
		$(MAKE) -C tests/$(notdir $@) generate
		@printf "\n\n"

.PHONY: migrate
migrate:
	$(MAKE) -C packages/db migrate

.PHONY: switch-env
switch-env:
	@ touch .last_used_env
	@ printf "Switching from `tput setaf 1``tput bold`$(shell cat .last_used_env)`tput sgr0` to `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n\n"
	@ echo $(ENV) > .last_used_env
	@if [ "$(PROVIDER)" = "gcp" ]; then \
	  $(MAKE) -C iac/provider-gcp switch; \
	else \
	  echo "No backend switch required for provider '$(PROVIDER)'"; \
	fi

.PHONY: setup-ssh
setup-ssh:
	@ printf "Setting up SSH for env: `tput setaf 2``tput bold`$(ENV)`tput sgr0`\n"
	@ gcloud compute config-ssh --remove
	@ gcloud compute config-ssh --project $(GCP_PROJECT_ID) --quiet
	@ printf "SSH setup complete\n"

.PHONY: test
test:
	go work edit -json \
		| jq -r '.Use[] | select (.DiskPath | contains("packages")) | .DiskPath' \
		| xargs -I{} $(MAKE) -C {} test

.PHONY: test-integration
test-integration:
	$(MAKE) -C tests/integration test

.PHONY: connect-orchestrator
connect-orchestrator:
	$(MAKE) -C tests/integration connect-orchestrator

.PHONY: fmt
fmt:
	@./scripts/golangci-lint-install.sh "2.4.0"
	golangci-lint fmt
	terraform fmt -recursive

.PHONY: lint
lint:
	@./scripts/golangci-lint-install.sh "2.4.0"
	go work edit -json | jq -r '.Use[].DiskPath' | xargs -P 10 -I{} golangci-lint run {}/... --fix

.PHONY: generate-mocks
generate-mocks:
	go run github.com/vektra/mockery/v3@v3.5.0

.PHONY: tidy
tidy:
	scripts/golang-dependencies-integrity.sh

.PHONY: local-infra
local-infra:
	docker compose --file ./packages/local-dev/docker-compose.yaml up --abort-on-container-failure
