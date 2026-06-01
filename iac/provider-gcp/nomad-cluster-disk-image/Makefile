ENV := $(shell cat ../../../.last_used_env || echo "not-set")
-include ../../../.env.${ENV}

PACKER_NETWORK_NAME ?= e2b-build-cluster-disk-image
PACKER_SUBNET_NAME ?= e2b-build-cluster-disk-image-subnetwork

tf_vars := TF_VAR_gcp_project_id=$(GCP_PROJECT_ID) \
	TF_VAR_gcp_region=$(GCP_REGION) \
	TF_VAR_network_name=$(PACKER_NETWORK_NAME) \
	TF_VAR_subnet_name=$(PACKER_SUBNET_NAME)

init:
	packer init -upgrade .

build:
	terraform init -input=false -reconfigure -backend-config="bucket=${TERRAFORM_STATE_BUCKET}"
	$(tf_vars) terraform apply -auto-approve -input=false -compact-warnings
	packer build \
		$(if $(PACKER_CONSUL_VERSION),-var "consul_version=$(PACKER_CONSUL_VERSION)") \
		$(if $(PACKER_NOMAD_VERSION),-var "nomad_version=$(PACKER_NOMAD_VERSION)") \
		$(if $(PACKER_NETWORK_NAME),-var "network_name=$(PACKER_NETWORK_NAME)") \
		$(if $(PACKER_SUBNET_NAME),-var "subnet_name=$(PACKER_SUBNET_NAME)") \
		$(if $(PACKER_SOURCE_IMAGE),-var "source_image=$(PACKER_SOURCE_IMAGE)") \
		-var "gcp_project_id=$(GCP_PROJECT_ID)" \
		-var "gcp_zone=$(GCP_ZONE)" \
		.

format:
	packer fmt .
