#!/usr/bin/env bats

# The StatefulSet is a translation of compose/compose.yaml; these tests keep
# the two from drifting. Needs kubectl (for `kubectl kustomize`) and python3;
# the tests that compare against compose also need Docker with the compose
# plugin and jq, as team-api-key.bats already does.
#
# The rendered manifest is read as text, not through a YAML parser: python3's
# yaml module is not guaranteed on the test host. kustomize emits two-space
# indentation with sorted keys, so a container's `name:` and `startupProbe:`
# both sit at eight spaces and everything nested under them is deeper.

setup() {
  cd "$BATS_TEST_DIRNAME/.." || return 1
  RENDERED="$(kubectl kustomize kubernetes)"
}

# `service period timeout failures` per container that has a startup probe.
startup_probes() {
  printf '%s\n' "$RENDERED" | python3 -c '
import re, sys
lines = sys.stdin.read().splitlines()
name = None
for i, line in enumerate(lines):
    m = re.match(r"^ {8}name: (\S+)$", line)
    if m:
        name = m.group(1)
        continue
    if line != "        startupProbe:" or name is None:
        continue
    v = {}
    for nxt in lines[i + 1:]:
        if not nxt.startswith(" " * 10):
            break
        k = re.match(r"^ {10}(periodSeconds|timeoutSeconds|failureThreshold): ([0-9]+)$", nxt)
        if k:
            v[k.group(1)] = k.group(2)
    print(name, v.get("periodSeconds", "-"), v.get("timeoutSeconds", "-"), v.get("failureThreshold", "-"))
'
}

# The shell of the ready container's command, found by structure rather than by
# any of the text it is about to be compared against: `command` sorts before
# `name`, so the first eight-space `name:` after a block scalar names the
# container the block belongs to.
rendered_ready_script() {
  printf '%s\n' "$RENDERED" | python3 -c '
import re, sys
lines = sys.stdin.read().splitlines()
i = 0
while i < len(lines):
    if lines[i] != "        - |":
        i += 1
        continue
    j, block = i + 1, []
    while j < len(lines) and (lines[j].startswith(" " * 10) or lines[j] == ""):
        block.append(lines[j][10:])
        j += 1
    owner = next(
        (m.group(1) for m in (re.match(r"^ {8}name: (\S+)$", x) for x in lines[j:]) if m),
        None,
    )
    if owner == "ready":
        sys.stdout.write("\n".join(block).rstrip("\n") + "\n")
        sys.exit(0)
    i = j
sys.exit("no ready container with a shell command in the rendered manifest")
'
}

# Both ready scripts start with a host-specific assignment prologue and share
# everything from this line down.
ready_body() {
  awk 'f || /^test -s \/run\/e2b\/team-api-key \|\| \{$/ { f = 1; print }'
}

env_value() { sed -n "s/^$1=\([^ #]*\).*/\1/p" compose/.env; }

@test "kustomization renders" {
  [ -n "$RENDERED" ]
}

@test "image tags equal the pins in compose/.env" {
  for var in E2B_API_IMAGE E2B_DB_MIGRATOR_IMAGE E2B_CLIENT_PROXY_IMAGE E2B_CLICKHOUSE_MIGRATOR_IMAGE E2B_TOOLS_IMAGE E2B_NODE_E2B_IMAGE E2B_SEED_IMAGE; do
    image="$(env_value "$var")"
    [ -n "$image" ]
    echo "$RENDERED" | grep -q "image: $image\$" || { echo "missing $image"; return 1; }
  done
}

@test "artifact versions equal the pins in compose/.env" {
  for var in E2B_ORCHESTRATOR_VERSION E2B_ENVD_VERSION E2B_KERNEL_VERSION E2B_FIRECRACKER_VERSION E2B_BUSYBOX_VERSION; do
    value="$(env_value "$var")"
    echo "$RENDERED" | grep -q "$var: $value\$" || { echo "missing $var=$value"; return 1; }
  done
}

# Nothing else pins these four: kustomization.yaml overrides only the E2B
# images, and no .env variable stands behind them. A bump applied to one file
# alone is invisible until the two shapes run different store versions.
@test "the store images equal compose's" {
  compose_json="$(docker compose --project-directory compose config --format json)"
  for store in postgres redis clickhouse vector; do
    want="$(printf '%s' "$compose_json" | jq -r --arg s "$store" '.services[$s].image')"
    [ -n "$want" ] && [ "$want" != null ] || { echo "compose has no image for $store"; return 1; }
    got="$(awk -v n="        - name: $store" '
      $0 == n { f = 1; next }
      f && /^ +image: / { print $2; exit }' kubernetes/statefulset.yaml)"
    [ "$got" = "$want" ] || {
      echo "$store: the manifest pins $got, compose pins $want"
      return 1
    }
  done
}

