#!/bin/bash
set -euo pipefail

# Guard against raw structured-log identifier keys.
#
# Identifier fields (sandbox/team/template/build/node/cluster/user/execution/
# service-instance IDs) must be logged through the canonical logger.With* helpers
# in packages/shared/pkg/logger/fields.go, which emit a single agreed "<noun>.id"
# key. Logging a raw zap.String("team_id", ...) / zap.String("teamID", ...) etc.
# reintroduces the key-name drift that forces log queries and alerts to match a
# concept under several spellings across api / orchestrator / client-proxy.
#
# golangci-lint's forbidigo cannot express this: it matches the called function
# name, not a string-literal argument, so a grep gate is used instead.
#
# A line may opt out with a trailing "log-key-exempt:" comment (used only for the
# Vector-legacy aliases in logger/sandbox/metadata.go during the migration
# window). Test files are not scanned.

# Raw keys that each already have a canonical With* helper. Extend alongside
# fields.go.
FORBIDDEN='sandbox_id|sandboxID|sandboxId|team_id|teamID|teamId|template_id|templateID|templateId|build_id|buildID|buildId|node_id|nodeID|nodeId|cluster_id|clusterID|clusterId|instance_id|instanceID|env_id|envID'

PATTERN="zap\\.String\\(\"($FORBIDDEN)\""

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

# All non-test Go files, excluding generated code.
mapfile -t files < <(git ls-files 'packages/**/*.go' | grep -vE '_test\.go$|\.gen\.go$|\.pb\.go$')

hits=0
for f in "${files[@]}"; do
  while IFS= read -r line; do
    # line is "lineno:content"
    content="${line#*:}"
    case "$content" in
      *log-key-exempt:*) continue ;;
    esac
    printf '%s:%s\n' "$f" "$line"
    hits=$((hits + 1))
  done < <(grep -nE "$PATTERN" "$f" 2>/dev/null || true)
done

if [[ "$hits" -gt 0 ]]; then
  cat >&2 <<'MSG'

Raw structured-log identifier key(s) found above.

Use the canonical helpers from packages/shared/pkg/logger instead, e.g.:
    zap.String("team_id", id)   ->   logger.WithTeamID(id)
    zap.String("build_id", id)  ->   logger.WithBuildID(id)

This keeps one key per concept (team.id, build.id, ...) across every service.
If a line is a deliberate, documented exception, append a trailing comment
containing "log-key-exempt:".
MSG
  exit 1
fi

echo "log field keys OK: no raw identifier keys found"
