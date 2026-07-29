# GCP E2B raw lifecycle canary

This package is the bounded operator-only proof for the first Monad GCP E2B
workcell. It locks `e2b` to exactly `2.21.0`, refuses a non-HTTPS control-plane
URL, starts with an empty synthetic team, and never admits more than two
sandboxes.

The lifecycle proves:

1. immutable template build and marker verification;
2. source create and fixed command execution with internet denied;
3. pause plus `Sandbox.connect` resume;
4. explicit snapshot, restore, state verification, and restored-sandbox delete;
5. raw `POST /sandboxes/{id}/fork` with `X-API-Key`,
   `{"count":1,"timeout":900}`, HTTP 201, per-result validation, and
   source/fork divergence;
6. source, fork, restored sandbox, and explicit snapshot deletion; and
7. an empty final sandbox inventory.

Install only the checked lock:

```bash
npm ci --prefix tests/operator-canary
```

Use the unique synthetic credential created by
`packages/db/scripts/canary/bootstrap`; never echo it or put it in a file:

```bash
export E2B_API_KEY="$(
  gcloud secrets versions access latest \
    --project monad-code \
    --secret <unique-canary-secret-id>
)"
export E2B_API_URL=https://api.e2b.monad0.net
export E2B_DOMAIN=e2b.monad0.net
```

Build a uniquely tagged base template and retain the non-secret JSON result:

```bash
E2B_TEMPLATE_NAME=monad-gcp-canary-base:infra-<reviewed-sha> \
  npm run --prefix tests/operator-canary build-template
```

The build refuses to overwrite an existing name/tag. Set `E2B_TEMPLATE` to the
returned `template_ref`, run the canary, and
retain its non-secret JSON evidence:

```bash
E2B_TEMPLATE=<returned-template-name-and-tag> \
  npm run --prefix tests/operator-canary canary
```

If the lifecycle fails, the script still inventories and deletes every sandbox
owned by the unique synthetic team and every explicit snapshot it can identify.
Do not clean up the synthetic team/key until the final inventory is empty.
