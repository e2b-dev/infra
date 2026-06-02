ENV := $(shell cat ../../../.last_used_env || echo "not-set")
-include ../../../.env.${ENV}

# The e2b-orch AMI is built by the packer_image resource (../packer-image.tf) during the
# regular `make plan && make apply` (Terraform drives Packer via the toowoxx/packer
# provider). `init` only installs the amazon plugin into the global Packer plugin cache.

init:
	packer init -upgrade .

format:
	packer fmt .
