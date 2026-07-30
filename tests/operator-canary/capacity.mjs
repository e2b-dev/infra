import { execFile as execFileCallback } from "node:child_process";
import { randomUUID } from "node:crypto";
import { promisify } from "node:util";
import { Sandbox } from "e2b";
import {
  assertCapacityConfirmation,
  assertTemplateResources,
  assertWorkerProfile,
  filteredOperatorEnvironment,
  isAdmissionBoundaryError,
  parseMaximumWorkcells,
  percentile,
} from "./capacity-core.mjs";
import {
  collectPaginator,
  normalizeApiUrl,
  requiredEnv,
  safeErrorMessage,
} from "./lifecycle-core.mjs";

const execFile = promisify(execFileCallback);
const environment = process.env;
const apiKey = requiredEnv(environment, "E2B_API_KEY");
const apiUrl = normalizeApiUrl(requiredEnv(environment, "E2B_API_URL"));
const domain = requiredEnv(environment, "E2B_DOMAIN");
const templateName = requiredEnv(environment, "E2B_TEMPLATE");
const project = environment.E2B_CAPACITY_GCP_PROJECT?.trim() || "monad-code";
const zone = environment.E2B_CAPACITY_GCP_ZONE?.trim() || "us-east4-c";
const worker =
  environment.E2B_CAPACITY_WORKER?.trim() || "e2b-orch-client-44v2";
const expectedMachineType =
  environment.E2B_CAPACITY_MACHINE_TYPE?.trim() || "n1-standard-8";
const maximumWorkcells = parseMaximumWorkcells(
  environment.E2B_CAPACITY_MAX_WORKCELLS,
);
assertCapacityConfirmation(environment.E2B_CAPACITY_CONFIRM);

const requestTimeoutMs = 10 * 60 * 1000;
const sandboxTimeoutMs = 15 * 60 * 1000;
const runId = `${new Date().toISOString().replaceAll(/[-:.]/g, "").toLowerCase()}-${randomUUID().slice(0, 8)}`;
const connection = {
  apiKey,
  apiUrl,
  domain,
  requestTimeoutMs,
};
const createOptions = {
  ...connection,
  timeoutMs: sandboxTimeoutMs,
  allowInternetAccess: false,
  network: {
    denyOut: ["0.0.0.0/0"],
    allowPublicTraffic: false,
  },
  lifecycle: {
    onTimeout: "pause",
    autoResume: false,
  },
  metadata: {
    "monad.operator.capacity-benchmark": runId,
    "monad.operator.synthetic": "true",
  },
};
const trackedSandboxes = new Map();
const pressurePidPath = `/tmp/monad-capacity-pressure-${runId}.pids`;
const gcloudEnvironment = filteredOperatorEnvironment(environment);
const evidence = {
  run_id: runId,
  started_at: new Date().toISOString(),
  api_origin: new URL(apiUrl).origin,
  requested_maximum_workcells: maximumWorkcells,
  levels: [],
};

const hostProbe = Buffer.from(
  String.raw`
import glob, json, os, time
def cpu():
    values = [int(value) for value in open("/proc/stat").readline().split()[1:]]
    return sum(values), values[3] + values[4]
total1, idle1 = cpu()
time.sleep(1)
total2, idle2 = cpu()
memory = {}
for line in open("/proc/meminfo"):
    key, value, *_ = line.split()
    memory[key.rstrip(":")] = int(value)
processes = []
for path in glob.glob("/proc/[0-9]*/comm"):
    try:
        if open(path).read().strip() != "firecracker":
            continue
        pid = path.split("/")[2]
        status = {}
        for line in open(f"/proc/{pid}/status"):
            if line.startswith(("VmRSS:", "Threads:")):
                key, value, *_ = line.split()
                status[key.rstrip(":")] = int(value)
        processes.append(status)
    except (FileNotFoundError, PermissionError, ProcessLookupError):
        pass
network = {}
for line in open("/proc/net/dev").read().splitlines()[2:]:
    name, data = line.split(":", 1)
    if name.strip() == "ens5":
        values = [int(value) for value in data.split()]
        network = {"rx_bytes": values[0], "tx_bytes": values[8]}
def disk(path):
    stats = os.statvfs(path)
    return {
        "free_mb": stats.f_bavail * stats.f_frsize // 1048576,
        "total_mb": stats.f_blocks * stats.f_frsize // 1048576,
    }
print(json.dumps({
    "cpu_utilization_percent": round(100 * (1 - (idle2 - idle1) / (total2 - total1)), 2),
    "load_1m": os.getloadavg()[0],
    "memory_available_mb": memory["MemAvailable"] // 1024,
    "swap_used_mb": (memory["SwapTotal"] - memory["SwapFree"]) // 1024,
    "hugepages_total": memory.get("HugePages_Total", 0),
    "hugepages_free": memory.get("HugePages_Free", 0),
    "firecracker_processes": len(processes),
    "firecracker_rss_mb": sum(row.get("VmRSS", 0) for row in processes) // 1024,
    "root_disk": disk("/"),
    "workcell_disk": disk("/orchestrator"),
    "network": network,
}, sort_keys=True))
`,
).toString("base64");

