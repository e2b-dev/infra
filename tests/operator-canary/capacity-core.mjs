export const CAPACITY_CONFIRMATION = "RUN LIVE MONAD F1 CAPACITY BENCHMARK";
export const MAX_BENCHMARK_WORKCELLS = 5;

export function parseMaximumWorkcells(value) {
  const parsed = Number.parseInt(value ?? String(MAX_BENCHMARK_WORKCELLS), 10);
  if (
    !Number.isSafeInteger(parsed) ||
    parsed < 1 ||
    parsed > MAX_BENCHMARK_WORKCELLS
  ) {
    throw new Error(
      `E2B_CAPACITY_MAX_WORKCELLS must be between 1 and ${MAX_BENCHMARK_WORKCELLS}`,
    );
  }
  return parsed;
}

export function assertCapacityConfirmation(value) {
  if (value !== CAPACITY_CONFIRMATION) {
    throw new Error(
      `E2B_CAPACITY_CONFIRM must equal ${JSON.stringify(CAPACITY_CONFIRMATION)}`,
    );
  }
}

export function assertWorkerProfile(instance, expectedMachineType) {
  const machineType = instance.machineType?.split("/").at(-1);
  const zone = instance.zone?.split("/").at(-1);
  if (instance.status !== "RUNNING") {
    throw new Error(`benchmark worker is ${instance.status ?? "not running"}`);
  }
  if (machineType !== expectedMachineType) {
    throw new Error(
      `benchmark worker uses ${machineType ?? "an unknown profile"}; expected ${expectedMachineType}`,
    );
  }
  return {
    name: instance.name,
    machine_type: machineType,
    zone,
    status: instance.status,
  };
}

export function assertTemplateResources(template, maximums = {}) {
  const readyBuilds =
    template.builds?.filter((build) => build.status === "ready") ?? [];
  if (readyBuilds.length !== 1) {
    throw new Error(
      `benchmark template must have exactly one ready build; found ${readyBuilds.length}`,
    );
  }

  const [build] = readyBuilds;
  const limits = {
    cpuCount: maximums.cpuCount ?? 2,
    memoryMB: maximums.memoryMB ?? 2048,
    diskSizeMB: maximums.diskSizeMB ?? 1024,
  };
  for (const [name, maximum] of Object.entries(limits)) {
    if (
      !Number.isFinite(build[name]) ||
      build[name] <= 0 ||
      build[name] > maximum
    ) {
      throw new Error(
        `benchmark template ${name}=${build[name]} exceeds the ${maximum} bound`,
      );
    }
  }

  return {
    template_id: template.templateID,
    build_id: build.buildID,
    cpu_count: build.cpuCount,
    memory_mb: build.memoryMB,
    disk_size_mb: build.diskSizeMB,
  };
}

export function percentile(values, percentileValue) {
  if (!Array.isArray(values) || values.length === 0) {
    throw new Error("percentile requires at least one value");
  }
  const sorted = [...values].sort((left, right) => left - right);
  const index = Math.ceil((percentileValue / 100) * sorted.length) - 1;
  return sorted[Math.max(0, Math.min(index, sorted.length - 1))];
}

export function filteredOperatorEnvironment(environment) {
  const forbidden = new Set([
    "E2B_ACCESS_TOKEN",
    "E2B_API_KEY",
    "POSTGRES_CONNECTION_STRING",
  ]);
  return Object.fromEntries(
    Object.entries(environment).filter(([name]) => !forbidden.has(name)),
  );
}

export function isAdmissionBoundaryError(error) {
  const status =
    error?.status ?? error?.statusCode ?? error?.response?.status ?? null;
  const message = error instanceof Error ? error.message : String(error);
  return (
    (status === 409 || status === 429 || status === 503) &&
    /capacity|insufficient|maximum number|no nodes|not enough|placement/i.test(
      message,
    )
  );
}
