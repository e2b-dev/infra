# Monad GCP E2B canary status

Last verified: 2026-07-28 (Australia/Melbourne)

This file is an operator checkpoint for the first `dev` workcell canary. It
records evidence and the safe resume boundary; it is not a claim that TAMS is
already using GCP.

## Completed

- Keyless GCP foundation is applied in project `monad-code`, region
  `us-east4`, with workload zone `us-east4-c`.
- Core E2B release revision is pinned to `909df5f3fea8`.
- GCE image `e2b-orch-dev-candidate-a52586eafdec` passed disposable Docker,
  Nomad, Consul, and nested-KVM smoke tests.
- Canonical image family `e2b-orch` resolves to exact image ID
  `5598366152540933019`.
- No transient Packer/smoke instances or rollout mutation lease remain.
- The three prerequisite Nomad binaries were built once as Linux amd64 from
  exact core revision `909df5f3fea8` and uploaded into the previously empty
  `gs://monad-code-fc-env-pipeline` bucket.

| Object | Generation | Size | GCS MD5 |
|---|---:|---:|---|
| `orchestrator` | `1785232442089508` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `orchestrator.909df5f3fea8` | `1785232866521885` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `template-manager` | `1785232472561265` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `template-manager.909df5f3fea8` | `1785232895401036` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `clean-nfs-cache` | `1785232517773444` | `50557937` | `zLUBMUIvTYcm+aDVzj1ZFA==` |
| `clean-nfs-cache.909df5f3fea8` | `1785232911212631` | `50557937` | `zLUBMUIvTYcm+aDVzj1ZFA==` |

Earlier nine-character revision aliases remain as historical uploads and are
not release inputs.

Local SHA-256:

- `orchestrator`: `16401bbd8a34a697c79f0ded917c1ebc9493c441ed4a10a8bd01e34effb7bd0d`
- `clean-nfs-cache`: `9ff92543bc424576db4ec638fa3c76bef6a5c0a9f4ba32994712ccf0fd709723`

Do not replace the canonical GCS objects between publishing a reviewed
workload plan and consuming that plan.

## Latest guarded-plan result

The first full `workload-plan` made no infrastructure changes and released
both locks. It exposed these first-deploy prerequisites:

1. the three GCS binaries above were absent;
2. `e2b-cloudflare-api-token` had no enabled version;
3. the template-manager count lookup contacted Nomad before Nomad existed;
4. a full one-shot apply could race Nomad jobs against cluster readiness.

The GCS prerequisite is now complete. The guarded release change now:

- skips the unnecessary live Nomad count lookup for the fixed one-worker
  topology while retaining fail-closed lookup for autoscaled deployments;
- splits the rollout into a reviewed cluster-only saved plan/apply, a bounded
  Nomad health gate, and the reviewed full workload saved plan/apply;
- binds canonical and revision GCS object generations and checksums into plan
  provenance, while Nomad jobs fetch only preconditioned immutable revision
  aliases using go-getter-supported generation fragments; and
- admits the full phase only when its plan contains no mutating cluster compute
  actions, avoiding double-counting the already-created fleet while preserving
  peak-minus-base operational quota reserve.

Its offline release suite, Terraform validation, live read-only artifact
attestation, and live bootstrap quota check pass. Merge that guarded release
change before running the resume sequence below.

## Manual prerequisite

Create a Cloudflare API token restricted to the `monad0.net` zone with
`Zone:Read` and `DNS:Edit`, then enter it directly into the operator terminal:

```bash
gcloud secrets versions add e2b-cloudflare-api-token \
  --project=monad-code --data-file=-
```

Do not paste the token into chat, source control, plan output, or this file.

## Resume boundary

No E2B workload VMs, Cloud SQL instance, or public E2B service have been
created yet. After the bootstrap release gate is merged and the Cloudflare
secret has an enabled version:

1. publish and review the saved `module.cluster` bootstrap plan;
2. apply only those reviewed bytes with the exact canary confirmation;
3. wait for stable MIGs, certificate/DNS, and a healthy three-server Nomad
   control plane;
4. publish and review the full workload plan;
5. apply only those reviewed bytes;
6. run the SDK create/execute/pause/resume/fork canary;
7. only then connect the TAMS provider seam.

`local_docker` remains development/emergency-recovery infrastructure and is
not the beta execution target.
