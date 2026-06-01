# The Packer build VM runs on the same network as the rest of the cluster
# (var.network_name, by default GCP's auto-mode "default" network) — there's no need
# for a dedicated build network. The only thing the build needs that the network
# doesn't already provide is IAP SSH access, so we add a single firewall rule scoped to
# a build-only network tag. The VM carries that tag (see `tags` on the Packer source in
# nomad-cluster-disk-image/main.pkr.hcl) so this does not widen SSH for any other VM.

locals {
  packer_build_tag = "packer-build"
}

resource "google_compute_firewall" "packer_build_ssh" {
  name    = "${var.prefix}packer-build-ssh-ingress"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  priority    = 900
  direction   = "INGRESS"
  target_tags = [local.packer_build_tag]

  # IAP TCP forwarding range — https://googlecloudplatform.github.io/iap-desktop/setup-iap/
  source_ranges = ["35.235.240.0/20"]
}
