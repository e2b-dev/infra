data "aws_ecr_image" "api" {
  repository_name = var.api_repository_name
  image_tag       = "latest"
}

data "aws_ecr_image" "db_migrator" {
  repository_name = var.db_migrator_repository_name
  image_tag       = "latest"
}

data "aws_ecr_image" "client_proxy" {
  repository_name = var.client_proxy_repository_name
  image_tag       = "latest"
}

data "aws_ecr_image" "clickhouse_migrator" {
  repository_name = var.clickhouse_migrator_repository_name
  image_tag       = "latest"
}
