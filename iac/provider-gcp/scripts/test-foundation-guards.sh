#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
workspace="$(mktemp -d)"
trap 'rm -rf "${workspace}"' EXIT

fake_terraform="${workspace}/terraform"
state_mode_file="${workspace}/state-mode"
plan_json_file="${workspace}/plan.json"
plan_text_file="${workspace}/plan.txt"

cat >"${fake_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  state)
    case "$(cat "${STATE_MODE_FILE}")" in
      empty)
        printf 'No state file was found!\n' >&2
        exit 1
        ;;
      foundation)
        printf '%s\n' \
          'module.init.google_project_service.compute_engine_api' \
          'module.init.google_service_account.infra_instances_service_account'
        ;;
      outside)
        printf '%s\n' \
          'module.init.google_project_service.compute_engine_api' \
          'module.cluster.google_compute_instance.server'
        ;;
      credential)
        printf '%s\n' \
          'module.init.google_service_account_key.legacy'
        ;;
      legacy-acl)
        printf '%s\n' \
          'module.init.google_project_service.compute_engine_api' \
          'module.init.random_uuid.consul_acl_token' \
          'module.init.random_uuid.nomad_acl_token'
        ;;
      backend-error)
        printf 'failed to read remote backend\n' >&2
        exit 1
        ;;
      *)
        printf 'unknown state fixture\n' >&2
        exit 2
        ;;
    esac
    ;;
  show)
    if [[ "${2:-}" == "-json" ]]; then
      cat "${PLAN_JSON_FILE}"
    else
      cat "${PLAN_TEXT_FILE}"
    fi
    ;;
  *)
    printf 'unexpected fake Terraform command: %s\n' "${1:-<none>}" >&2
    exit 2
    ;;
esac
EOF
chmod +x "${fake_terraform}"

export STATE_MODE_FILE="${state_mode_file}"
export PLAN_JSON_FILE="${plan_json_file}"
export PLAN_TEXT_FILE="${plan_text_file}"
: >"${plan_text_file}"

expect_pass() {
  local description="$1"
  shift
  if ! "$@" >"${workspace}/stdout" 2>"${workspace}/stderr"; then
    printf 'expected pass: %s\n' "${description}" >&2
    cat "${workspace}/stderr" >&2
    exit 1
  fi
}

expect_fail() {
  local description="$1"
  shift
  if "$@" >"${workspace}/stdout" 2>"${workspace}/stderr"; then
    printf 'expected failure: %s\n' "${description}" >&2
    exit 1
  fi
}

printf 'empty' >"${state_mode_file}"
expect_pass "fresh state" "${script_dir}/assert-foundation-state.sh" "${fake_terraform}"

printf 'foundation' >"${state_mode_file}"
expect_pass "foundation-only state" "${script_dir}/assert-foundation-state.sh" "${fake_terraform}"

printf 'outside' >"${state_mode_file}"
expect_fail "state outside module.init" "${script_dir}/assert-foundation-state.sh" "${fake_terraform}"

printf 'credential' >"${state_mode_file}"
expect_fail "long-lived credential state" "${script_dir}/assert-foundation-state.sh" "${fake_terraform}"

printf 'legacy-acl' >"${state_mode_file}"
expect_fail "leaking legacy ACL generator state" "${script_dir}/assert-foundation-state.sh" "${fake_terraform}"

printf 'backend-error' >"${state_mode_file}"
expect_fail "backend inspection error" "${script_dir}/assert-foundation-state.sh" "${fake_terraform}"

cat >"${plan_json_file}" <<'JSON'
{"resource_changes":[{"address":"module.init.google_project_service.compute_engine_api","mode":"managed","type":"google_project_service","change":{"actions":["create"]}}]}
JSON
expect_pass "foundation create plan" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply

cat >"${plan_json_file}" <<'JSON'
{
  "variables": {
    "gcp_project_id": {"value": "monad-code"},
    "gcp_region": {"value": "us-east4"},
    "environment": {"value": "dev"}
  },
  "resource_changes": [
    {
      "address": "module.init.google_project_service.compute_engine_api",
      "mode": "managed",
      "type": "google_project_service",
      "change": {"actions": ["create"]}
    }
  ]
}
JSON
expect_pass \
  "plan identity matches selected context" \
  "${script_dir}/assert-foundation-plan.sh" \
  ignored \
  "${fake_terraform}" \
  apply \
  monad-code \
  us-east4 \
  dev

jq '.variables.gcp_project_id.value = "other-project"' \
  "${plan_json_file}" >"${plan_json_file}.next"
mv "${plan_json_file}.next" "${plan_json_file}"
expect_fail \
  "plan project cannot override selected context" \
  "${script_dir}/assert-foundation-plan.sh" \
  ignored \
  "${fake_terraform}" \
  apply \
  monad-code \
  us-east4 \
  dev

