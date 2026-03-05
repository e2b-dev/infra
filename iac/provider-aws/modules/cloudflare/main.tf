resource "aws_secretsmanager_secret" "cloudflare" {
  name = "${var.prefix}cloudflare"
}

resource "aws_secretsmanager_secret_version" "cloudflare_initial" {
  secret_id = aws_secretsmanager_secret.cloudflare.id
  secret_string = jsonencode({
    TOKEN = "",
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "cloudflare" {
  secret_id     = aws_secretsmanager_secret.cloudflare.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.cloudflare_initial]
}
