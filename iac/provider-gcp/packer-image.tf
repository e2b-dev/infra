# Builds the e2b-orch node image with Packer, driven by Terraform via the toowoxx/packer
# provider. Replaces the old out-of-band `packer build` step and the downstream
# `data "google_compute_image" { family = "e2b-orch" }` lookups: the built image name is
# read back from the Packer manifest and fed directly into every node-pool instance
# template (see module.cluster -> var.orch_image_id).
#
# The googlecompute plugin must already be in the global Packer plugin cache; `make init`
# installs it (one-time bootstrap, see ../Makefile).

locals {
  packer_dir           = "${path.module}/nomad-cluster-disk-image"
  packer_shared_setup  = "${path.module}/../nomad-cluster-disk-image/setup"
  packer_manifest_path = "${local.packer_dir}/manifest.json"
  packer_build_tag     = "packer-build"
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

# IAP SSH for the build VM. It runs on the shared cluster network (var.network_name)
# tagged with local.packer_build_tag, so this rule grants no other VM wider SSH.
resource "google_compute_firewall" "packer_build_ssh" {
  name    = "${var.prefix}packer-build-ssh-ingress"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  priority      = 900
  direction     = "INGRESS"
  target_tags   = [local.packer_build_tag]
  source_ranges = ["35.235.240.0/20"] # IAP TCP forwarding range
}

# Lock the build VM down to IAP-only. A firewall allow rule is purely additive, so on a
# network that carries a broad default-allow-ssh (e.g. GCP's auto "default" VPC, prio
# 65534) the rule above would NOT prevent internet SSH to this privileged build host.
# This deny sits between the IAP allow (900) and the default rules (65534): IAP SSH still
# wins for the tunnel range, everything else is dropped before default-allow-ssh matches.
resource "google_compute_firewall" "packer_build_deny" {
  name    = "${var.prefix}packer-build-deny-ingress"
  network = var.network_name

  deny {
    protocol = "all"
  }

  priority      = 950
  direction     = "INGRESS"
  target_tags   = [local.packer_build_tag]
  source_ranges = ["0.0.0.0/0"]
}

locals {
  packer_variables = {
    gcp_project_id = var.gcp_project_id
    gcp_zone       = var.gcp_zone
    prefix         = var.prefix
    network_name   = var.network_name
    network_tag    = local.packer_build_tag
    consul_version = var.packer_consul_version
    nomad_version  = var.packer_nomad_version
    source_image   = var.packer_source_image
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

  depends_on = [
    google_compute_firewall.packer_build_ssh,
    google_compute_firewall.packer_build_deny,
  ]
}

locals {
  # The manifest post-processor appends one entry per build, so select the most recent
  # run. For the googlecompute builder, artifact_id is the GCE image name.
  orch_image_name = one([
    for b in packer_image.orch.manifest.builds : b.artifact_id
    if b.packer_run_uuid == packer_image.orch.manifest.last_run_uuid
  ])
  orch_image_self_link = "projects/${var.gcp_project_id}/global/images/${local.orch_image_name}"
}
