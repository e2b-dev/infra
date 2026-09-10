#!/usr/bin/env bats

# host-setup.sh is sourced with HS_NO_MAIN=1; HS_IPTABLES points at a fake that
# records its arguments and answers -C from a file, so the clamp logic runs
# unprivileged on any machine. HS_MSS_MARKER redirects the ownership marker
# into the tmpdir: whether it exists after clamp_mss is the contract
# host-teardown reads to decide whether the clamp is the stack's to remove.
# Negative assertions use `run cmd` plus a status check, never a bare `! cmd`:
# in bats a `!`-negated command is exempt from errexit and cannot fail a test
# wherever it sits (shellcheck SC2314). The assertions spell out the `-w 5`
# xtables-lock flag, so dropping it from any real iptables invocation turns
# this file red.
setup() {
  export HS_NO_MAIN=1
  export HS_IPTABLES="$BATS_TEST_TMPDIR/iptables"
  export FAKE_LOG="$BATS_TEST_TMPDIR/calls" FAKE_CHECK_RC="$BATS_TEST_TMPDIR/check-rc"
  # The ownership marker, pointed at the tmpdir so no test touches a real
  # /var/lib/e2b. Its directory is deliberately absent: clamp_mss creates it,
  # because on a fresh host the clamp runs before host-setup's install -d.
  export HS_MSS_MARKER="$BATS_TEST_TMPDIR/host/var/lib/e2b/mss-clamp.owned"
  cat > "$HS_IPTABLES" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$FAKE_LOG"
case " $* " in
  *" -C "*) exit "$(cat "$FAKE_CHECK_RC")" ;;
  *) exit 0 ;;
esac
EOF
  chmod +x "$HS_IPTABLES"
  # shellcheck disable=SC1091
  source "$BATS_TEST_DIRNAME/../compose/scripts/host-setup.sh"
}

@test "adds the MSS clamp when it is missing, and claims it" {
  echo 1 > "$FAKE_CHECK_RC"
  run clamp_mss
  [ "$status" -eq 0 ]
  [[ "$output" == *"MSS clamp added"* ]]
  grep -q -- "-w 5 -t mangle -C FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu" "$FAKE_LOG"
  grep -q -- "-w 5 -t mangle -I FORWARD 1 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu" "$FAKE_LOG"
  # The marker, and the directory it needed, are created by clamp_mss itself.
  [ -f "$HS_MSS_MARKER" ]
}

# The rule was already there, so it is not ours: no second rule is inserted
# (the original bug this branch fixes) and no marker is written, which is what
# stops host-teardown deleting a clamp the host configured itself.
@test "is idempotent when the rule is already present, and claims nothing" {
  echo 0 > "$FAKE_CHECK_RC"
  run clamp_mss
  [ "$status" -eq 0 ]
  [[ "$output" == *"already present"* ]]
  grep -q -- "-w 5 -t mangle -C FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu" "$FAKE_LOG"
  run grep -q -- " -I " "$FAKE_LOG"
  [ "$status" -ne 0 ]
  [ ! -e "$HS_MSS_MARKER" ]
}

@test "ends with a FIX line when iptables cannot insert the rule" {
  echo 1 > "$FAKE_CHECK_RC"
  printf '#!/usr/bin/env bash\ncase " $* " in *" -C "*) exit 1 ;; *) exit 2 ;; esac\n' > "$HS_IPTABLES"
  run clamp_mss
  [ "$status" -eq 1 ]
  [[ "$output" == *"FIX: install iptables"* ]]
  # An insert that failed claims nothing either.
  [ ! -e "$HS_MSS_MARKER" ]
}

@test "dies with a FIX line when the iptables check hits the xtables lock" {
  cat > "$HS_IPTABLES" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$FAKE_LOG"
case " $* " in
  *" -C "*) echo "Another app is currently holding the xtables lock." >&2; exit 4 ;;
  *) exit 0 ;;
esac
EOF
  chmod +x "$HS_IPTABLES"
  run clamp_mss
  [ "$status" -eq 1 ]
  [[ "$output" == *"FIX:"* ]]
  [[ "$output" == *"iptables -t mangle -S FORWARD"* ]]
  run grep -q -- " -I " "$FAKE_LOG"
  [ "$status" -ne 0 ]
}
