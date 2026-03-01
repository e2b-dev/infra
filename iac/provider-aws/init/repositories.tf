resource "aws_ecr_repository" "client_proxy" {
  name                 = "${var.prefix}core/client-proxy"
  image_tag_mutability = "MUTABLE"
  force_delete         = var.allow_force_destroy
}

resource "aws_ecr_repository" "clickhouse_migrator" {
  name                 = "${var.prefix}core/clickhouse-migrator"
  image_tag_mutability = "MUTABLE"
  force_delete         = var.allow_force_destroy
}

resource "aws_ecr_repository" "custom_environments" {
  name                 = "${var.prefix}core/custom-environments"
  image_tag_mutability = "MUTABLE"
  force_delete         = var.allow_force_destroy
}
