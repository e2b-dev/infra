ENV ?= $(shell cat ../../../.last_used_env 2>/dev/null || echo "not-set")
override ENV_FILE := ../../../.env.$(ENV)
override REPO_ROOT := $(abspath ../../..)
override PROVIDER_ROOT := $(abspath ..)
override CONFIG_ROOT := $(abspath .)

TF := $(shell command -v terraform 2>/dev/null)
PACKER := $(shell command -v packer 2>/dev/null)
GCLOUD := $(shell command -v gcloud 2>/dev/null)
override EXPECTED_TERRAFORM_VERSION := $(shell awk '$$1 == "terraform" { print $$2; exit }' ../../../.tool-versions)
override EXPECTED_PACKER_VERSION := $(shell awk '$$1 == "packer" { print $$2; exit }' ../../../.tool-versions)
override EXPECTED_GCLOUD_VERSION := $(shell awk '$$1 == "gcloud" { print $$2; exit }' ../../../.tool-versions)
override ENV_VALUE := ./scripts/read-env-value.sh

override GCP_PROJECT_ID := $(shell $(ENV_VALUE) "$(ENV_FILE)" GCP_PROJECT_ID)
override GCP_REGION := $(shell $(ENV_VALUE) "$(ENV_FILE)" GCP_REGION)
override GCP_ZONE := $(shell $(ENV_VALUE) "$(ENV_FILE)" GCP_ZONE)
override TERRAFORM_STATE_BUCKET := $(shell $(ENV_VALUE) "$(ENV_FILE)" TERRAFORM_STATE_BUCKET)
override TERRAFORM_STATE_BUCKET := $(if $(TERRAFORM_STATE_BUCKET),$(TERRAFORM_STATE_BUCKET),$(GCP_PROJECT_ID)-terraform-state)
override PREFIX := $(shell $(ENV_VALUE) "$(ENV_FILE)" PREFIX)
override PREFIX := $(if $(PREFIX),$(PREFIX),e2b-)

override TERRAFORM_STATE_PREFIX := terraform/cluster-disk-image/state
override ROOT_ARTIFACT_LOCK := $(CONFIG_ROOT)/setup/root-artifacts.lock.json
override ROOT_ARTIFACT_LOCK_SHA256 := $(shell shasum -a 256 "$(ROOT_ARTIFACT_LOCK)" 2>/dev/null | awk '{print $$1}')
override PACKER_NETWORK_NAME := e2b-build-cluster-disk-image
override PACKER_SUBNET_NAME := e2b-build-cluster-disk-image-subnetwork
override PACKER_CONSUL_VERSION := $(shell jq -er '.consul.version' "$(ROOT_ARTIFACT_LOCK)" 2>/dev/null)
override PACKER_NOMAD_VERSION := $(shell jq -er '.nomad.version' "$(ROOT_ARTIFACT_LOCK)" 2>/dev/null)
override PACKER_DOCKER_VERSION := $(shell jq -er '.docker_engine_version' "$(ROOT_ARTIFACT_LOCK)" 2>/dev/null)
override PACKER_SOURCE_IMAGE := ubuntu-2404-noble-amd64-v20260723
override PACKER_PLUGIN_PATH := $(CONFIG_ROOT)/.packer-plugins
override PACKER_SOURCE_REVISION := $(shell git -C "$(REPO_ROOT)" rev-parse --verify HEAD 2>/dev/null)
override PACKER_SOURCE_REVISION_SHORT := $(shell printf '%s' "$(PACKER_SOURCE_REVISION)" | cut -c1-12)
override PACKER_IMAGE_FAMILY := $(PREFIX)orch-$(ENV)-candidate
override PACKER_CANONICAL_IMAGE_FAMILY := $(PREFIX)orch
override PACKER_IMAGE_NAME := $(PACKER_IMAGE_FAMILY)-$(PACKER_SOURCE_REVISION_SHORT)
override PACKER_GATE_MAX_PLAN_AGE_SECONDS := 3600

