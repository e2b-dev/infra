import test from "node:test";
import assert from "node:assert/strict";
import {
  assertCapacityConfirmation,
  assertTemplateResources,
  assertWorkerProfile,
  CAPACITY_CONFIRMATION,
  filteredOperatorEnvironment,
  isAdmissionBoundaryError,
  parseMaximumWorkcells,
  percentile,
} from "./capacity-core.mjs";

test("capacity benchmark confirmation is exact", () => {
  assert.doesNotThrow(() => assertCapacityConfirmation(CAPACITY_CONFIRMATION));
  assert.throws(() => assertCapacityConfirmation("yes"), /must equal/);
});

test("capacity benchmark maximum is hard bounded", () => {
  assert.equal(parseMaximumWorkcells(undefined), 5);
  assert.equal(parseMaximumWorkcells("3"), 3);
  for (const value of ["0", "6", "nope"]) {
    assert.throws(() => parseMaximumWorkcells(value), /between 1 and 5/);
  }
});

test("worker profile must be running and exact", () => {
  assert.deepEqual(
    assertWorkerProfile(
      {
        name: "worker",
        status: "RUNNING",
        machineType: "zones/us-east4-c/machineTypes/n1-standard-8",
        zone: "projects/monad-code/zones/us-east4-c",
      },
      "n1-standard-8",
    ),
    {
      name: "worker",
      machine_type: "n1-standard-8",
      zone: "us-east4-c",
      status: "RUNNING",
    },
  );
  assert.throws(
    () =>
      assertWorkerProfile(
        { name: "worker", status: "RUNNING", machineType: "n1-standard-4" },
        "n1-standard-8",
      ),
    /expected n1-standard-8/,
  );
});

test("template resources are singular and bounded", () => {
  assert.deepEqual(
    assertTemplateResources({
      templateID: "template",
      builds: [
        {
          buildID: "build",
          status: "ready",
          cpuCount: 2,
          memoryMB: 2048,
          diskSizeMB: 1012,
        },
      ],
    }),
    {
      template_id: "template",
      build_id: "build",
      cpu_count: 2,
      memory_mb: 2048,
      disk_size_mb: 1012,
    },
  );
  assert.throws(
    () =>
      assertTemplateResources({
        templateID: "template",
        builds: [
          {
            buildID: "build",
            status: "ready",
            cpuCount: 4,
            memoryMB: 2048,
            diskSizeMB: 1012,
          },
        ],
      }),
    /cpuCount=4/,
  );
});

test("percentile uses the nearest-rank method", () => {
  assert.equal(percentile([40, 10, 30, 20], 50), 20);
  assert.equal(percentile([40, 10, 30, 20], 95), 40);
});

test("gcloud children do not inherit canary or database credentials", () => {
  assert.deepEqual(
    filteredOperatorEnvironment({
      PATH: "/usr/bin",
      E2B_API_KEY: "secret",
      E2B_ACCESS_TOKEN: "secret",
      POSTGRES_CONNECTION_STRING: "secret",
      SAFE: "retained",
    }),
    { PATH: "/usr/bin", SAFE: "retained" },
  );
});

test("only explicit capacity responses are admission boundaries", () => {
  assert.equal(
    isAdmissionBoundaryError(
      Object.assign(new Error("insufficient placement capacity"), {
        status: 503,
      }),
    ),
    true,
  );
  assert.equal(
    isAdmissionBoundaryError(
      Object.assign(new Error("upstream unavailable"), { status: 503 }),
    ),
    false,
  );
  assert.equal(
    isAdmissionBoundaryError(
      Object.assign(new Error("maximum number reached"), { status: 401 }),
    ),
    false,
  );
});