@test "the config copies are the documented derivation of the source configs" {
  python3 compose/scripts/dev/sync-configs.py --print-k8s compose/config/clickhouse/config.xml > "$BATS_TEST_TMPDIR/clickhouse"
  cmp "$BATS_TEST_TMPDIR/clickhouse" kubernetes/config/clickhouse-config.xml
  python3 compose/scripts/dev/sync-configs.py --print-k8s compose/config/vector/vector.toml > "$BATS_TEST_TMPDIR/vector"
  cmp "$BATS_TEST_TMPDIR/vector" kubernetes/config/vector.toml
}

@test "every store binds loopback on the host network" {
  run grep -rn '0.0.0.0' kubernetes/config
  [ "$status" -eq 1 ]
  grep -q 'http://127.0.0.1:8123' kubernetes/config/vector.toml
  grep -q 'address = "127.0.0.1:20006"' kubernetes/config/vector.toml
  echo "$RENDERED" | grep -q 'http://127.0.0.1:20006'
  # 30006 is inside the default NodePort range; a NodePort allocated it would
  # DNAT the pod's own loopback log POSTs away from Vector.
  run grep -n ':30006' <<<"$RENDERED"
  [ "$status" -eq 1 ]
  grep -q '<listen_host>127.0.0.1</listen_host>' kubernetes/config/clickhouse-config.xml
  [ "$(cat kubernetes/config/clickhouse-docker-related-config.xml)" = '<clickhouse/>' ]
  echo "$RENDERED" | grep -q -- '- listen_addresses=127.0.0.1$'
  echo "$RENDERED" | grep -q -- '- --bind$'
  echo "$RENDERED" | grep -q 'docker_related_config.xml'
  run grep -n '0.0.0.0' <<< "$RENDERED"
  [ "$status" -eq 1 ]
}

@test "the pod shares the host network and pid namespaces" {
  echo "$RENDERED" | grep -q '^      hostNetwork: true$'
  echo "$RENDERED" | grep -q '^      hostPID: true$'
}

@test "the data lives on the node under /var/lib/e2b/data and the pod is pinned to it" {
  for d in postgres clickhouse seed-state; do
    echo "$RENDERED" | grep -q "path: /var/lib/e2b/data/$d\$" || { echo "missing hostPath for $d"; return 1; }
  done
  echo "$RENDERED" | grep -q 'e2b.dev/single-node: "true"'
  run grep -c volumeClaimTemplates <<<"$RENDERED"
  [ "$status" -eq 1 ]
}

@test "the orchestrator requests the hugepages its sandboxes fault and sweeps the host cgroup tree" {
  echo "$RENDERED" | grep -q 'hugepages-2Mi: 4Gi'
  echo "$RENDERED" | grep -q 'OL_CGROUP_ROOT'
  echo "$RENDERED" | grep -q 'mountPath: /host-cgroup'
}

@test "every startup probe carries its compose healthcheck timings" {
  probes="$(startup_probes)"
  [ "$(printf '%s\n' "$probes" | grep -c .)" -eq 7 ]
  compose_json="$(docker compose --project-directory compose config --format json)"
  for service in postgres redis clickhouse vector orchestrator api client-proxy; do
    # A compose duration is "10s" here, but older plugins emit nanoseconds.
    want="$(printf '%s' "$compose_json" | jq -r --arg s "$service" '
      def secs: tostring
        | if test("^[0-9]+s$") then rtrimstr("s")
          elif test("^[0-9]+$") then (tonumber / 1000000000 | floor | tostring)
          else . end;
      .services[$s].healthcheck
      | "\(.interval | secs) \(.timeout | secs) \(.retries)"')"
    got="$(printf '%s\n' "$probes" | sed -n "s/^$service //p")"
    [ -n "$got" ] || { echo "$service has no startup probe in the manifest"; return 1; }
    [ "$got" = "$want" ] || {
      echo "$service: probe period/timeout/failures is '$got', compose interval/timeout/retries is '$want'"
      return 1
    }
  done
}

