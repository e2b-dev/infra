#!/bin/bash
# This script is meant to be run in the Startup Script of each Compute Instance while it's booting. The script uses the
# run-nomad and run-consul scripts to configure and start Consul and Nomad in server mode. Note that this script
# assumes it's running in a Google IMage built from the Packer template in examples/nomad-consul-image/nomad-consul.json.

set -e

# Send the log output from this script to user-data.log, syslog, and the console
# Inspired by https://alestic.com/2010/12/ec2-user-data-output/
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 65536
export GOMAXPROCS='nproc'

gsutil cp "gs://${SCRIPTS_BUCKET}/run-consul-${RUN_CONSUL_FILE_HASH}.sh" /opt/consul/bin/run-consul.sh
gsutil cp "gs://${SCRIPTS_BUCKET}/run-nomad-${RUN_NOMAD_FILE_HASH}.sh" /opt/nomad/bin/run-nomad.sh

chmod +x /opt/consul/bin/run-consul.sh /opt/nomad/bin/run-nomad.sh

# Keep the Nomad credential out of argv, logs, process-wide environment, and
# Supervisor configuration. This stable /run contract is populated directly
# from Secret Manager by the ACL-bootstrap lane after rebase; until then it
# safely replaces the pre-existing raw-token argv handoff. /run is cleared on
# reboot, and both the bootstrap and health service fail closed on unsafe mode,
# owner, symlink, or content.
health_token_dir='/run/e2b-nomad-health'
health_token_path="$health_token_dir/token"
install -d -o root -g root -m 0700 "$health_token_dir"
health_token_tmp="$(mktemp "$health_token_dir/token.XXXXXX")"
trap 'rm -f -- "$health_token_tmp"' EXIT
chmod 0600 "$health_token_tmp"
chown root:root "$health_token_tmp"
printf '%s' "${NOMAD_TOKEN}" >"$health_token_tmp"
mv -f -- "$health_token_tmp" "$health_token_path"
trap - EXIT

health_script_path='/opt/nomad/bin/nomad-voter-health.py'
health_script_tmp="$(mktemp "$health_script_path.XXXXXX")"
trap 'rm -f -- "$health_script_tmp"' EXIT
cat >"$health_script_tmp" <<'PY'
${NOMAD_VOTER_HEALTH_SCRIPT}
PY
chown root:root "$health_script_tmp"
chmod 0755 "$health_script_tmp"
mv -f -- "$health_script_tmp" "$health_script_path"
trap - EXIT

/opt/consul/bin/run-consul.sh --server --cluster-tag-name "${CLUSTER_TAG_NAME}" --consul-token "${CONSUL_TOKEN}" --enable-gossip-encryption --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}"
/opt/nomad/bin/run-nomad.sh --server --num-servers "${NUM_SERVERS}" --consul-token "${CONSUL_TOKEN}" --nomad-token-file "$health_token_path"

cat >/etc/supervisor/conf.d/nomad-voter-health.conf <<'EOF'
[program:nomad-voter-health]
command=/usr/bin/python3 /opt/nomad/bin/nomad-voter-health.py
user=root
autostart=true
autorestart=true
startsecs=2
startretries=30
stopsignal=TERM
stopasgroup=true
killasgroup=true
redirect_stderr=true
stdout_logfile=/var/log/nomad-voter-health.log
stdout_logfile_maxbytes=10485760
stdout_logfile_backups=3
EOF

supervisorctl reread
supervisorctl update
