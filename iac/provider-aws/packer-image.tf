# Builds the e2b-orch node AMI with Packer, driven by Terraform via the toowoxx/packer
# provider. Replaces the old out-of-band `packer build` step and the downstream
# most-recent `data "aws_ami"` lookups: the built AMI id is read back from the Packer
# manifest and fed into every node-pool launch template (module.cluster -> var.image_id).
#
# The amazon plugin must already be in the global Packer plugin cache; `make init`
# installs it (one-time bootstrap, see ./Makefile). The amazon-ebs builder provisions a
# temporary SSH security group on the build VM itself, so no firewall resource is needed.

locals {
  packer_dir           = "${path.module}/nomad-cluster-disk-image"
  packer_shared_setup  = "${path.module}/../nomad-cluster-disk-image/setup"
  packer_manifest_path = "${local.packer_dir}/manifest.json"
}

# Hash of the Packer template + every setup script/asset it consumes, so the build only
# re-runs when its inputs actually change.
data "packer_files" "orch" {
  directory = local.packer_dir
  file_dependencies = concat(
    # Exclude manifest.json: the build rewrites it on every run, so including it here
    # would change files_hash after each build and trigger a perpetual rebuild loop.
    [for f in fileset(local.packer_dir, "**/*") : "${local.packer_dir}/${f}" if f != "manifest.json"],
    [for f in fileset(local.packer_shared_setup, "**/*") : "${local.packer_shared_setup}/${f}"],
  )
}

locals {
  packer_variables = {
    prefix                 = var.prefix
    aws_region             = data.aws_region.current.id
    aws_profile            = var.aws_profile
    vpc_id                 = module.init.vpc_id
    subnet_id              = module.init.vpc_public_subnet_ids[0]
    consul_version         = var.packer_consul_version
    nomad_version          = var.packer_nomad_version
    source_ami_filter_name = var.packer_source_ami_filter_name
  }
}

resource "packer_image" "orch" {
  directory     = local.packer_dir
  manifest_path = local.packer_manifest_path

  variables = local.packer_variables

  # Rebuild when the template/scripts change (files) OR when any build variable changes
  # (variables). The toowoxx/packer provider only re-runs `packer build` when a trigger value
  # changes; variable changes alone do not, so a version/base-image bump would otherwise leave
  # the manifest (and every node pool that reads it) pinned to the previously built image.
  triggers = merge(
    { files = data.packer_files.orch.files_hash },
    local.packer_variables,
  )
}

locals {
  # The manifest post-processor appends one entry per build, so select the most recent
  # run. For the amazon-ebs builder, artifact_id is "region:ami-id" (comma-joined when
  # the AMI is copied to multiple regions); pull the ami-id out of the first entry.
  orch_ami_artifact = one([
    for b in packer_image.orch.manifest.builds : b.artifact_id
    if b.packer_run_uuid == packer_image.orch.manifest.last_run_uuid
  ])
  orch_ami_id = trimspace(element(split(":", element(split(",", local.orch_ami_artifact), 0)), 1))
}