override NETWORK_PLAN := .tfplan.network.$(ENV)
override NETWORK_PLAN_MANIFEST := $(NETWORK_PLAN).manifest
override PACKER_BUILD_MANIFEST := .packer-build-manifest.$(ENV).json
override ROLLOUT_LEASE_TOKEN := .rollout-lease-token.$(ENV).json
override LOCAL_LOCK_DIR := .operator-canary.$(ENV).lock
override POLICY_PATH := $(PROVIDER_ROOT)/topology/minimal-workload-policy.json
override PACKER_SMOKE_MACHINE_TYPE := $(shell jq -er '.transient_reserve.machine_type' "$(POLICY_PATH)" 2>/dev/null)
override PACKER_RESERVE_GUARD := $(PROVIDER_ROOT)/scripts/assert-packer-reserve.sh
override ROLLOUT_LEASE := $(PROVIDER_ROOT)/scripts/rollout-mutation-lease.sh

override tf_vars := \
	TF_VAR_gcp_project_id="$(GCP_PROJECT_ID)" \
	TF_VAR_gcp_region="$(GCP_REGION)" \
	TF_VAR_network_name="$(PACKER_NETWORK_NAME)" \
	TF_VAR_subnet_name="$(PACKER_SUBNET_NAME)"

override packer_vars := \
	-var "build_manifest_path=$(PACKER_BUILD_MANIFEST)" \
	-var "consul_version=$(PACKER_CONSUL_VERSION)" \
	-var "gcp_project_id=$(GCP_PROJECT_ID)" \
	-var "gcp_zone=$(GCP_ZONE)" \
	-var "image_environment=$(ENV)" \
	-var "image_family=$(PACKER_IMAGE_FAMILY)" \
	-var "image_name=$(PACKER_IMAGE_NAME)" \
	-var "network_name=$(PACKER_NETWORK_NAME)" \
	-var "nomad_version=$(PACKER_NOMAD_VERSION)" \
	-var "source_image=$(PACKER_SOURCE_IMAGE)" \
	-var "source_revision=$(PACKER_SOURCE_REVISION)" \
	-var "subnet_name=$(PACKER_SUBNET_NAME)"

override define metadata_context
PACKER_GATE_ENV="$(ENV)" \
PACKER_GATE_ENV_FILE="$(abspath $(ENV_FILE))" \
PACKER_GATE_GCP_PROJECT_ID="$(GCP_PROJECT_ID)" \
PACKER_GATE_GCP_REGION="$(GCP_REGION)" \
PACKER_GATE_GCP_ZONE="$(GCP_ZONE)" \
PACKER_GATE_STATE_BUCKET="$(TERRAFORM_STATE_BUCKET)" \
PACKER_GATE_STATE_PREFIX="$(TERRAFORM_STATE_PREFIX)" \
PACKER_GATE_NETWORK_NAME="$(PACKER_NETWORK_NAME)" \
PACKER_GATE_SUBNET_NAME="$(PACKER_SUBNET_NAME)" \
PACKER_GATE_CONSUL_VERSION="$(PACKER_CONSUL_VERSION)" \
PACKER_GATE_NOMAD_VERSION="$(PACKER_NOMAD_VERSION)" \
PACKER_GATE_SOURCE_IMAGE="$(PACKER_SOURCE_IMAGE)" \
PACKER_GATE_IMAGE_NAME="$(PACKER_IMAGE_NAME)" \
PACKER_GATE_IMAGE_FAMILY="$(PACKER_IMAGE_FAMILY)" \
PACKER_GATE_CANONICAL_IMAGE_FAMILY="$(PACKER_CANONICAL_IMAGE_FAMILY)" \
PACKER_GATE_SOURCE_REVISION="$(PACKER_SOURCE_REVISION)" \
PACKER_GATE_MAX_PLAN_AGE_SECONDS="$(PACKER_GATE_MAX_PLAN_AGE_SECONDS)" \
PACKER_PLUGIN_PATH="$(PACKER_PLUGIN_PATH)"
endef

.PHONY: init build destroy
init:
	@ printf "Refusing legacy init because it could select an unreviewed plugin/backend. Use 'make operator-canary-init ENV=dev'.\n"
	@ exit 1

