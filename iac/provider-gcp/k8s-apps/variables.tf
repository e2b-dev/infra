variable "namespace" {
  type        = string
  description = "Namespace for all e2b k8s workloads."
  default     = "e2b"
}

variable "argocd_apps_bucket_name" {
  type        = string
  description = "GCS bucket that holds the ArgoCD Application manifests (one object per release)."
}

variable "argocd_namespace" {
  type        = string
  description = "Namespace where ArgoCD reconciles Application objects (metadata.namespace of each Application)."
  default     = "argocd"
}

variable "argocd_charts_repo_url" {
  type        = string
  description = "OCI registry holding the Helm charts (without the oci:// scheme), used as Application spec.source.repoURL."
  default     = "ghcr.io/e2b-dev/charts"
}

variable "argocd_enabled" {
  type    = bool
  default = false
}

variable "core_repository_name" {
  type = string
}

variable "prefix" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "api_server_count" {
  type = number
}

variable "api_resources_cpu_count" {
  type = number
}

variable "api_resources_memory_mb" {
  type = number
}

variable "api_node_pool" {
  type = string
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "api_internal_grpc_port" {
  type    = number
  default = 5009
}

variable "environment" {
  type = string
}

variable "api_machine_count" {
  type = number
}

variable "api_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "api_db_migrator_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "auth_provider_config" {
  type = object({
    jwt = optional(list(object({
      issuer = object({
        url                 = string
        discoveryURL        = optional(string)
        audiences           = list(string)
        audienceMatchPolicy = optional(string)
      })
      cacheDuration = optional(string)
    })))
  })
  sensitive = true
  default   = null
}

variable "google_service_account_key" {
  type = string
}

variable "api_secret" {
  type = string
}

variable "custom_envs_repository_name" {
  type = string
}

variable "postgres_connection_string_secret_name" {
  type = string
}

variable "postgres_read_replica_connection_string_secret_version" {
  type = any
}

variable "redis_cluster_url_secret_version" {
  type = any
}

variable "redis_tls_ca_base64_secret_version" {
  type = any
}
