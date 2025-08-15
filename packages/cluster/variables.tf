variable "prefix" {
  type = string
}

variable "environment" {
  description = "The environment (e.g. staging, prod)."
  type        = string
}

variable "cloudflare_api_token_secret_name" {
  type = string
}

variable "cluster_tag_name" {
  description = "The tag name the Compute Instances will look for to automatically discover each other and form a cluster. TIP: If running more than one server Server cluster, each cluster should have its own unique tag name."
  type        = string
  default     = "orch"
}

variable "server_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "server_cluster_name" {
  type    = string
  default = "orch-server"
}

variable "server_cluster_size" {
  type = number
}

variable "server_machine_type" {
  type = string
}

variable "api_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "api_cluster_size" {
  type = number
}

variable "api_machine_type" {
  type = string
}

variable "build_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "build_cluster_size" {
  type = number
}

variable "build_machine_type" {
  type = string
}

variable "build_cluster_root_disk_size_gb" {
  type = number
}

variable "build_cluster_cache_disk_size_gb" {
  type = number
}

variable "edge_api_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "edge_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "client_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "client_cluster_name" {
  type    = string
  default = "orch-client"
}

variable "client_cluster_size" {
  type = number
}

variable "client_cluster_size_max" {
  type = number
}

variable "client_machine_type" {
  type = string
}

variable "client_cluster_cache_disk_size_gb" {
  type = number
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "network_name" {
  type    = string
  default = "default"
}

variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "logs_health_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}


variable "google_service_account_email" {
  type = string
}

variable "google_service_account_key" {
  type = string
}

variable "docker_contexts_bucket_name" {
  type = string
}

variable "domain_name" {
  type        = string
  description = "The domain name where e2b will run"
}

variable "additional_domains" {
  type        = list(string)
  description = "Additional domains which can be used to access the e2b cluster"
}

variable "cluster_setup_bucket_name" {
  type        = string
  description = "The name of the bucket to store the setup files"
}

variable "fc_env_pipeline_bucket_name" {
  type        = string
  description = "The name of the bucket to store the files for firecracker environment pipeline"
}

variable "fc_kernels_bucket_name" {
  type        = string
  description = "The name of the bucket to store the kernels for firecracker"
}

variable "fc_versions_bucket_name" {
  type = string
}

variable "consul_acl_token_secret" {
  type = string
}

variable "nomad_acl_token_secret" {
  type = string
}

variable "nomad_port" {
  type = number
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}

variable "clickhouse_cluster_name" {
  type    = string
  default = "clickhouse"
}

variable "clickhouse_cluster_size" {
  description = "The number of ClickHouse nodes in the cluster."
  type        = number
}

variable "clickhouse_machine_type" {
  description = "The machine type of the Compute Instance to run for each node in the cluster."
  type        = string
}


variable "clickhouse_job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
}

variable "clickhouse_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "clickhouse_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "additional_lb_matchers" {
  description = "Additional path rules to add to the load balancer routing."
  type = list(object({
    matcher_host_prefix       = string
    matcher_path_matcher_name = string

    backend_service_link     = string
    api_node_group_port_name = string
    api_node_group_port      = number
  }))
}