function nowMs() {
  return Number(process.hrtime.bigint()) / 1_000_000;
}

async function apiJson(path) {
  const response = await fetch(`${apiUrl}${path}`, {
    headers: { "X-API-Key": apiKey },
    signal: AbortSignal.timeout(requestTimeoutMs),
  });
  if (!response.ok) {
    throw new Error(`template preflight returned HTTP ${response.status}`);
  }
  return response.json();
}

async function listSandboxes() {
  return collectPaginator(
    Sandbox.list({
      ...connection,
      limit: 100,
      query: { state: ["running", "paused"] },
    }),
    connection,
  );
}

async function hostSample() {
  const command = `printf '%s' '${hostProbe}' | base64 -d | sudo python3`;
  const { stdout } = await execFile(
    "gcloud",
    [
      "compute",
      "ssh",
      worker,
      `--project=${project}`,
      `--zone=${zone}`,
      `--command=${command}`,
      "--quiet",
    ],
    { env: gcloudEnvironment, maxBuffer: 1024 * 1024 },
  );
  return JSON.parse(stdout.trim());
}

async function inspectWorker() {
  const { stdout } = await execFile(
    "gcloud",
    [
      "compute",
      "instances",
      "describe",
      worker,
      `--project=${project}`,
      `--zone=${zone}`,
      "--format=json(name,machineType,status,zone)",
    ],
    { env: gcloudEnvironment },
  );
  return assertWorkerProfile(JSON.parse(stdout), expectedMachineType);
}

async function runChecked(sandbox, command, options = {}) {
  const started = nowMs();
  const result = await sandbox.commands.run(command, {
    timeoutMs: options.timeoutMs ?? 120_000,
    requestTimeoutMs,
  });
  const elapsedMs = nowMs() - started;
  if (result.exitCode !== 0) {
    throw new Error(`sandbox command failed with exit code ${result.exitCode}`);
  }
  return { result, elapsedMs };
}

async function startPressure(sandbox) {
  const command = String.raw`
( timeout 90s sha256sum /dev/zero >/dev/null 2>&1 &
  cpu_pid_one="$!"
  timeout 90s sha256sum /dev/zero >/dev/null 2>&1 &
  cpu_pid_two="$!"
  timeout 90s python3 -c 'import time; value=bytearray(512*1024*1024); value[::4096]=b"x"*(len(value)//4096); time.sleep(90)' >/dev/null 2>&1 &
  memory_pid="$!"
  printf '%s\n' "$cpu_pid_one" "$cpu_pid_two" "$memory_pid" > ${pressurePidPath}
  wait
) >/dev/null 2>&1 &
printf '%s' "$!"
`;
  const { result } = await runChecked(sandbox, command);
  if (!/^[0-9]+$/.test(result.stdout.trim())) {
    throw new Error("pressure process did not return a PID");
  }
}

async function stopPressure(sandbox) {
  await runChecked(
    sandbox,
    `if test -f ${pressurePidPath}; then while IFS= read -r pid; do kill "$pid" 2>/dev/null || true; done < ${pressurePidPath}; unlink ${pressurePidPath} 2>/dev/null || true; fi; printf stopped`,
    { timeoutMs: 30_000 },
  );
}

async function probeLevel(sandboxes) {
  const before = await hostSample();
  await Promise.all(sandboxes.map((sandbox) => startPressure(sandbox)));
  await new Promise((resolve) => setTimeout(resolve, 3_000));
  const underCpuMemoryPressure = await hostSample();

  const latency = await Promise.all(
    sandboxes.map(async (sandbox) => {
      const { result, elapsedMs } = await runChecked(sandbox, "printf 'ready'");
      if (result.stdout !== "ready" || result.stderr !== "") {
        throw new Error("latency probe returned unexpected output");
      }
      return Math.round(elapsedMs * 100) / 100;
    }),
  );

  const cpuSeconds = await Promise.all(
    sandboxes.map(async (sandbox) => {
      const { result } = await runChecked(
        sandbox,
        "python3 -c 'import hashlib,time; data=b\"x\"*1048576; started=time.monotonic(); [hashlib.sha256(data).digest() for _ in range(256)]; print(time.monotonic()-started)'",
      );
      return Number.parseFloat(result.stdout);
    }),
  );

  const disk = await Promise.all(
    sandboxes.map(async (sandbox) => {
      const { result } = await runChecked(
        sandbox,
        'python3 -c \'import json,os,time; path="/tmp/monad-capacity-disk"; data=b"x"*1048576; started=time.monotonic(); handle=open(path,"wb"); [handle.write(data) for _ in range(64)]; handle.flush(); os.fsync(handle.fileno()); handle.close(); elapsed=time.monotonic()-started; os.unlink(path); print(json.dumps({"bytes":67108864,"seconds":elapsed}))\'',
      );
      const measurement = JSON.parse(result.stdout);
      return (
        Math.round(
          (measurement.bytes / 1_048_576 / measurement.seconds) * 100,
        ) / 100
      );
    }),
  );

  const network = await Promise.all(
    sandboxes.map(async (sandbox) => {
      const { result, elapsedMs } = await runChecked(
        sandbox,
        "head -c 4194304 /dev/zero | tr '\\0' x",
      );
      if (Buffer.byteLength(result.stdout) !== 4_194_304) {
        throw new Error("network probe returned an unexpected byte count");
      }
      return Math.round((4 / (elapsedMs / 1000)) * 100) / 100;
    }),
  );

  const afterIoPressure = await hostSample();
  await Promise.all(sandboxes.map((sandbox) => stopPressure(sandbox)));

  return {
    host_before: before,
    host_under_cpu_memory_pressure: underCpuMemoryPressure,
    host_after_disk_network_pressure: afterIoPressure,
    command_latency_ms: {
      values: latency,
      p50: percentile(latency, 50),
      p95: percentile(latency, 95),
    },
    cpu_probe_seconds: cpuSeconds,
    disk_fsync_mib_per_second: disk,
    proxied_network_mib_per_second: network,
  };
}

