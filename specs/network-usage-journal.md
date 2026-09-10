# SUP-898: host network observation journal

This slice preserves Firecracker per-flush byte deltas on the host. It is disabled
unless `NETWORK_USAGE_JOURNAL_DIR` is configured. It does not publish prices, charge
wallets, establish billable network semantics, or claim complete session coverage.
Parent collector work: [SUP-897](https://linear.app/superintelligent-group/issue/SUP-897).

## Contract and evidence

The small transition model follows the invariant-preserving kernel approach in
[dafny-replay](https://github.com/metareflection/dafny-replay). We retain Go for host
I/O and use an executable Dafny oracle to test the correspondence. The Go program
is **not** compiled from Dafny and has no universal refinement proof.

| ID | Required behavior | Formal obligation | Executable evidence |
| --- | --- | --- | --- |
| NJ1 | Every accepted sample adds its per-flush TX/RX deltas exactly once; equal adjacent samples both count. Successful appends advance sequence by one. | [`DurableConservation`, `Step`](network-usage-journal.dfy) | [`TestConservationAndExactJSON`, `TestDafnyReplay`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go) |
| NJ2 | Full write and successful file sync precede acknowledgment and in-memory publication. Short write/write/sync errors latch; later calls cannot append a misleading tail. | [`NoPublicationOnFailure`, `Step`](network-usage-journal.dfy), assuming a truthful persistence outcome | [`TestPersistenceFailuresLatch`, `TestDafnyReplay`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go), injected short write, disk error, sync error |
| NJ3 | Totals and sequence never wrap uint64. Counter overflow preserves both totals and records an invalid gap. Sequence exhaustion stops appending. | [`Inv`, `OverflowNeverWraps`, `Replay`](network-usage-journal.dfy) | [`TestOverflowAndGapsAreSticky`, `TestDafnyReplay`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go) |
| NJ4 | Known gaps and backward observation time irreversibly invalidate the incarnation. A later valid frame cannot repair missing evidence. | [`Step`, `Replay`](network-usage-journal.dfy) preserve `!valid` | [`TestClockRegressionAndClose`, `TestOverflowAndGapsAreSticky`, `TestDafnyReplay`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go) |
| NJ5 | A new Open has a new random incarnation, starts at sequence zero, and uses an exclusive host-owned file independent of sandbox-name path characters. | Outside arithmetic model: OS and entropy assumptions | [`TestDurableOpenAndIdentity`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go) checks distinct files, identities, permissions, start records, disabled configuration, and missing directory rejection |
| NJ6 | Concurrent reader/flusher calls serialize; every acknowledged sample contributes once. Close is idempotent and blocks subsequent writes. Every row has `complete=false`; EOF is never terminal billing evidence. | [`Step`](network-usage-journal.dfy) models closed state; mutex and complete flag require runtime tests | [`TestConcurrentObservers`, `TestClockRegressionAndClose`, `TestDafnyReplay`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go), run with Go race detector |
| NJ7 | Absent/null counters are unknown, not zero. Invalid/negative/fractional/overflowing JSON cannot become evidence. uint64 values remain exact beyond JavaScript's integer precision. | Parser and serialization outside arithmetic model | [`TestNetworkMetricsEvidencePresence`](../packages/orchestrator/pkg/sandbox/fc/network_metrics_test.go), [`TestConservationAndExactJSON`](../packages/orchestrator/pkg/sandbox/networkusage/journal_test.go) |

Implementation: [`Journal`](../packages/orchestrator/pkg/sandbox/networkusage/journal.go),
[`startMetricsReader`](../packages/orchestrator/pkg/sandbox/fc/fc_metrics.go),
[`Create` / `Resume`](../packages/orchestrator/pkg/sandbox/fc/process.go).
The reader records malformed frames, missing counters, FIFO/scanner failures, and
flush-request errors as gaps. A configured journal that cannot initialize fails
Create/Resume and stops that Firecracker process. Later persistence failure is
logged and latched; it does not silently become a successful billing observation.

## Reproduce proof and tests

Use Dafny **4.11.0**, its C# target with a compatible .NET SDK, Node, and Linux Go
**1.26.5**. From the repository root:

```sh
DAFNY=/path/to/dafny node scripts/local-proof/network-journal-model.mjs
cd packages/orchestrator
GOWORK=off go test -race -count=1 ./pkg/sandbox/networkusage ./pkg/sandbox/fc ./pkg/cfg
```

The first command verifies the model, compiles and executes its `Main`, then
compares its output to [`testdata/model.csv`](../packages/orchestrator/pkg/sandbox/networkusage/testdata/model.csv).
`TestDafnyReplay` reads **both actions and expected states** from that fixture and
executes those actions against Go. There is no handwritten Go oracle repeating
the implementation's arithmetic. A changed model with stale fixtures fails the
first command. Deliberate updates use `--write`, followed by review and Go tests.
These checks are required for this slice in addition to the repository's existing
local proof lanes; the existing general verifier does not automatically run them.

The proof establishes the model's claims for all permitted states/actions and
finite replays. The 22 fixture transitions check representative boundary behavior
of Go; they do not prove universal Go/Dafny equivalence. Fault injection tests the
I/O adapter, while Linux filesystem and race tests cover actual runtime boundaries.
Neither tests nor this proof establish drive firmware honesty, machine-loss
durability, a trusted wall clock, or the accuracy of Firecracker's source counters.

Local verification on 2026-09-10: Dafny 4.11.0 reported **12 verified, 0 errors**;
the executable oracle matched **22 transitions**. Linux Go 1.26.5 race tests passed
for `networkusage`, `fc`, and `cfg`.

## Operational and performance boundary

Configure a pre-existing operator-owned directory that is never guest-mounted.
Creation uses mode 0600, exclusive random filenames, initial file sync, and parent
directory sync. Each record uses decimal strings for uint64 JSON fields. Treat
`(sandboxId, incarnation, sequence)` as the future delivery identity; guest IDs
alone are insufficient across resume/restart. A repeated raw Firecracker frame
has no source identity and cannot be deduplicated safely by comparing quantities.

The journal performs one append and sync per frame in the existing reader, with
constant in-memory state. It adds no per-sample processes, goroutines, or remote
round trips. The normal explicit flush interval is five seconds; additional
Firecracker flushes can increase the rate. Benchmark with:

```sh
GOWORK=off go test -run '^$' -bench=BenchmarkDurableObserve -benchtime=2s -count=3 ./pkg/sandbox/networkusage
```

File sync blocks the metrics reader, so production activation requires storage
latency and fleet-load measurements, including impact on balloon metrics. Do not
infer fleet throughput from a single local append benchmark. Storage use grows
with observations; operator-owned retention, disk budgets, remote durable delivery,
idempotent consumption, recovery of partial tails, and a final quiescent flush
protocol remain SUP-897 follow-up work. Do not enable this experimental producer
fleet-wide before those gates. Every record remains `complete=false`, including
the `closed` record with `terminal_coverage_unverified`. A synced prefix is local
observation evidence and cannot authorize pricing publication by itself.

Local benchmark on 2026-09-10, Docker Desktop Linux amd64, Go 1.26.5, Intel
i7-10700KF, Linux named volume, three two-second trials: **1.169 / 1.345 / 1.396 ms
per synced observation**, 464-465 B and three allocations per operation. These
are trial means, not latency percentiles. A separate run on the container writable
layer during compilation contention measured 4.930-7.273 ms/op. This variation is
why the local result is an overhead measurement, not a fleet latency guarantee.
