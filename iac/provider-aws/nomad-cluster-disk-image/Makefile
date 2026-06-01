ENV := $(shell cat ../../../.last_used_env || echo "not-set")
-include ../../../.env.${ENV}

init:
	packer init -upgrade .

build:
	packer build \
		$(if $(PACKER_CONSUL_VERSION),-var "consul_version=$(PACKER_CONSUL_VERSION)") \
		$(if $(PACKER_NOMAD_VERSION),-var "nomad_version=$(PACKER_NOMAD_VERSION)") \
		$(if $(PACKER_VPC_ID),-var "vpc_id=$(PACKER_VPC_ID)") \
		$(if $(PACKER_SUBNET_ID),-var "subnet_id=$(PACKER_SUBNET_ID)") \
		$(if $(PACKER_SOURCE_AMI_FILTER_NAME),-var "source_ami_filter_name=$(PACKER_SOURCE_AMI_FILTER_NAME)") \
		-var "prefix=$(PREFIX)" \
		-var "aws_region=$(AWS_REGION)" \
		-var "aws_profile=$(AWS_PROFILE)" \
		.

format:
	packer fmt .