async function cleanup() {
  const failures = [];
  try {
    for (const sandbox of await listSandboxes()) {
      trackedSandboxes.set(sandbox.sandboxId, sandbox);
    }
  } catch (error) {
    failures.push(`inventory: ${safeErrorMessage(error, [apiKey])}`);
  }

  for (const sandboxId of trackedSandboxes.keys()) {
    try {
      await Sandbox.kill(sandboxId, connection);
    } catch (error) {
      failures.push(
        `sandbox ${sandboxId}: ${safeErrorMessage(error, [apiKey])}`,
      );
    }
  }

  try {
    let remaining = [];
    for (let attempt = 0; attempt < 30; attempt += 1) {
      remaining = await listSandboxes();
      if (remaining.length === 0) {
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 2_000));
    }
    if (remaining.length !== 0) {
      failures.push(`${remaining.length} sandbox(es) remain`);
    } else {
      trackedSandboxes.clear();
    }
  } catch (error) {
    failures.push(`final inventory: ${safeErrorMessage(error, [apiKey])}`);
  }

  if (failures.length > 0) {
    throw new Error(`cleanup incomplete: ${failures.join("; ")}`);
  }
}

let benchmarkError;
try {
  const initial = await listSandboxes();
  if (initial.length !== 0) {
    throw new Error(
      `synthetic benchmark team is not empty: found ${initial.length} sandbox(es)`,
    );
  }

  evidence.worker = await inspectWorker();
  const alias = await apiJson(
    `/templates/aliases/${encodeURIComponent(templateName)}`,
  );
  const template = await apiJson(
    `/templates/${encodeURIComponent(alias.templateID)}`,
  );
  evidence.template = assertTemplateResources(template);
  evidence.host_baseline = await hostSample();

  for (let target = 1; target <= maximumWorkcells; target += 1) {
    const createStarted = nowMs();
    let sandbox;
    try {
      sandbox = await Sandbox.create(templateName, createOptions);
    } catch (error) {
      if (target === 1 || !isAdmissionBoundaryError(error)) {
        throw error;
      }
      evidence.admission_boundary = {
        requested_workcells: target,
        active_workcells: trackedSandboxes.size,
        error: safeErrorMessage(error, [apiKey]),
      };
      break;
    }
    trackedSandboxes.set(sandbox.sandboxId, sandbox);
    const createLatencyMs = Math.round((nowMs() - createStarted) * 100) / 100;
    const level = await probeLevel([...trackedSandboxes.values()]);
    evidence.levels.push({
      active_workcells: target,
      create_latency_ms: createLatencyMs,
      ...level,
    });
    process.stderr.write(
      `capacity level ${target}/${maximumWorkcells} complete\n`,
    );
  }

  if (evidence.levels.length === 0) {
    throw new Error("benchmark did not complete the one-workcell baseline");
  }
} catch (error) {
  benchmarkError = error;
} finally {
  try {
    await cleanup();
    evidence.cleanup = {
      active_sandboxes: 0,
      confirmed_at: new Date().toISOString(),
    };
    evidence.host_after_cleanup = await hostSample();
  } catch (cleanupError) {
    benchmarkError = benchmarkError
      ? new AggregateError(
          [benchmarkError, cleanupError],
          "benchmark and cleanup both failed",
        )
      : cleanupError;
  }
}

evidence.finished_at = new Date().toISOString();
if (benchmarkError) {
  evidence.error = safeErrorMessage(benchmarkError, [apiKey]);
  process.stdout.write(`${JSON.stringify(evidence, null, 2)}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`${JSON.stringify(evidence, null, 2)}\n`);
}
