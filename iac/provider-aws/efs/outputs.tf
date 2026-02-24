output "efs_id" {
  value = aws_efs_file_system.shared_cache.id
}

output "efs_dns_name" {
  value = aws_efs_file_system.shared_cache.dns_name
}

output "efs_access_point_id" {
  value = aws_efs_access_point.chunks_cache.id
}
