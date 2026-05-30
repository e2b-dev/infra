ENV := $(shell cat ../../../.last_used_env || echo "not-set")
-include ../../../.env.${ENV}

init:
	packer init -upgrade .

build:
	packer build \
		$(if $(CONSUL_VERSION),-var "consul_version=$(CONSUL_VERSION)") \
		$(if $(NOMAD_VERSION),-var "nomad_version=$(NOMAD_VERSION)") \
		$(if $(PACKER_NETWORK_NAME),-var "network_name=$(PACKER_NETWORK_NAME)") \
		$(if $(PACKER_SUBNET_NAME),-var "subnet_name=$(PACKER_SUBNET_NAME)") \
		-var "prefix=$(PREFIX)" \
		-var "aws_region=$(AWS_REGION)" \
		-var "aws_profile=$(AWS_PROFILE)" \
		.

format:
	packer fmt .
