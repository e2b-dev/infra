
variable "nfs_version" {
  type     = string
  default  = ""
  nullable = false

  validation {
    condition     = contains(["", "3", "4.1"], var.nfs_version)
    error_message = "Must be either 3 or 4.1"
  }
}

variable "prefix" {
  type = string
}

variable "key" {
  type = string
}

variable "tier" {
  type = string
}

variable "location" {
  type = string
}

variable "allow_deletion" {
  type = bool
}

variable "capacity_gb" {
  type = number
}

variable "network_name" {
  type = string
}
