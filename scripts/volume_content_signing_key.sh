#!/usr/bin/env bash

# Generate a new ed25519 signing key that can be exported as TF_VAR_volume_token_signature
# to bypass the terraform in iac/provider-gcp/volume_content_signing_key.tf that otherwise
# generates one (tls_private_key.volume_token + time_static.volume_token_generation).
#
# The matching variable is defined in iac/provider-gcp/variables.tf:
#   variable "volume_token_signature" {
#     type = object({ key = string, name = string, method = string })
#   }
#
# The key value is consumed verbatim as VOLUME_TOKEN_SIGNING_KEY (iac/provider-gcp/main.tf),
# which the API parses as "<METHOD>:<base64(PEM)>" (packages/api/internal/cfg/model.go).
# For ed25519 the PEM must be PKCS#8 ("-----BEGIN PRIVATE KEY-----") so jwt.ParseEdPrivateKeyFromPEM
# can read it -- ssh-keygen emits OpenSSH format and won't work, so we use openssl.
#
# Output looks like:
#   TF_VAR_volume_token_signature='{method : "EdDSA", name : "1717430400", key : "ED25519:<base64>"}'

set -euo pipefail

private_pem="$(openssl genpkey -algorithm ed25519)"
public_pem="$(printf '%s\n' "$private_pem" | openssl pkey -pubout)"

private_b64="$(printf '%s\n' "$private_pem" | base64 -w0)"
public_b64="$(printf '%s\n' "$public_pem" | base64 -w0)"

unix_now="$(date +%s)"

echo "Export this to bypass the terraform-generated signing key:"
echo
echo "export TF_VAR_volume_token_signature='{method : \"EdDSA\", name : \"${unix_now}\", key : \"ED25519:${private_b64}\"}'"
echo
echo "Add the matching verification (public) key to volume_content_jwt_config in belt"
echo "(iac/provider-gcp/volume-content.tf). The map key is the signing-key name and the"
echo "value is the method-prefixed public key:"
echo
echo "volume_content_jwt_config.verification_keys = {\"${unix_now}\": \"ED25519:${public_b64}\"}"
