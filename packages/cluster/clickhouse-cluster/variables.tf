# ---------------------------------------------------------------------------------------------------------------------
# REQUIRED PARAMETERS
# You must provide a value for each of these parameters.
# ---------------------------------------------------------------------------------------------------------------------

variable "environment" {
  description = "The environment (e.g. staging, prod, dev)."
  type        = string
}

variable "job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
}

variable "node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "machine_type" {
  description = "The machine type of the Compute Instance to run for each node in the cluster (e.g. n1-standard-1)."
  type        = string
}

variable "cluster_size" {
  description = "The number of nodes to have in the Nomad cluster. We strongly recommended that you use either 3 or 5."
  type        = number
}

variable "gcp_zone" {
  description = "The GCP zone in which the server cluster will be created (e.g. us-central1-a)."
  type        = string
}

variable "cluster_tag_name" {
  description = "The tag name for the cluster."
  type        = string
}

variable "image_family" {
  description = "The source image family used to create the boot disk for a Vault node. Only images based on Ubuntu 16.04 or 18.04 LTS are supported at this time."
  type        = string
}

variable "startup_script" {
  description = "A Startup Script to execute when the server first boots. We recommend passing in a bash script that executes the run-vault script, which should have been installed in the Vault Google Image by the install-vault module."
  type        = string
}

variable "service_account_email" {
  description = "The email of the service account for the instance template."
  type        = string
}

variable "nomad_port" {
  description = "The port on which Nomad will listen for incoming connections."
  type        = number
}

variable "clickhouse_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

# ---------------------------------------------------------------------------------------------------------------------
# OPTIONAL PARAMETERS
# You can provide a value for each of these parameters.
# ---------------------------------------------------------------------------------------------------------------------


variable "instance_group_target_pools" {
  description = "To use a Load Balancer with the Consul cluster, you must populate this value. Specifically, this is the list of Target Pool URLs to which new Compute Instances in the Instance Group created by this module will be added. Note that updating the Target Pools attribute does not affect existing Compute Instances."
  type        = list(string)
  default     = []
}

variable "cluster_description" {
  description = "A description of the Vault cluster; it will be added to the Compute Instance Template."
  type        = string
  default     = null
}

variable "network_name" {
  description = "The name of the VPC Network where all resources should be created."
  type        = string
}

variable "custom_tags" {
  description = "A list of tags that will be added to the Compute Instance Template in addition to the tags automatically added by this module."
  type        = list(string)
  default     = []
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}

# Metadata
variable "custom_metadata" {
  description = "A map of metadata key value pairs to assign to the Compute Instance metadata."
  type        = map(string)
  default     = {}
}

variable "cluster_name" {
  description = "The name of the Nomad cluster (e.g. nomad-stage). This variable is used to namespace all resources created by this module."
  type        = string
  default     = "clickhouse"
}

# Disk Settings

variable "root_volume_disk_size_gb" {
  description = "The size, in GB, of the root disk volume on each Consul node."
  type        = number
  default     = 200
}

variable "root_volume_disk_type" {
  description = "The GCE disk type. Can be either pd-ssd, local-ssd, or pd-standard"
  type        = string
  default     = "pd-ssd"
}