build:
	@ printf "Refusing legacy build because it auto-applied network changes. Use operator-canary-plan, review its exact confirmation, then operator-canary-apply-build.\n"
	@ exit 1

destroy:
	@ printf "Destroy is intentionally unavailable in the operator-canary workflow. Use a separately reviewed Terraform change.\n"
	@ exit 1

.PHONY: format
format: operator-canary-toolchain-guard
	@ "$(TF)" fmt .
	@ "$(PACKER)" fmt .

.PHONY: operator-canary-toolchain-guard
operator-canary-toolchain-guard:
	@ command -v jq >/dev/null 2>&1 || (printf "jq is required.\n"; exit 1)
	@ test -n "$(TF)" && test -x "$(TF)" || (printf "Terraform is not installed.\n"; exit 1)
	@ test -n "$(PACKER)" && test -x "$(PACKER)" || (printf "Packer is not installed.\n"; exit 1)
	@ test -n "$(GCLOUD)" && test -x "$(GCLOUD)" || (printf "gcloud is not installed.\n"; exit 1)
	@ actual="$$("$(TF)" version -json | jq -er '.terraform_version')"; \
	test "$$actual" = "$(EXPECTED_TERRAFORM_VERSION)" || (printf "Terraform %s is required; found %s at %s.\n" "$(EXPECTED_TERRAFORM_VERSION)" "$$actual" "$(TF)"; exit 1)
	@ actual="$$("$(PACKER)" version -machine-readable | awk -F, '$$3 == "version" { print $$4; exit }')"; \
	test "$$actual" = "$(EXPECTED_PACKER_VERSION)" || (printf "Packer %s is required; found %s at %s.\n" "$(EXPECTED_PACKER_VERSION)" "$$actual" "$(PACKER)"; exit 1)
	@ actual="$$("$(GCLOUD)" version --format=json | jq -er '."Google Cloud SDK"')"; \
	test "$$actual" = "$(EXPECTED_GCLOUD_VERSION)" || (printf "gcloud %s is required; found %s at %s.\n" "$(EXPECTED_GCLOUD_VERSION)" "$$actual" "$(GCLOUD)"; exit 1)

.PHONY: operator-canary-static-check
operator-canary-static-check:
	@ PACKER_PLUGIN_PATH="$(PACKER_PLUGIN_PATH)" \
		./scripts/assert-operator-inputs.sh "$(CONFIG_ROOT)" "$(REPO_ROOT)" "$(PACKER_PLUGIN_PATH)" false
	@ ./scripts/assert-network-config.sh ./main.tf
	@ ./scripts/assert-packer-config.sh ./main.pkr.hcl ./variables.pkr.hcl
	@ "$(PACKER_RESERVE_GUARD)" "$(POLICY_PATH)" ./main.pkr.hcl
	@ find ./scripts "$(ROLLOUT_LEASE)" -type f -name '*.sh' -print0 | xargs -0 -n1 bash -n

