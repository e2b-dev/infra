output "shared_chunk_cache_path" {
  value = var.efs_cache_enabled ? "${local.nfs_mount_path}/${local.nfs_mount_subdir}" : ""
}

output "api_asg_id" {
  value = aws_autoscaling_group.api.id
}

output "api_asg_name" {
  value = aws_autoscaling_group.api.name
}

output "server_asg_id" {
  value = aws_autoscaling_group.server.id
}

output "server_asg_name" {
  value = aws_autoscaling_group.server.name
}
