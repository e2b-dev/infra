output "stack_url" {
  value = grafana_cloud_stack.e2b_stack.url
}

output "service_account_token" {
  value = grafana_cloud_stack_service_account_token.manage_datasource.key
}
