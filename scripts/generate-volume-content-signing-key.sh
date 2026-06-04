#!/usr/bin/env bash

# Generate a new ed25519 signing key and emit the env vars the services consume.
#
# The API signs volume-content tokens (packages/api/internal/cfg/model.go,
# VolumesTokenConfig):
#   VOLUME_TOKEN_SIGNING_METHOD   - jwt signing method, e.g. "EdDSA"
#   VOLUME_TOKEN_SIGNING_KEY      - "<TYPE>:<base64(PEM)>", parsed by jwt.ParseEdPrivateKeyFromPEM
#   VOLUME_TOKEN_SIGNING_KEY_NAME - the key id (kid), here a unix timestamp
# For ed25519 the PEM must be PKCS#8 ("-----BEGIN PRIVATE KEY-----") so the jwt
# parser can read it -- ssh-keygen emits OpenSSH format and won't work, so we use openssl.
#
# The belt volume-content service verifies those tokens. Its authn config is
# prefixed with JWT_ (belt packages/volume-content/internal/{cfg,authn}/config.go):
#   JWT_VERIFICATION_KEYS - "<name>:<TYPE>:<base64(public PEM)>" map; the value is
#                           split on the first ":" so the key name maps to the
#                           method-prefixed public key.

set -euo pipefail

private_pem="$(openssl genpkey -algorithm ed25519)"
public_pem="$(printf '%s\n' "$private_pem" | openssl pkey -pubout)"

private_b64="$(printf '%s\n' "$private_pem" | openssl base64 -A)"
public_b64="$(printf '%s\n' "$public_pem" | openssl base64 -A)"

unix_now="$(date +%s)"

echo "Step 1: set this on the belt volume-content service so it verifies the new key:"
echo
echo "JWT_VERIFICATION_KEYS=${unix_now}:ED25519:${public_b64}"
echo
echo "Step 2: set these on the API service so it signs with the new key:"
echo
echo "VOLUME_TOKEN_SIGNING_METHOD=EdDSA"
echo "VOLUME_TOKEN_SIGNING_KEY_NAME=${unix_now}"
echo "VOLUME_TOKEN_SIGNING_KEY=ED25519:${private_b64}"