.PHONY: operator-canary-config-guard
operator-canary-config-guard: operator-canary-static-check
	@ test "$(ENV)" = "dev" || (printf "The legacy singleton Packer-network state is authorised only for ENV=dev; found %s.\n" "$(ENV)"; exit 1)
	@ test -f "$(ENV_FILE)" && test ! -L "$(ENV_FILE)" || (printf "Missing or unsafe environment file: %s.\n" "$(ENV_FILE)"; exit 1)
	@ printf '%s' "$(GCP_PROJECT_ID)" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$$' || (printf "Invalid GCP_PROJECT_ID.\n"; exit 1)
	@ printf '%s' "$(GCP_REGION)" | grep -Eq '^[a-z]+-[a-z]+[0-9]$$' || (printf "Invalid GCP_REGION.\n"; exit 1)
	@ printf '%s' "$(GCP_ZONE)" | grep -Eq '^$(GCP_REGION)-[a-z]$$' || (printf "GCP_ZONE must belong to GCP_REGION.\n"; exit 1)
	@ printf '%s' "$(TERRAFORM_STATE_BUCKET)" | grep -Eq '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$$' || (printf "Invalid state bucket.\n"; exit 1)
	@ test "$(TERRAFORM_STATE_BUCKET)" = "$(GCP_PROJECT_ID)-terraform-state" || (printf "The shared rollout lease requires canonical state bucket %s-terraform-state; found %s.\n" "$(GCP_PROJECT_ID)" "$(TERRAFORM_STATE_BUCKET)"; exit 1)
	@ printf '%s' "$(PACKER_NETWORK_NAME)" | grep -Eq '^[a-z][a-z0-9-]{0,61}[a-z0-9]$$' || (printf "Invalid Packer network name.\n"; exit 1)
	@ printf '%s' "$(PACKER_SUBNET_NAME)" | grep -Eq '^[a-z][a-z0-9-]{0,61}[a-z0-9]$$' || (printf "Invalid Packer subnet name.\n"; exit 1)
	@ printf '%s' "$(PACKER_IMAGE_FAMILY)" | grep -Eq '^[a-z][a-z0-9-]{0,61}[a-z0-9]$$' || (printf "Invalid candidate image family.\n"; exit 1)
	@ printf '%s' "$(PACKER_CANONICAL_IMAGE_FAMILY)" | grep -Eq '^[a-z][a-z0-9-]{0,61}[a-z0-9]$$' || (printf "Invalid canonical image family.\n"; exit 1)
	@ test "$(PACKER_IMAGE_FAMILY)" != "$(PACKER_CANONICAL_IMAGE_FAMILY)" || (printf "Candidate and canonical image families must differ.\n"; exit 1)
	@ printf '%s' "$(PACKER_IMAGE_NAME)" | grep -Eq '^[a-z][a-z0-9-]{0,61}[a-z0-9]$$' || (printf "Invalid candidate image name.\n"; exit 1)
	@ PACKER_PLUGIN_PATH="$(PACKER_PLUGIN_PATH)" \
		./scripts/assert-operator-inputs.sh "$(CONFIG_ROOT)" "$(REPO_ROOT)" "$(PACKER_PLUGIN_PATH)" true

.PHONY: operator-canary-init
operator-canary-init: operator-canary-toolchain-guard operator-canary-config-guard
	@ "$(PACKER_RESERVE_GUARD)" "$(POLICY_PATH)" ./main.pkr.hcl
	@ rm -rf -- "$(PACKER_PLUGIN_PATH)"
	@ mkdir -m 0700 "$(PACKER_PLUGIN_PATH)"
	@ PACKER_PLUGIN_PATH="$(PACKER_PLUGIN_PATH)" "$(PACKER)" init .
	@ ./scripts/assert-packer-plugin.sh "$(PACKER)" "$(PACKER_PLUGIN_PATH)"
	@ "$(TF)" init -input=false -reconfigure \
		-backend-config=bucket="$(TERRAFORM_STATE_BUCKET)" \
		-backend-config=prefix="$(TERRAFORM_STATE_PREFIX)"
	@ "$(PROVIDER_ROOT)/scripts/assert-foundation-backend.sh" \
		"$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)" .terraform
	@ "$(PROVIDER_ROOT)/scripts/assert-foundation-workspace.sh" "$(TF)"
	@ "$(PROVIDER_ROOT)/scripts/assert-foundation-state-bucket.sh" \
		"$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)"

