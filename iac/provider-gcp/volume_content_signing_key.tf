locals {
  volume_token_issuer = coalesce(var.volume_token_issuer, var.domain_name)

  should_generate_volume_token_signing_key = var.volume_token_signature == null

  volume_token_signing_key = (
    local.should_generate_volume_token_signing_key
    ? format("ED25519:%s", base64encode(tls_private_key.volume_token[0].private_key_pem))
    : var.volume_token_signature.key
  )

  volume_token_signature_verification_key = (
    local.should_generate_volume_token_signing_key
    ? format("ED25519:%s", base64encode(tls_private_key.volume_token[0].public_key_pem))
  : "")

  volume_token_signature_name = (
    local.should_generate_volume_token_signing_key
    ? time_static.volume_token_generation.unix
    : var.volume_token_signature.name
  )
  volume_token_signature_method = (
    local.should_generate_volume_token_signing_key
    ? "EdDSA"
    : var.volume_token_signature.method
  )
}

resource "time_static" "volume_token_generation" {}

resource "tls_private_key" "volume_token" {
  count     = local.should_generate_volume_token_signing_key ? 1 : 0
  algorithm = "ED25519"
}

output "volume_token_signature_verification_key_name" {
  value = local.volume_token_signature_name
}

output "volume_token_signature_verification_key" {
  value = local.volume_token_signature_verification_key
}
