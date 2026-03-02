variable "name" {
  description = "The name of the Nomad cluster (e.g. nomad-stage). This variable is used to namespace all resources created by this module."
  type        = string
}

variable "network_name" {
  description = "The name of the VPC Network where all resources should be created."
  type        = string
}

variable "tier" {
  description = "The tier of the Filestore cache"
  type        = string
}

variable "capacity_gb" {
  description = "The capacity of the Filestore cache in GB"
  type        = number
}

variable "nfs_version" {
  description = "NFS version"
  type        = string

  validation {
    condition     = contains(["", "3", "4.1"], var.nfs_version)
    error_message = "nfs_version must be either 3 or 4.1"
  }
}
