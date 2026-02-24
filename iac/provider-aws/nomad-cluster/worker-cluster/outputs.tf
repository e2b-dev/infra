output "asg_name" {
  value = aws_autoscaling_group.worker.name
}

output "asg_id" {
  value = aws_autoscaling_group.worker.id
}

output "launch_template_id" {
  value = aws_launch_template.worker.id
}
