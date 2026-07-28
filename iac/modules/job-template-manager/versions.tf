terraform {
  required_version = ">= 1.0"

  required_providers {
    external = {
      source  = "hashicorp/external"
      version = "2.4.0"
    }

    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }
  }
}
