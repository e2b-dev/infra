terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "6.13.0"
    }
  }
}
resource "google_filestore_instance" "" {
  name = ""
  tier = ""
}