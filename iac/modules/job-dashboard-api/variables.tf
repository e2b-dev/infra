variable "node_pool" {
  type = string
}

variable "update_stanza" {
  type = bool
}

variable "environment" {
  type = string
}

variable "image" {
  type = string
}

variable "count_instances" {
  type = number
}

variable "postgres_connection_string" {
  type      = string
  sensitive = true
}

variable "auth_db_connection_string" {
  type      = string
  sensitive = true
}

variable "auth_db_read_replica_connection_string" {
  type      = string
  sensitive = true
  default   = ""
}

variable "clickhouse_connection_string" {
  type      = string
  sensitive = true
}

variable "supabase_jwt_secrets" {
  type      = string
  sensitive = true
}

variable "extra_env" {
  type    = map(string)
  default = {}
}

variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
}
