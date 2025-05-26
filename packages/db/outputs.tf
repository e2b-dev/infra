output "postgres_connection_string_api" {
  value = "postgresql://${postgresql_role.api.name}${local.pooler_suffix}:${random_password.api.result}@${local.hostname}:${local.port}/postgres"
}

output "postgres_connection_string_docker_reverse_proxy" {
  value = "postgresql://${postgresql_role.docker_reverse_proxy.name}${local.pooler_suffix}:${random_password.docker_reverse_proxy.result}@${local.hostname}:${local.port}/postgres"
}
