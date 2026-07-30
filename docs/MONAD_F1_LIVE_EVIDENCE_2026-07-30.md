# Monad GCP F1 live evidence — 2026-07-30

This is the operator evidence for G1/F1 against infra revision
`b20d6a57ca1ee2952d8d502cf52e1e228fa82493`, GCP project `monad-code`,
region `us-east4`, and worker zone `us-east4-c`. It distinguishes observations
from supported limits. No customer workload or credential was used.

## Billing guard

`billingbudgets.googleapis.com` and `cloudbilling.googleapis.com` are enabled.
The project-filtered `Monad F1 monthly budget alert` is active at USD 5,000 per
calendar month, including all credits, with current-spend thresholds at 50%,
90%, and 100% plus a forecast threshold at 75%.

The alert is notification-only. It neither caps spend nor scales resources.

## Runtime identity proof

The running fleet contains six GCE instances: three Nomad servers, one API
node, one build worker, and one Firecracker client worker. Every instance and
every corresponding instance template attaches
`e2b-infra-instances@monad-code.iam.gserviceaccount.com`.

### Live observations

| Surface                  | Observation                                                                                                                                                                                                                                                                                            |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Service-account keys     | The default Compute, image-builder, and runtime service accounts each have `0` user-managed keys. Their two system-managed keys are Google-owned signing infrastructure, not downloadable static credentials.                                                                                          |
| Metadata ADC             | All six running instances returned the attached runtime service-account email and a valid short-lived Bearer token from the metadata server. No token value was printed or retained.                                                                                                                   |
| Host files and processes | Across 10,853 inspected files in the runtime/config roots on the six hosts, there were zero Google service-account JSON files. Running process environments contained zero former key-shaped variable names.                                                                                               |
| Docker                   | API, build, and client hosts use `docker-credential-gcr` for `us-east4-docker.pkg.dev`, with zero static Docker `auths` entries. Server nodes do not run a Docker credential configuration.                                                                                                            |
| Live cloud calls         | From both the API and Firecracker client hosts, metadata ADC successfully listed the template bucket, called IAM Credentials `signBlob`, obtained a registry credential, and read the pinned Artifact Registry manifest. Every response was HTTP 200; only presence/status was recorded.               |
| Nomad jobs               | All 11 live job specifications (`api`, `client-proxy`, `docker-reverse-proxy`, `ingress`, `logs-collector`, `loki`, `orchestrator-dev`, both OTel collectors, `redis`, and `template-manager`) contain zero former key-shaped environment/string seams, `private_key_id` objects, or private-key PEMs. |
| Terraform state          | All 45 retained `.tfstate` generations contain zero `google_service_account_key` resources, zero `google_storage_hmac_key` resources, zero `private_key_id` objects, and zero former key-shaped environment strings.                                                                                   |
| Audit logs               | The retained log window contains zero create, upload, delete, disable, or enable service-account-key audit calls. Searches for the former credential environment names and private-key PEM markers returned zero.                                                                                      |
| Runtime artifacts        | Five pinned core OCI images, all current setup scripts, 13 template metadata objects, both Cloud Build source archives, and the four pinned Firecracker pipeline binaries were inspected without printing values. No credential payload or service-account JSON was present.                           |

The `private_key_id` log search returned 65 entries. Sixty-three are
`syslog`/Ops Agent copies of the operator's literal audit command, and two are
Compute address audit records whose request text also carried the scanner
literal. None is an IAM key lifecycle event.

Compiled ADC-capable Go binaries contain the SDK field-name literals
`private_key_id` and `GOOGLE_APPLICATION_CREDENTIALS`. The Docker reverse
proxy and three Firecracker pipeline binaries each contain one such marker,
but contain neither the paired service-account JSON type nor a PEM. Their OCI
configuration also contains no credential environment variable. These are
executable code paths for ADC compatibility, not embedded credentials.

Ten recent Terraform state generations contain two PEM strings:
`tls_private_key.volume_token.private_key_pem` and
`private_key_pem_pkcs8`. This is the expected ED25519 signer for E2B volume
content tokens. It is not a Google credential and is not used to authenticate
to GCP. It remains sensitive Terraform state and is disclosed here so that a
generic “no private keys” claim is not made.

ClickHouse is intentionally at MIG target size zero. Its instance template
attaches the same runtime service account, the live backup bucket grants that
identity `roles/storage.objectUser`, and its setup/job artifacts are free of
Google key material. That is deploy-time evidence, not a claim that a live
ClickHouse backup ran during this checkpoint.

## Firecracker worker benchmark