.PHONY: operator-canary-plan
operator-canary-plan: operator-canary-toolchain-guard operator-canary-config-guard
	@ set -eu; \
	test -d "$(PACKER_PLUGIN_PATH)" || (printf "Run 'make operator-canary-init ENV=dev' first.\n"; exit 1); \
	./scripts/assert-packer-plugin.sh "$(PACKER)" "$(PACKER_PLUGIN_PATH)" >/dev/null; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-backend.sh" "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)" .terraform; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-workspace.sh" "$(TF)"; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-state-bucket.sh" "$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)"; \
	"$(PACKER_RESERVE_GUARD)" "$(POLICY_PATH)" ./main.pkr.hcl; \
	./scripts/assert-live-capacity.sh "$(POLICY_PATH)" "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)"; \
	if "$(GCLOUD)" compute images describe "$(PACKER_IMAGE_NAME)" --project="$(GCP_PROJECT_ID)" --format='value(name)' >/dev/null 2>&1; then \
		printf "Candidate image already exists; this identity is one-shot: %s.\n" "$(PACKER_IMAGE_NAME)" >&2; \
		exit 1; \
	fi; \
	mkdir "$(LOCAL_LOCK_DIR)" 2>/dev/null || (printf "Another local operator-canary workflow holds %s.\n" "$(LOCAL_LOCK_DIR)"; exit 1); \
	temp_dir="$$(mktemp -d ".operator-plan.$(ENV).XXXXXX")"; \
	cleanup() { status=$$?; rm -rf -- "$${temp_dir}"; rmdir "$(LOCAL_LOCK_DIR)" 2>/dev/null || true; exit $$status; }; \
	trap cleanup EXIT HUP INT TERM; \
	rm -f -- "$(NETWORK_PLAN)" "$(NETWORK_PLAN_MANIFEST)"; \
	umask 077; \
	before_fingerprint="$$($(metadata_context) ./scripts/network-plan-metadata.sh fingerprint "$(TF)" "$(PACKER)" "$(GCLOUD)" "$(CONFIG_ROOT)" "$(REPO_ROOT)")"; \
	$(tf_vars) "$(TF)" plan -input=false -compact-warnings -out="$${temp_dir}/plan"; \
	chmod 0600 "$${temp_dir}/plan"; \
	./scripts/assert-network-plan.sh "$${temp_dir}/plan" "$(TF)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PACKER_NETWORK_NAME)" "$(PACKER_SUBNET_NAME)"; \
	after_fingerprint="$$($(metadata_context) ./scripts/network-plan-metadata.sh fingerprint "$(TF)" "$(PACKER)" "$(GCLOUD)" "$(CONFIG_ROOT)" "$(REPO_ROOT)")"; \
	test "$${before_fingerprint}" = "$${after_fingerprint}" || (printf "Inputs changed while Terraform planned.\n" >&2; exit 1); \
	$(metadata_context) ./scripts/network-plan-metadata.sh write "$${temp_dir}/plan" "$${temp_dir}/manifest" "$(TF)" "$(PACKER)" "$(GCLOUD)" "$(CONFIG_ROOT)" "$(REPO_ROOT)" "$${after_fingerprint}"; \
	PACKER_PLUGIN_PATH="$(PACKER_PLUGIN_PATH)" "$(PACKER)" validate -only=googlecompute.orch $(packer_vars) .; \
	mv "$${temp_dir}/plan" "$(NETWORK_PLAN)"; \
	mv "$${temp_dir}/manifest" "$(NETWORK_PLAN_MANIFEST)"; \
	chmod 0600 "$(NETWORK_PLAN)" "$(NETWORK_PLAN_MANIFEST)"; \
	"$(TF)" show -no-color "$(NETWORK_PLAN)"; \
	printf "\nReviewed provenance (contains no credentials):\n"; \
	jq -S . "$(NETWORK_PLAN_MANIFEST)"; \
	printf "\nExact one-use confirmation:\nCONFIRM='%s'\n" "$$($(metadata_context) ./scripts/network-plan-metadata.sh confirmation "$(NETWORK_PLAN_MANIFEST)")"; \
	rmdir "$(LOCAL_LOCK_DIR)"; \
	trap - EXIT HUP INT TERM

