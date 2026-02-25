output "clickhouse_password" {
  value     = random_password.clickhouse_password.result
  sensitive = true
}
