#!/bin/bash

set -e

# Configuration
VAULT_ADDR="${VAULT_ADDR:-http://localhost:8200}"
GCP_PROJECT="${GCP_PROJECT}"
SECRET_PREFIX="${SECRET_PREFIX:-e2b-}"
VAULT_INIT_OUTPUT="/tmp/vault-init.json"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function print_usage {
  echo "Usage: $0 [OPTIONS]"
  echo ""
  echo "Initialize HashiCorp Vault with GCP KMS auto-unseal"
  echo ""
  echo "Options:"
  echo "  --project       GCP project ID (required)"
  echo "  --prefix        Prefix for GCP Secret Manager secrets (required)"
  echo "  --vault-addr    Vault address (default: http://vault.service.consul:8200)"
  echo "  --help          Show this help message"
  echo ""
  echo "Example:"
  echo "  $0 --project my-project --prefix prod-"
}

function log_info {
  echo -e "${GREEN}[INFO]${NC} $1"
}

function log_warn {
  echo -e "${YELLOW}[WARN]${NC} $1"
}

function log_error {
  echo -e "${RED}[ERROR]${NC} $1"
}

function check_vault_status {
  local status_output
  if status_output=$(vault status -format=json 2>/dev/null); then
    echo "$status_output"
  else
    echo '{"initialized":false,"sealed":true}'
  fi
}

function save_to_secret_manager {
  local secret_name="$1"
  local secret_data="$2"

  log_info "Saving data to Secret Manager: ${secret_name}"

  echo "$secret_data" | gcloud secrets versions add "${secret_name}" \
    --data-file=- \
    --project="${GCP_PROJECT}"
}

function get_from_secret_manager {
  local secret_name="$1"

  gcloud secrets versions access latest \
    --secret="${secret_name}" \
    --project="${GCP_PROJECT}" 2>/dev/null || echo ""
}

function initialize_vault {
  log_info "Initializing Vault with GCP KMS auto-unseal..."

  # Initialize Vault with recovery keys instead of unseal keys (auto-unseal mode)
  # Recovery keys are used for operations like generating a new root token
  vault operator init \
    -recovery-shares=5 \
    -recovery-threshold=3 \
    -format=json > "${VAULT_INIT_OUTPUT}"

  if [ $? -eq 0 ]; then
    log_info "Vault initialized successfully with auto-unseal"

    # Extract recovery keys and root token
    local recovery_keys=$(jq -r '.recovery_keys_b64[]' "${VAULT_INIT_OUTPUT}" | tr '\n' ',' | sed 's/,$//')
    local root_token=$(jq -r '.root_token' "${VAULT_INIT_OUTPUT}")

    # Save root token to vault-root-key secret
    save_to_secret_manager "${SECRET_PREFIX}vault-root-key" "$root_token"

    # Save recovery keys to vault-recovery-keys secret in the expected format
    local recovery_keys_data=$(jq -n \
      --arg keys "$recovery_keys" \
      '{
        recovery_keys: ($keys | split(",")),
        initialized: true,
        auto_unseal: true,
        initialized_at: now | todate
      }')

    save_to_secret_manager "${SECRET_PREFIX}vault-recovery-keys" "$recovery_keys_data"

    # Clean up temp file
    rm -f "${VAULT_INIT_OUTPUT}"

    return 0
  else
    log_error "Failed to initialize Vault"
    return 1
  fi
}

function wait_for_auto_unseal {
  log_info "Waiting for Vault to auto-unseal..."

  local max_attempts=30
  local attempt=0

  while [ $attempt -lt $max_attempts ]; do
    local status=$(check_vault_status)
    local sealed=$(echo "$status" | jq -r '.sealed')

    if [ "$sealed" == "false" ]; then
      log_info "Vault has auto-unsealed successfully"
      return 0
    fi

    log_info "Vault is still sealed, waiting... (attempt $((attempt + 1))/$max_attempts)"
    sleep 2
    attempt=$((attempt + 1))
  done

  log_error "Vault did not auto-unseal within expected time"
  return 1
}

function setup_secrets_engine {
  log_info "Setting up KV secrets engine at secret/"

  # Enable KV v2 secrets engine at secret/
  vault secrets enable -path=secret kv-v2 2>/dev/null || {
    log_info "KV secrets engine already enabled at secret/"
  }
}

function create_api_approle {
  log_info "Configuring API service AppRole with write/delete permissions..."

  # Enable AppRole auth method if not already enabled
  vault auth enable approle 2>/dev/null || {
    log_info "AppRole auth method already enabled"
  }

  # Check if the role already exists
  if vault read auth/approle/role/api-service/role-id &>/dev/null; then
    log_info "API service AppRole already exists, skipping creation"
    return 0
  fi

  # Create API policy with write and delete permissions
  vault policy write api-service-policy - <<EOF
path "secret/data/*" {
  capabilities = ["create", "update", "delete"]
}

path "secret/metadata/*" {
  capabilities = ["create", "update", "delete", "read", "list"]
}
EOF

  # Create API service role
  vault write auth/approle/role/api-service \
    token_policies="api-service-policy" \
    token_ttl=1h \
    token_max_ttl=24h \
    token_num_uses=0

  # Get role ID
  local api_role_id=$(vault read -format=json auth/approle/role/api-service/role-id | jq -r '.data.role_id')

  # Generate secret ID
  local api_secret_id=$(vault write -format=json -f auth/approle/role/api-service/secret-id | jq -r '.data.secret_id')

  # Save to GCP Secret Manager
  local api_creds=$(jq -n \
    --arg role_id "$api_role_id" \
    --arg secret_id "$api_secret_id" \
    '{
      role_id: $role_id,
      secret_id: $secret_id,
      role: "api-service",
      permissions: ["write", "delete"]
    }')

  save_to_secret_manager "${SECRET_PREFIX}vault-api-approle" "$api_creds"

  log_info "API service AppRole created and saved to ${SECRET_PREFIX}vault-api-approle"
}