.PHONY: operator-canary-apply-build
operator-canary-apply-build: operator-canary-toolchain-guard operator-canary-config-guard
	@ set -eu; \
	test -f "$(NETWORK_PLAN)" && test ! -L "$(NETWORK_PLAN)" || (printf "Missing safe saved plan. Run operator-canary-plan.\n"; exit 1); \
	test -f "$(NETWORK_PLAN_MANIFEST)" && test ! -L "$(NETWORK_PLAN_MANIFEST)" || (printf "Missing plan provenance. Run operator-canary-plan.\n"; exit 1); \
	expected_confirmation="$$($(metadata_context) ./scripts/network-plan-metadata.sh confirmation "$(NETWORK_PLAN_MANIFEST)")"; \
	test "$(CONFIRM)" = "$${expected_confirmation}" || (printf "Confirmation mismatch. Re-read the exact value emitted by operator-canary-plan.\n"; exit 1); \
	mkdir "$(LOCAL_LOCK_DIR)" 2>/dev/null || (printf "Another local operator-canary workflow holds %s.\n" "$(LOCAL_LOCK_DIR)"; exit 1); \
	temp_dir="$$(mktemp -d ".operator-apply.$(ENV).XXXXXX")"; \
	lease_acquired=false; \
	promotion_started=false; \
	cleanup() { status=$$?; if [ "$${lease_acquired}" = true ] && [ "$${promotion_started}" = false ]; then "$(ROLLOUT_LEASE)" release "$(GCLOUD)" "$(ROLLOUT_LEASE_TOKEN)" || true; elif [ "$${lease_acquired}" = true ]; then printf "Canonical promotion started but did not verify; preserving the shared lease and token for manual recovery.\n" >&2; fi; rm -rf -- "$${temp_dir}"; rmdir "$(LOCAL_LOCK_DIR)" 2>/dev/null || true; exit $$status; }; \
	trap cleanup EXIT HUP INT TERM; \
	manifest_digest="$$(shasum -a 256 "$(NETWORK_PLAN_MANIFEST)" | awk '{print $$1}')"; \
	"$(ROLLOUT_LEASE)" acquire "$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${manifest_digest}" "$(ROLLOUT_LEASE_TOKEN)"; \
	lease_acquired=true; \
	./scripts/assert-packer-plugin.sh "$(PACKER)" "$(PACKER_PLUGIN_PATH)" >/dev/null; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-backend.sh" "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)" .terraform; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-workspace.sh" "$(TF)"; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-state-bucket.sh" "$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)"; \
	"$(PACKER_RESERVE_GUARD)" "$(POLICY_PATH)" ./main.pkr.hcl; \
	./scripts/assert-live-capacity.sh "$(POLICY_PATH)" "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)"; \
	$(metadata_context) ./scripts/network-plan-metadata.sh verify "$(NETWORK_PLAN)" "$(NETWORK_PLAN_MANIFEST)" "$(TF)" "$(PACKER)" "$(GCLOUD)" "$(CONFIG_ROOT)" "$(REPO_ROOT)"; \
	./scripts/assert-network-plan.sh "$(NETWORK_PLAN)" "$(TF)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(PACKER_NETWORK_NAME)" "$(PACKER_SUBNET_NAME)"; \
	if "$(GCLOUD)" compute images describe "$(PACKER_IMAGE_NAME)" --project="$(GCP_PROJECT_ID)" --format='value(name)' >/dev/null 2>&1; then \
		printf "Candidate image already exists; refusing replacement: %s.\n" "$(PACKER_IMAGE_NAME)" >&2; \
		exit 1; \
	fi; \
	"$(TF)" apply -input=false "$(NETWORK_PLAN)"; \
	if $(tf_vars) "$(TF)" plan -input=false -compact-warnings -detailed-exitcode; then :; else \
		printf "Post-apply drift check was not exactly clean; Packer will not run.\n" >&2; exit 1; \
	fi; \
	$(metadata_context) ./scripts/network-plan-metadata.sh verify-build-inputs "$(NETWORK_PLAN_MANIFEST)" "$(TF)" "$(PACKER)" "$(GCLOUD)" "$(CONFIG_ROOT)" "$(REPO_ROOT)"; \
	if "$(GCLOUD)" compute images describe "$(PACKER_IMAGE_NAME)" --project="$(GCP_PROJECT_ID)" --format='value(name)' >/dev/null 2>&1; then \
		printf "Candidate image appeared before Packer; refusing replacement.\n" >&2; exit 1; \
	fi; \
	rm -f -- "$(PACKER_BUILD_MANIFEST)"; \
	PACKER_PLUGIN_PATH="$(PACKER_PLUGIN_PATH)" "$(PACKER)" build -only=googlecompute.orch -parallel-builds=1 -on-error=cleanup $(packer_vars) .; \
	./scripts/assert-packer-artifact.sh "$(PACKER_BUILD_MANIFEST)" "$(GCP_PROJECT_ID)" "$(PACKER_IMAGE_NAME)" "$(PACKER_IMAGE_FAMILY)" "$(ENV)" "$(PACKER_SOURCE_REVISION)" "$(PACKER_SOURCE_IMAGE)" "$(ROOT_ARTIFACT_LOCK_SHA256)"; \
	./scripts/assert-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(PACKER_IMAGE_NAME)" "$(PACKER_IMAGE_FAMILY)" "$(ENV)" "$(PACKER_SOURCE_REVISION)"; \
	./scripts/smoke-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(GCP_ZONE)" "$(PACKER_IMAGE_NAME)" "$(PACKER_NETWORK_NAME)" "$(PACKER_SUBNET_NAME)" "$(PACKER_SMOKE_MACHINE_TYPE)" "$(PACKER_DOCKER_VERSION)" "$(PACKER_NOMAD_VERSION)" "$(PACKER_CONSUL_VERSION)" "$(ENV)" "$(PACKER_SOURCE_REVISION)"; \
	promotion_started=true; \
	./scripts/promote-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(PACKER_IMAGE_NAME)" "$(PACKER_IMAGE_FAMILY)" "$(PACKER_CANONICAL_IMAGE_FAMILY)" "$(ENV)" "$(PACKER_SOURCE_REVISION)"; \
	promotion_started=false; \
	"$(ROLLOUT_LEASE)" release "$(GCLOUD)" "$(ROLLOUT_LEASE_TOKEN)"; \
	lease_acquired=false; \
	rm -f -- "$(NETWORK_PLAN)" "$(NETWORK_PLAN_MANIFEST)"; \
	rmdir "$(LOCAL_LOCK_DIR)"; \
	trap - EXIT HUP INT TERM; \
	printf "Operator canary image completed: %s/%s.\n" "$(GCP_PROJECT_ID)" "$(PACKER_IMAGE_NAME)"

