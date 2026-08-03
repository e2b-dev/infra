# Monad invited-beta Cloud SQL migration

This runbook moves the live workload from the retained operator-canary database
to the regional invited-beta database without modifying or replacing the source
instance in place.

## Fixed identities

- Source: `e2b-postgres-canary` (`db-f1-micro`, zonal, PD_HDD).
- Candidate: `e2b-postgres-beta` (`db-custom-2-7680`, regional, PD_SSD).
- Active application secret: the existing `postgres-connection-string` secret.

Provisioning the candidate must not change the active application secret. A
separate reviewed cutover changes that binding only after migration evidence is
complete.

## Phase 1: provision only

1. Require a clean live Terraform plan with no replacement or deletion of
   `google_sql_database_instance.operator_canary` and no unrelated cluster
   changes.
2. Create the regional candidate, its database, user, generated password, and
   candidate-only Secret Manager password version.
3. Confirm private-only networking, encrypted-only connections, regional HA,
   PITR, seven retained backups, and deletion protection through both Terraform
   evidence and the Cloud SQL API.
4. Confirm the API and Nomad jobs still resolve the source connection secret.

Stop if the source instance, active connection secret, or workload cluster
appears in the plan with a destructive or replacement action.

Use the dedicated prerequisite path for this phase; the full workload plan may
contain independent Nomad job reconciliation and is not the provisioning
artifact:

```bash
make -C iac/provider-gcp ENV=dev workload-prerequisite-plan
terraform -chdir=iac/provider-gcp show .tfplan.workload-prerequisite.dev
make -C iac/provider-gcp ENV=dev workload-prerequisite-apply \
  CONFIRM='APPLY WORKLOAD PREREQUISITES'
```

The reviewed environment must set `API_SERVER_COUNT=2`. Before candidate
creation, the expected saved plan is six creates (candidate instance,
database, user, generated password, candidate-only password secret, and secret
version), verified no-ops for the existing prerequisites, and at most the exact
connection-budget bookkeeping update from one to two API allocations (`19` to
`28` aggregate connections). Nomad resources, source/active-secret mutations,
deferred data reads, replacements, and deletes are forbidden.

## Phase 2: copy and validate

1. Take an on-demand source backup and record its identifier and completion
   time.
2. Run the copy from a private operator path with a four-connection ceiling.
   Credentials must be read from Secret Manager at execution time and must not
   be written to the repository, shell history, logs, artifacts, or instance
   metadata.
3. Apply the exact deployed schema migration set to the candidate.
4. Compare schema, table counts, critical row counts, and application-level
   reads. Record replication/copy lag and the latest copied transaction.
5. Exercise API health and read/write probes against the candidate through a
   temporary operator-only job. Ordinary traffic remains on the source.

## Phase 3: cut over

1. Announce and enforce a bounded write pause.
2. Apply the final delta and repeat the integrity checks.
3. Publish a new version of the existing active connection secret that points
   to the candidate. Do not change the secret identity.
4. Restart the two API allocations serially, then restart the remaining Nomad
   consumers. Require health, migration-version, and write/read-after-write
   evidence after each step.
5. End the write pause only after every active allocation uses the candidate.

The cutover PR and operator evidence must name the exact TAMS SHA, infra SHA,
source backup, candidate instance, secret version, migration version, and
rollback deadline.

## Rollback

Before the rollback deadline, restore the prior active connection-secret
version and restart consumers serially. Reconcile writes made after cutover
before reopening traffic; never point two writable application fleets at both
databases concurrently.

Keep the source instance protected and available until the invited-beta load,
failover, and rollback drills pass and an explicit decommission PR is approved.
No Terraform change in the provisioning phase is authorized to destroy it.

## Advancement evidence

- Candidate CPU, connections, memory, disk latency, and database p95 under the
  25-active/75-queued test.
- Cloud SQL regional failover with successful API recovery.
- Zero failed migrations, lost writes, cross-tenant reads, or secret leakage.
- A successful rollback drill using recorded secret versions.
- A final Terraform plan with no accidental source-instance destruction.

Move the candidate to `db-custom-4-15360` before cohort rollout if the load test
exceeds 70 percent CPU or connection utilization, or 100 ms database p95.
