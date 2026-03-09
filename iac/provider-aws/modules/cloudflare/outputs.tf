locals {
  cloudflare_raw = jsondecode(data.aws_secretsmanager_secret_version.cloudflare.secret_string)
}

output "cloudflare" {
  value = {
    token = local.cloudflare_raw["TOKEN"]
  }
}
