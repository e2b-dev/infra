#!/bin/bash

set -e

echo "Starting Vault in dev mode..."
docker run -d --name vault \
  --cap-add=IPC_LOCK \
  -e 'VAULT_DEV_ROOT_TOKEN_ID=myroot' \
  -e 'VAULT_DEV_LISTEN_ADDRESS=0.0.0.0:8200' \
  -p 8200:8200 \
  hashicorp/vault:1.20.3

echo "Waiting for Vault to be ready..."
for i in {1..30}; do
  if curl -s http://localhost:8200/v1/sys/health | grep -q "initialized"; then
    echo "Vault is ready!"
    break
  fi
  echo "Waiting for Vault... ($i/30)"
  sleep 1
done

# Configure Vault
export VAULT_ADDR='http://localhost:8200'
export VAULT_TOKEN='myroot'

echo "Enabling AppRole auth..."
docker exec -e VAULT_ADDR=$VAULT_ADDR -e VAULT_TOKEN=$VAULT_TOKEN vault \
  vault auth enable approle || true

echo "Creating Vault policy..."
cat > /tmp/vault-policy.hcl <<'POLICY'
path "secret/data/*" {
  capabilities = ["create", "read", "update", "delete"]
}
path "secret/metadata/*" {
  capabilities = ["create", "read", "update", "delete"]
}
POLICY

docker cp /tmp/vault-policy.hcl vault:/tmp/vault-policy.hcl
docker exec -e VAULT_ADDR=$VAULT_ADDR -e VAULT_TOKEN=$VAULT_TOKEN vault \
  vault policy write test-policy /tmp/vault-policy.hcl

echo "Creating AppRole..."
docker exec -e VAULT_ADDR=$VAULT_ADDR -e VAULT_TOKEN=$VAULT_TOKEN vault \
  vault write auth/approle/role/test-role \
  token_policies="test-policy" \
  token_ttl=1h \
  token_max_ttl=4h

echo "Getting role-id and secret-id..."
ROLE_ID=$(docker exec -e VAULT_ADDR=$VAULT_ADDR -e VAULT_TOKEN=$VAULT_TOKEN vault \
  vault read -field=role_id auth/approle/role/test-role/role-id)
SECRET_ID=$(docker exec -e VAULT_ADDR=$VAULT_ADDR -e VAULT_TOKEN=$VAULT_TOKEN vault \
  vault write -field=secret_id -f auth/approle/role/test-role/secret-id)

echo "Exporting Vault configuration..."
if [ -n "$GITHUB_ENV" ]; then
  echo "VAULT_ADDR=http://localhost:8200" >> $GITHUB_ENV
  echo "VAULT_APPROLE_ROLE_ID=${ROLE_ID}" >> $GITHUB_ENV
  echo "VAULT_APPROLE_SECRET_ID=${SECRET_ID}" >> $GITHUB_ENV
else
  echo "VAULT_ADDR=http://localhost:8200"
  echo "VAULT_APPROLE_ROLE_ID=${ROLE_ID}"
  echo "VAULT_APPROLE_SECRET_ID=${SECRET_ID}"
fi

echo "Vault setup complete!"