jq '.variables.gcp_project_id.value = "monad-code" | .variables.gcp_region.value = "us-west1"' \
  "${plan_json_file}" >"${plan_json_file}.next"
mv "${plan_json_file}.next" "${plan_json_file}"
expect_fail \
  "plan region cannot override selected context" \
  "${script_dir}/assert-foundation-plan.sh" \
  ignored \
  "${fake_terraform}" \
  apply \
  monad-code \
  us-east4 \
  dev

jq '.variables.gcp_region.value = "us-east4" | .variables.environment.value = "prod"' \
  "${plan_json_file}" >"${plan_json_file}.next"
mv "${plan_json_file}.next" "${plan_json_file}"
expect_fail \
  "plan environment cannot override selected context" \
  "${script_dir}/assert-foundation-plan.sh" \
  ignored \
  "${fake_terraform}" \
  apply \
  monad-code \
  us-east4 \
  dev

cat >"${plan_json_file}" <<'JSON'
{"resource_changes":[{"address":"module.cluster.google_compute_instance.server","mode":"managed","type":"google_compute_instance","change":{"actions":["create"]}}]}
JSON
expect_fail "plan outside module.init" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply

cat >"${plan_json_file}" <<'JSON'
{"resource_changes":[{"address":"module.init.google_project_service.compute_engine_api","mode":"managed","type":"google_project_service","change":{"actions":["delete"]}}]}
JSON
expect_fail "destructive foundation apply" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply
expect_pass "foundation destroy plan" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" destroy

cat >"${plan_json_file}" <<'JSON'
{"resource_changes":[{"address":"module.init.google_service_account_key.legacy","mode":"managed","type":"google_service_account_key","change":{"actions":["create"]}}]}
JSON
expect_fail "long-lived credential plan" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply

cat >"${plan_json_file}" <<'JSON'
{
  "resource_changes": [
    {
      "address": "module.init.random_password.consul_acl_token_seed",
      "mode": "managed",
      "type": "random_password",
      "name": "consul_acl_token_seed",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"result": "consul-seed-sentinel"},
        "after_sensitive": {"result": true}
      }
    },
    {
      "address": "module.init.google_secret_manager_secret_version.consul_acl_token_active",
      "mode": "managed",
      "type": "google_secret_manager_secret_version",
      "name": "consul_acl_token_active",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"secret_data": "consul-token-sentinel"},
        "after_sensitive": {"secret_data": true}
      }
    },
    {
      "address": "module.init.random_password.nomad_acl_token_seed",
      "mode": "managed",
      "type": "random_password",
      "name": "nomad_acl_token_seed",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"result": "nomad-seed-sentinel"},
        "after_sensitive": {"result": true}
      }
    },
    {
      "address": "module.init.google_secret_manager_secret_version.nomad_acl_token_active",
      "mode": "managed",
      "type": "google_secret_manager_secret_version",
      "name": "nomad_acl_token_active",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"secret_data": "nomad-token-sentinel"},
        "after_sensitive": {"secret_data": true}
      }
    }
  ]
}
JSON
printf 'All active ACL values are redacted as sensitive.\n' >"${plan_text_file}"
expect_pass "sensitive ACL plan is redacted" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply

jq '.resource_changes[0].change.after_sensitive.result = false' \
  "${plan_json_file}" >"${plan_json_file}.next"
mv "${plan_json_file}.next" "${plan_json_file}"
expect_fail "ACL seed must be sensitive" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply

jq '
  .resource_changes[0].change.after_sensitive.result = true
  | .resource_changes[1].change.after_sensitive.secret_data = false
' "${plan_json_file}" >"${plan_json_file}.next"
mv "${plan_json_file}.next" "${plan_json_file}"
expect_fail "ACL secret version must be sensitive" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply

jq '.resource_changes[1].change.after_sensitive.secret_data = true' \
  "${plan_json_file}" >"${plan_json_file}.next"
mv "${plan_json_file}.next" "${plan_json_file}"
printf 'leaked value: consul-token-sentinel\n' >"${plan_text_file}"
expect_fail "ACL material cannot appear in human plan output" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" apply
: >"${plan_text_file}"

cat >"${plan_json_file}" <<'JSON'
{
  "resource_changes": [
    {
      "address": "module.init.random_uuid.consul_acl_token",
      "mode": "managed",
      "type": "random_uuid",
      "name": "consul_acl_token",
      "change": {"actions": ["delete"]}
    }
  ]
}
JSON
expect_fail "legacy ACL UUID generator plan" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" destroy

cat >"${plan_json_file}" <<'JSON'
{"resource_changes":[{"address":"module.init.google_project_service.compute_engine_api","mode":"managed","type":"google_project_service","change":{"actions":["create"]}}]}
JSON
expect_fail "create action in destroy plan" "${script_dir}/assert-foundation-plan.sh" ignored "${fake_terraform}" destroy

printf 'Foundation guard fixtures passed.\n'