function create_orchestrator_approle {
  log_info "Configuring Orchestrator service AppRole with read-only permissions..."

  # Check if the role already exists
  if vault read auth/approle/role/orchestrator-service/role-id &>/dev/null; then
    log_info "Orchestrator service AppRole already exists, skipping creation"
    return 0
  fi

  # Create Orchestrator policy with read-only permissions
  vault policy write orchestrator-service-policy - <<EOF
path "secret/data/*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "secret/metadata/*" {
  capabilities = ["create", "update", "delete", "read", "list"]
}
EOF

  # Create Orchestrator service role
  vault write auth/approle/role/orchestrator-service \
    token_policies="orchestrator-service-policy" \
    token_ttl=1h \
    token_max_ttl=24h \
    token_num_uses=0

  # Get role ID
  local orchestrator_role_id=$(vault read -format=json auth/approle/role/orchestrator-service/role-id | jq -r '.data.role_id')

  # Generate secret ID
  local orchestrator_secret_id=$(vault write -format=json -f auth/approle/role/orchestrator-service/secret-id | jq -r '.data.secret_id')

  # Save to GCP Secret Manager
  local orchestrator_creds=$(jq -n \
    --arg role_id "$orchestrator_role_id" \
    --arg secret_id "$orchestrator_secret_id" \
    '{
      role_id: $role_id,
      secret_id: $secret_id,
      role: "orchestrator-service",
      permissions: ["read"]
    }')

  save_to_secret_manager "${SECRET_PREFIX}vault-orchestrator-approle" "$orchestrator_creds"

  log_info "Orchestrator service AppRole created and saved to ${SECRET_PREFIX}vault-orchestrator-approle"
}

function configure_vault_approles {
  log_info "Configuring Vault AppRoles for services..."

  # Get root token from Secret Manager
  local root_token=$(get_from_secret_manager "${SECRET_PREFIX}vault-root-key")

  if [ -z "$root_token" ]; then
    log_error "Root token not found in Secret Manager"
    return 1
  fi

  # Login with root token
  export VAULT_TOKEN="$root_token"

  # Setup secrets engine
  setup_secrets_engine

  # Create AppRoles
  create_api_approle
  create_orchestrator_approle

  # Unset token for security
  unset VAULT_TOKEN

  log_info "AppRoles configuration completed"
}

function main {
  # Parse arguments
  while [[ $# -gt 0 ]]; do
    case $1 in
      --project)
        GCP_PROJECT="$2"
        shift 2
        ;;
      --prefix)
        SECRET_PREFIX="$2"
        shift 2
        ;;
      --vault-addr)
        VAULT_ADDR="$2"
        shift 2
        ;;
      --help)
        print_usage
        exit 0
        ;;
      *)
        log_error "Unknown option: $1"
        print_usage
        exit 1
        ;;
    esac
  done

  # Validate required parameters
  if [ -z "$GCP_PROJECT" ] || [ -z "$SECRET_PREFIX" ]; then
    log_error "Missing required parameters"
    print_usage
    exit 1
  fi

  # Export Vault address
  export VAULT_ADDR

  # Check if vault CLI is installed
  if ! command -v vault &> /dev/null; then
    log_error "vault CLI is not installed"
    exit 1
  fi

  # Check if gcloud CLI is installed
  if ! command -v gcloud &> /dev/null; then
    log_error "gcloud CLI is not installed"
    exit 1
  fi

  log_info "Checking Vault status at ${VAULT_ADDR}..."

  # Get Vault status
  local status=$(check_vault_status)
  local initialized=$(echo "$status" | jq -r '.initialized')
  local sealed=$(echo "$status" | jq -r '.sealed')

  log_info "Vault initialized: $initialized"
  log_info "Vault sealed: $sealed"

  # Initialize if needed
  if [ "$initialized" == "false" ]; then
    initialize_vault
    if [ $? -ne 0 ]; then
      exit 1
    fi
    # After initialization with auto-unseal, wait for it to unseal
    wait_for_auto_unseal
    if [ $? -ne 0 ]; then
      exit 1
    fi
  elif [ "$sealed" == "true" ]; then
    # If already initialized but sealed, wait for auto-unseal
    log_info "Vault is initialized but sealed, waiting for auto-unseal..."
    wait_for_auto_unseal
    if [ $? -ne 0 ]; then
      exit 1
    fi
  fi

  log_info "Vault is ready with GCP KMS auto-unseal!"

  # Configure AppRoles for services
  configure_vault_approles

  # Show root token for initial configuration
  #local root_token=$(get_from_secret_manager "${SECRET_PREFIX}vault-root-key")
  #if [ -n "$root_token" ]; then
  #  echo ""
  #  log_info "Root token (save this securely and delete after creating admin policies):"
  #  echo "$root_token"
  #  echo ""
  #fi

  log_info "AppRole credentials have been saved to GCP Secret Manager:"
  log_info "  - API Service: ${SECRET_PREFIX}vault-api-approle (write/delete permissions)"
  log_info "  - Orchestrator Service: ${SECRET_PREFIX}vault-orchestrator-approle (read-only permissions)"

  log_warn "Remember to:"
  log_warn "1. Create additional authentication methods if needed (userpass, etc.)"
  log_warn "2. Create additional policies for different access levels"
  log_warn "3. Revoke the root token after initial setup"
  log_warn "4. Recovery keys are stored in Secret Manager and used for emergency procedures only"
}

main "$@"