.PHONY: operator-canary-resume-promotion
operator-canary-resume-promotion: operator-canary-toolchain-guard operator-canary-config-guard
	@ set -eu; \
	test -f "$(PACKER_BUILD_MANIFEST)" && test ! -L "$(PACKER_BUILD_MANIFEST)" || (printf "Missing safe Packer build manifest.\n" >&2; exit 1); \
	recovery_revision="$$(jq -er '.builds[0].custom_data.source_revision' "$(PACKER_BUILD_MANIFEST)")"; \
	recovery_image="$$(jq -er '.builds[0].custom_data.image_name' "$(PACKER_BUILD_MANIFEST)")"; \
	printf '%s' "$${recovery_revision}" | grep -Eq '^[0-9a-f]{40}$$' || (printf "Invalid recovery revision.\n" >&2; exit 1); \
	recovery_short="$$(printf '%s' "$${recovery_revision}" | cut -c1-12)"; \
	test "$${recovery_image}" = "$(PACKER_IMAGE_FAMILY)-$${recovery_short}" || (printf "Recovery image does not match its exact revision.\n" >&2; exit 1); \
	expected_confirmation="RESUME PACKER CANARY image=$${recovery_image} revision=$${recovery_revision}"; \
	test "$(CONFIRM)" = "$${expected_confirmation}" || (printf "Confirmation mismatch. Required exact value:\nCONFIRM='%s'\n" "$${expected_confirmation}" >&2; exit 1); \
	git -C "$(REPO_ROOT)" merge-base --is-ancestor "$${recovery_revision}" HEAD || (printf "Recovery revision is not an ancestor of the reviewed checkout.\n" >&2; exit 1); \
	./scripts/assert-packer-artifact.sh "$(PACKER_BUILD_MANIFEST)" "$(GCP_PROJECT_ID)" "$${recovery_image}" "$(PACKER_IMAGE_FAMILY)" "$(ENV)" "$${recovery_revision}" "$(PACKER_SOURCE_IMAGE)" "$(ROOT_ARTIFACT_LOCK_SHA256)"; \
	./scripts/assert-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$${recovery_image}" "$(PACKER_IMAGE_FAMILY)" "$(ENV)" "$${recovery_revision}"; \
	mkdir "$(LOCAL_LOCK_DIR)" 2>/dev/null || (printf "Another local operator-canary workflow holds %s.\n" "$(LOCAL_LOCK_DIR)"; exit 1); \
	lease_acquired=false; \
	promotion_started=false; \
	cleanup() { status=$$?; if [ "$${lease_acquired}" = true ] && [ "$${promotion_started}" = false ]; then "$(ROLLOUT_LEASE)" release "$(GCLOUD)" "$(ROLLOUT_LEASE_TOKEN)" || true; elif [ "$${lease_acquired}" = true ]; then printf "Canonical promotion started but did not verify; preserving the shared lease and token for manual recovery.\n" >&2; fi; rmdir "$(LOCAL_LOCK_DIR)" 2>/dev/null || true; exit $$status; }; \
	trap cleanup EXIT HUP INT TERM; \
	manifest_digest="$$(shasum -a 256 "$(PACKER_BUILD_MANIFEST)" | awk '{print $$1}')"; \
	"$(ROLLOUT_LEASE)" acquire "$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$${manifest_digest}" "$(ROLLOUT_LEASE_TOKEN)"; \
	lease_acquired=true; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-backend.sh" "$(TERRAFORM_STATE_BUCKET)" "$(TERRAFORM_STATE_PREFIX)" .terraform; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-workspace.sh" "$(TF)"; \
	"$(PROVIDER_ROOT)/scripts/assert-foundation-state-bucket.sh" "$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)" "$(GCLOUD)"; \
	"$(PACKER_RESERVE_GUARD)" "$(POLICY_PATH)" ./main.pkr.hcl; \
	./scripts/assert-live-capacity.sh "$(POLICY_PATH)" "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)"; \
	if $(tf_vars) "$(TF)" plan -input=false -compact-warnings -detailed-exitcode; then :; else \
		printf "Recovery drift check was not exactly clean; smoke and promotion will not run.\n" >&2; exit 1; \
	fi; \
	./scripts/assert-packer-artifact.sh "$(PACKER_BUILD_MANIFEST)" "$(GCP_PROJECT_ID)" "$${recovery_image}" "$(PACKER_IMAGE_FAMILY)" "$(ENV)" "$${recovery_revision}" "$(PACKER_SOURCE_IMAGE)" "$(ROOT_ARTIFACT_LOCK_SHA256)"; \
	./scripts/assert-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$${recovery_image}" "$(PACKER_IMAGE_FAMILY)" "$(ENV)" "$${recovery_revision}"; \
	./scripts/smoke-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$(GCP_ZONE)" "$${recovery_image}" "$(PACKER_NETWORK_NAME)" "$(PACKER_SUBNET_NAME)" "$(PACKER_SMOKE_MACHINE_TYPE)" "$(PACKER_DOCKER_VERSION)" "$(PACKER_NOMAD_VERSION)" "$(PACKER_CONSUL_VERSION)" "$(ENV)" "$${recovery_revision}"; \
	promotion_started=true; \
	./scripts/promote-built-image.sh "$(GCLOUD)" "$(GCP_PROJECT_ID)" "$${recovery_image}" "$(PACKER_IMAGE_FAMILY)" "$(PACKER_CANONICAL_IMAGE_FAMILY)" "$(ENV)" "$${recovery_revision}"; \
	promotion_started=false; \
	"$(ROLLOUT_LEASE)" release "$(GCLOUD)" "$(ROLLOUT_LEASE_TOKEN)"; \
	lease_acquired=false; \
	rmdir "$(LOCAL_LOCK_DIR)"; \
	trap - EXIT HUP INT TERM; \
	printf "Recovered, smoked, and promoted operator canary image: %s/%s.\n" "$(GCP_PROJECT_ID)" "$${recovery_image}"

.PHONY: operator-canary-lease-inspect
operator-canary-lease-inspect: operator-canary-toolchain-guard operator-canary-config-guard
	@ "$(ROLLOUT_LEASE)" inspect "$(GCLOUD)" "$(TERRAFORM_STATE_BUCKET)" "$(GCP_PROJECT_ID)" "$(GCP_REGION)"
