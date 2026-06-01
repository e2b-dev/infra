# Builds the e2b-orch node image with Packer, driven entirely by Terraform via the
# toowoxx/packer provider. Replaces the old out-of-band `packer build` step and the
# downstream `data "google_compute_image" { family = "e2b-orch" }` lookups: the built
# image name is read back from the Packer manifest and fed directly into every
# node-pool instance template (see module.cluster -> var.orch_image_id).

locals {
  packer_dir           = "${path.module}/nomad-cluster-disk-image"
  packer_shared_setup  = "${path.module}/../nomad-cluster-disk-image/setup"
  packer_manifest_path = "${local.packer_dir}/manifest.json"
}

# Resolve the operator's Packer binary on PATH (mise/asdf/system install). Avoids
# hardcoding a machine-specific absolute path while still using a single, known Packer
# for both `init` and `build`.
data "external" "packer_binary" {
  program = ["bash", "-c", "printf '{\"path\":\"%s\"}' \"$(command -v packer)\""]
}

# Hash of the Packer template + every setup script/asset it consumes. Drives the
# packer_image `triggers` so the build only re-runs when its inputs actually change.
data "packer_files" "orch" {
  directory = local.packer_dir
  file      = "${local.packer_dir}/main.pkr.hcl"

  file_dependencies = concat(
    [for f in fileset(local.packer_dir, "**/*") : "${local.packer_dir}/${f}"],
    [for f in fileset(local.packer_shared_setup, "**/*") : "${local.packer_shared_setup}/${f}"],
  )
}

# Install the googlecompute plugin required by main.pkr.hcl before building.
resource "null_resource" "packer_init" {
  triggers = {
    binary = data.external.packer_binary.result.path
    files  = data.packer_files.orch.files_hash
  }

  provisioner "local-exec" {
    command = "${data.external.packer_binary.result.path} init ${local.packer_dir}"
  }
}

resource "packer_image" "orch" {
  directory = local.packer_dir
  # file          = "main.pkr.hcl"
  manifest_path = local.packer_manifest_path
  force         = true

  variables = {
    gcp_project_id = var.gcp_project_id
    gcp_zone       = var.gcp_zone
    prefix         = var.prefix
    network_name   = var.network_name
    network_tag    = local.packer_build_tag
  }

  triggers = {
    files = data.packer_files.orch.files_hash
  }

  depends_on = [
    null_resource.packer_init,
    google_compute_firewall.packer_build_ssh,
  ]
}

locals {
  # The manifest post-processor APPENDS one entry per build, so pick the build from the
  # most recent run (last_run_uuid) rather than builds[0]. For the googlecompute builder
  # the artifact_id is the GCE image name (e.g. "e2b-orch-2026-06-01-12-00-00").
  orch_image_name = one([
    for b in packer_image.orch.manifest.builds : b.artifact_id
    if b.packer_run_uuid == packer_image.orch.manifest.last_run_uuid
  ])
  orch_image_self_link = "projects/${var.gcp_project_id}/global/images/${local.orch_image_name}"
}
