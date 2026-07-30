#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"

# Internal GCP runtime identity must come from Application Default
# Credentials on the attached VM service account. These patterns cover the
# former environment, Terraform, Docker and backup key seams.
forbidden_pattern='GOOGLE_SERVICE_ACCOUNT_BASE64|GOOGLE_SERVICE_ACCOUNT_KEY|DOCKER_AUTH_BASE64|GCS_CREDENTIALS_JSON(_ENCODED)?|_json_key(_base64)?|google_service_account_key|google_storage_hmac_key|NewJSONKeyAuthenticator|[Ss]erviceAccountJson|random_uuid\.(consul_acl_token|nomad_acl_token)'

explicit_paths=false
if [[ "$#" -gt 0 ]]; then
  explicit_paths=true
  scan_paths=("$@")
else
  scan_paths=(".")
fi

cd "$repo_root"
umask 077
scan_error="$(mktemp)"
trap 'rm -f "${scan_error}"' EXIT

set +e
matches="$(
  grep -RInE -I \
    --exclude-dir=.git \
    --exclude-dir=.terraform \
    --exclude-dir=node_modules \
    --exclude-dir=vendor \
    --exclude=assert-keyless-runtime.sh \
    --exclude=test-keyless-runtime-guard.sh \
    -- "$forbidden_pattern" "${scan_paths[@]}" \
    2>"${scan_error}"
)"
scan_status=$?
set -e

if [[ "${scan_status}" -gt 1 ]]; then
  echo "Unable to complete the static runtime credential scan:" >&2
  cat "${scan_error}" >&2
  exit 1
fi

if [[ -z "$matches" ]]; then
  echo "Keyless GCP runtime guard passed."
  exit 0
fi

# When scanning the repository, retain only deliberate non-runtime uses:
# - the foundation state/plan guards detect forbidden Terraform resources;
# - API/protobuf files represent customer-supplied credentials for pulling a
#   customer's private registry and are not Monad's GCP runtime identity.
# Explicit paths, used by the guard test and CI callers, have no allowlist.
unexpected=()
while IFS= read -r match; do
  [[ -z "$match" ]] && continue

  if [[ "$explicit_paths" == "false" ]]; then
    path="${match%%:*}"
    path="${path#./}"
    case "$path" in
      .github/upstream-workflows-disabled/* | \
        docs/MONAD_F1_LIVE_EVIDENCE_2026-07-30.md | \
        docs/MONAD_GCP_FOUNDATION.md | \
        iac/provider-aws/init/secrets-cluster.tf | \
        iac/provider-gcp/scripts/assert-no-legacy-acl-token-state.sh | \
        iac/provider-gcp/scripts/assert-foundation-plan.sh | \
        iac/provider-gcp/scripts/assert-foundation-state.sh | \
        iac/provider-gcp/scripts/scrub-legacy-acl-token-state.sh | \
        iac/provider-gcp/scripts/test-foundation-guards.sh | \
        iac/provider-gcp/scripts/test-scrub-legacy-acl-token-state.sh | \
        packages/orchestrator/template-manager.proto | \
        spec/openapi.yml | \
        packages/api/internal/template-manager/create_template.go | \
        packages/api/internal/api/api.gen.go | \
        packages/orchestrator/pkg/template/build/core/oci/auth/gcp.go | \
        packages/shared/pkg/grpc/template-manager/template-manager.pb.go | \
        tests/integration/internal/api/generated.go)
        continue
        ;;
    esac
  fi

  unexpected+=("$match")
done <<<"$matches"

if [[ "${#unexpected[@]}" -gt 0 ]]; then
  echo "Static GCP credentials are forbidden in runtime paths:" >&2
  printf '  %s\n' "${unexpected[@]}" >&2
  exit 1
fi

echo "Keyless GCP runtime guard passed."
