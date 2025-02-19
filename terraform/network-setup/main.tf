variable "gcp_project_id" {
  default = "e2b-"
}

resource "google_compute_firewall" "allow_dns" {
  project = var.gcp_project_id
  name    = "allow-dns-ports"
  network = "default" # Change to your VPC name if not using the default one

  allow {
    protocol = "udp"
    ports    = ["5353"]
  }

  direction = "INGRESS"
  priority  = 1000

  source_ranges = ["0.0.0.0/0"] # Change this to restrict access

  description = "Allow UDP traffic on ports 5353 (mDNS)"
}

resource "google_compute_firewall" "allow_50001" {
  project = var.gcp_project_id
  name    = "allow-50001-ingress"
  network = "default" # Change this if you're using a custom network

  allow {
    protocol = "tcp"
    ports    = ["50001"]
  }

  direction = "INGRESS"
  priority  = 1000

  source_ranges = ["0.0.0.0/0"] # Change this to restrict allowed sources
}

resource "google_compute_firewall" "allow_3002" {
  project = var.gcp_project_id
  name    = "allow-3002-ingress"
  network = "default" # Change this if you're using a custom network

  allow {
    protocol = "tcp"
    ports    = ["3002"]
  }

  direction = "INGRESS"
  priority  = 1000

  source_ranges = ["0.0.0.0/0"] # Change this to restrict allowed sources
}
