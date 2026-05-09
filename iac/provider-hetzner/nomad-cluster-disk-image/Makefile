.PHONY: init validate build build-base build-api build-clickhouse build-client build-control-server clean

# Hetzner Snapshot Builder Workflow
#
# Usage:
#   export HCLOUD_TOKEN=<your-hetzner-token>  (or PKR_VAR_hetzner_api_token)
#   make init        # download Packer plugins
#   make validate    # validate config
#   make build       # build ALL snapshots (base + 4 role-snapshots)
#   make build-api   # build only API snapshot

PKR_VAR_hetzner_api_token ?= $(HCLOUD_TOKEN)
export PKR_VAR_hetzner_api_token

init:
	packer init main.pkr.hcl

validate:
	packer validate main.pkr.hcl

build:
	packer build main.pkr.hcl

build-base:
	packer build -only=hcloud.base main.pkr.hcl

build-api:
	packer build -only=hcloud.api main.pkr.hcl

build-clickhouse:
	packer build -only=hcloud.clickhouse main.pkr.hcl

build-client:
	packer build -only=hcloud.client main.pkr.hcl

build-control-server:
	packer build -only=hcloud.control_server main.pkr.hcl

clean:
	rm -rf .packer/ packer_cache/ *.pkrvars.hcl