The bounded operator run
`20260730t034842724z-4a6a2359` used the private immutable template
`z007am7rtk9lcfguioff`, build
`8f05a81c-34e8-4da5-b87d-ee29fc4bd6cb`:

- 2 vCPU;
- 2,048 MiB RAM;
- 1,012 MiB disk;
- outbound internet denied; and
- one synthetic canary team with an initially empty inventory.

The worker was `e2b-orch-client-44v2`, profile `n1-standard-8` (8 vCPU,
30,074 MiB RAM), with 10,104 two-MiB hugepages reserved for Firecracker and a
375-GiB local workcell disk. Each level ran two CPU burners and a touched
512-MiB allocation per workcell, then concurrent 64-MiB fsynced disk writes,
4-MiB proxied network responses, a CPU probe, and command-latency probes.

| Active workcells | Peak worker CPU | Command p95 | Slowest CPU probe | Slowest fsync | Slowest proxy stream |  Swap |
| ---------------: | --------------: | ----------: | ----------------: | ------------: | -------------------: | ----: |
|                1 |           40.7% |      256 ms |            1.26 s |     346 MiB/s |           1.59 MiB/s | 0 MiB |
|                2 |           76.2% |      252 ms |            1.31 s |     338 MiB/s |           1.62 MiB/s | 0 MiB |
|                3 |           86.1% |      260 ms |            2.20 s |     279 MiB/s |           1.91 MiB/s | 0 MiB |
|                4 |            100% |      916 ms |            2.24 s |     215 MiB/s |           1.72 MiB/s | 0 MiB |
|                5 |            100% |      395 ms |            3.11 s |     184 MiB/s |           1.51 MiB/s | 0 MiB |

Memory, hugepages, local disk, and network remained available through the
bounded fifth workcell. CPU is the first limiting resource. Four workcells
consume all worker CPU and exhibit a 3.6x command-latency outlier versus the
one-workcell level. Five workcells are CPU-oversubscribed and roughly double
the CPU-probe time.

Cleanup deleted all five workcells. The final API inventory contained zero
running or paused sandboxes, Firecracker process count returned to zero,
hugepages free returned from 8,227 to all 10,104, swap remained unused, and
the local workcell disk returned to its baseline free-space band.

## Supported F1 envelope

These nouns are not interchangeable:

| Quantity         | F1 meaning                                                                                                | Supported envelope                                                                                                                                             |
| ---------------- | --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Durable sessions | Control-plane records and paused snapshots. They do not consume a running Firecracker slot until resumed. | No numeric durability/SLO ceiling was established by this worker benchmark. Resumes must enter the active-workcell admission envelope.                         |
| Active workcells | Running Firecracker microVMs executing user work.                                                         | Plan at **2 per ready `n1-standard-8` worker**. Enforce a hard placement cap of **3 per worker**. Four or more on one worker is unsupported.                   |
| Worker hosts     | GCE client-pool VMs that run Firecracker workcells.                                                       | Current live count: **1**, with no GCE autoscaler. Current quota-safe reviewed ceiling: **2 total workers**, yielding 4 planned / 6 hard-cap active workcells. |
| Build workers    | GCE workers used for template construction, not user workcells.                                           | Current live count: **1**. Do not include it in active-workcell capacity.                                                                                      |
| Fixed nodes      | Nomad/control/API/data-plane hosts independent of active workcell count.                                  | Current live count: **4** control/API nodes (3 server + 1 API); ClickHouse and Loki are zero-sized. They are not worker capacity.                              |

The steady planning factor is deliberately lower than the measured placement
cap. At two fully pressured workcells the worker reached 76% CPU, which is
already above the intended 70% autoscaler target. A future autoscaler should
therefore aim for:

```text
ready worker hosts = max(1, ceil(active workcells / 2))
placement admission: active workcells on one worker <= 3
```

Live quota headroom at the checkpoint was 174 regional vCPU, 10 regional
in-use addresses, 26 instances, 38 global vCPU, and 240 GiB regional SSD.
Each additional worker consumes 8 vCPU, one address, one instance, and 100 GiB
SSD. Retaining one-worker replacement reserve makes SSD the current binding
limit and permits two total worker hosts. If the regional SSD quota is raised
from 500 to 2,500 GiB, the existing global vCPU quota permits four total
workers while retaining one 8-vCPU replacement reserve: 8 planned / 12
hard-cap active workcells.

This document does not authorize a direct MIG resize. Enabling autoscaling or
raising the worker ceiling requires the repository's saved-plan review,
quota/topology guards, mutation lease, and post-apply readiness checks.
