output "instance_name" {
  description = "Cloud SQL instance short name for the e2b control-plane DB."
  value       = google_sql_database_instance.e2b.name
}

output "private_ip" {
  description = "Private IP of the e2b control-plane DB on harness-vpc."
  value       = google_sql_database_instance.e2b.private_ip_address
}