@test "the hugepages host-setup reserves are the hugepages the orchestrator requests" {
  pages="$(printf '%s\n' "$RENDERED" | grep -A1 '^ *- name: HUGEPAGES$' | sed -n 's/^ *value: "\([0-9]*\)"$/\1/p')"
  [ -n "$pages" ] || { echo "no HUGEPAGES value in the manifest"; return 1; }
  # HUGEPAGES is deliberately absent from compose/.env: compose.yaml carries
  # the number as the default of ${HUGEPAGES:-N}, and that default is what a
  # compose host reserves. Both shapes have to hand host-setup the same one.
  # shellcheck disable=SC2016  # the ${HUGEPAGES:-N} is compose's, matched literally
  compose_pages="$(sed -n 's/^ *HUGEPAGES: \${HUGEPAGES:-\([0-9]*\)}$/\1/p' compose/compose.yaml)"
  [ -n "$compose_pages" ] || { echo "no HUGEPAGES default in compose/compose.yaml"; return 1; }
  [ "$pages" = "$compose_pages" ] || {
    echo "the manifest hands host-setup $pages pages, compose.yaml defaults to $compose_pages"
    return 1
  }
  reserved_mib=$((pages * 2))
  quantities="$(printf '%s\n' "$RENDERED" | sed -n 's/^ *hugepages-2Mi: \(.*\)$/\1/p')"
  # request and limit, and a hugepage resource must have both.
  [ "$(printf '%s\n' "$quantities" | grep -c .)" -eq 2 ]
  for q in $quantities; do
    case "$q" in
      *Gi) requested_mib=$(( ${q%Gi} * 1024 )) ;;
      *Mi) requested_mib=${q%Mi} ;;
      *) echo "hugepages-2Mi '$q' is neither Mi nor Gi"; return 1 ;;
    esac
    [ "$requested_mib" -eq "$reserved_mib" ] || {
      echo "host-setup reserves $pages pages = ${reserved_mib} MiB, the orchestrator asks for $q = ${requested_mib} MiB"
      return 1
    }
  done
}

@test "the ready script is compose's ready script" {
  # `docker compose config` re-escapes a literal $ as $$ so its output is a
  # compose file again; undo that before comparing with the manifest's shell.
  docker compose --project-directory compose config --format json |
    jq -r '.services.ready.command[2]' |
    sed 's/\$\$/$/g' |
    ready_body > "$BATS_TEST_TMPDIR/compose-ready"
  rendered_ready_script | ready_body > "$BATS_TEST_TMPDIR/k8s-ready"
  [ -s "$BATS_TEST_TMPDIR/compose-ready" ]
  [ -s "$BATS_TEST_TMPDIR/k8s-ready" ]
  # jq -r adds a newline to a string that already ends in one.
  printf '%s\n' "$(cat "$BATS_TEST_TMPDIR/compose-ready")" > "$BATS_TEST_TMPDIR/compose-ready.trimmed"
  printf '%s\n' "$(cat "$BATS_TEST_TMPDIR/k8s-ready")" > "$BATS_TEST_TMPDIR/k8s-ready.trimmed"
  run diff -u "$BATS_TEST_TMPDIR/compose-ready.trimmed" "$BATS_TEST_TMPDIR/k8s-ready.trimmed"
  [ "$status" -eq 0 ] || { echo "compose ready (-) against the manifest's (+):"; echo "$output"; return 1; }
}

# The parity test above compares only the shared body, so a syntax error in
# either prologue or in the epilogue it assigns slips past it. `/bin/sh` is
# what both containers hand the script to; `-n` is that same parser, stopped
# before it runs the loop.
@test "both ready scripts parse" {
  rendered_ready_script > "$BATS_TEST_TMPDIR/k8s-ready"
  [ -s "$BATS_TEST_TMPDIR/k8s-ready" ]
  run sh -n "$BATS_TEST_TMPDIR/k8s-ready"
  [ "$status" -eq 0 ] || { echo "the manifest's ready script does not parse:"; echo "$output"; return 1; }

  # As above: `docker compose config` writes a literal $ back out as $$.
  docker compose --project-directory compose config --format json |
    jq -r '.services.ready.command[2]' |
    sed 's/\$\$/$/g' > "$BATS_TEST_TMPDIR/compose-ready"
  [ -s "$BATS_TEST_TMPDIR/compose-ready" ]
  run sh -n "$BATS_TEST_TMPDIR/compose-ready"
  [ "$status" -eq 0 ] || { echo "compose's ready script does not parse:"; echo "$output"; return 1; }
}
