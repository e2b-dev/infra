# This directory holds the Packer template for the e2b-orch node image.
#
# The build is driven by Terraform via the toowoxx/packer provider (see
# ../packer-image.tf and ../packer-build-firewall.tf) and runs as part of the main
# `make plan && make apply`. The targets below are only for local Packer development.
#
# Migration note: this directory used to be its own Terraform root (state prefix
# "terraform/cluster-disk-image/state") that created a dedicated build network. That
# root is gone — after switching, run `terraform destroy` against that old state to
# clean up the orphaned network/subnet/firewall. Nothing to import.

init:
	packer init -upgrade .

format:
	packer fmt .
