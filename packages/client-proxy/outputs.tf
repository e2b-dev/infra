output "client_proxy_docker_image_digest" {
  value = docker_image.client_proxy_image.repo_digest
}

output "edge_api_secret" {
  value = random_password.edge_api_secret.result
}