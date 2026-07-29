# Operator canary credential bootstrap

This command creates one uniquely named synthetic team and API key, stores the
raw key as a new Secret Manager secret version through stdin, and persists only
the non-secret identifiers needed for crash recovery. It never prints or writes
the raw key.

Use a private path outside the repository for `STATE_FILE`. The command creates
the file with mode `0600` before mutating either system and refuses to overwrite
an existing state file:

```bash
make -C packages/db canary-bootstrap \
  GCP_PROJECT_ID=monad-code \
  STATE_FILE=/private/operator-state/monad-sdk-canary.json
```

`POSTGRES_CONNECTION_STRING` must already be present in the environment. It is
removed, together with any ambient E2B API/access key, from every `gcloud`
child process. The generated E2B API key is passed only to:

```text
gcloud secrets versions add ... --data-file=-
```

If bootstrap fails, the command rolls back the database transaction, deletes
any committed canary identifiers it can find, deletes the unique secret, and
removes the state file. If reconciliation cannot finish, the private state file
is retained and the command prints the exact retry instruction without any
credential.

After the raw SDK lifecycle canary has deleted every sandbox and explicit
snapshot, delete the API-key row, team, Secret Manager secret, and reconciliation
file explicitly:

```bash
make -C packages/db canary-cleanup \
  STATE_FILE=/private/operator-state/monad-sdk-canary.json
```

Cleanup is idempotent across partial database/secret deletion. Before deleting
anything, it locks and verifies the exact generated team/API-key identity and
requires the Secret Manager object to retain its `purpose=monad-sdk-canary`
label and recorded name. A mismatched state file fails closed. Do not use the
general development seed helper for this canary, do not place the state file in
the repository, and do not allow the synthetic credential to reach customer
data or production workloads.
