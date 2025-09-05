ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

NETWORK_NAME := "e2b-build-cluster-disk-image"

tf_vars := TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_network_name=$(NETWORK_NAME)

init:
	packer init -upgrade .

build:
	terraform init -input=false -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
	$(tf_vars) terraform apply -auto-approve -input=false -compact-warnings
	packer build -var "gcp_project_id=$(GCP_PROJECT_ID)" -var "gcp_zone=$(GCP_ZONE)" -var "network_name=$(NETWORK_NAME)" .

format:
	packer fmt .
