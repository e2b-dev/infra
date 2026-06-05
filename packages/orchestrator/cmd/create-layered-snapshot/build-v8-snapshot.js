#!/usr/bin/env node

/**
 * build-v8-snapshot.js — V8 Startup Snapshot Builder for OpenClaw Agent Sandboxes
 *
 * This script is executed OUTSIDE the Firecracker VM during Layer 1 snapshot
 * creation to produce a V8 startup snapshot blob. The blob captures:
 *   1. All built-in modules parsed and compiled to bytecode
 *   2. OpenClaw module tree loaded and instantiated
 *   3. Pre-built object shapes and inline caches
 *
 * Usage (on host):
 *   node --snapshot-blob=openclaw-snapshot.blob build-v8-snapshot.js
 */

const { performance } = require('perf_hooks');
const v8 = require('v8');

// ── Configuration ──────────────────────────────────────────────────────

const outputPath = process.env.V8_SNAPSHOT_OUTPUT || '/tmp/openclaw-snapshot.blob';

// ── Helpers ────────────────────────────────────────────────────────────

function log(msg) {
  const ts = new Date().toISOString();
  process.stderr.write(`[v8-snapshot ${ts}] ${msg}\n`);
}

// ── Snapshot Construction ──────────────────────────────────────────────

v8.startupSnapshot.addDeserializeCallback(() => {
  log('V8 startup snapshot deserialized — modules pre-loaded');
});

const preloadedModules = {};

function preloadModules() {
  const start = performance.now();
  log('Preloading modules for V8 snapshot...');

  const modules = [
    '@openclaw/gateway',
    '@openclaw/agent',
    '@openclaw/skills',
    '@openclaw/memory',
    '@openclaw/channel',
  ];

  for (const name of modules) {
    try {
      preloadedModules[name] = require(name);
      log(`  loaded: ${name}`);
    } catch (e) {
      log(`  skipped (not found): ${name} — ${e.message}`);
    }
  }

  const elapsed = (performance.now() - start).toFixed(0);
  log(`Modules preloaded in ${elapsed}ms`);
}

v8.startupSnapshot.addSerializeCallback(() => {
  log('V8 snapshot serialization callback invoked');
});

// ── Main ────────────────────────────────────────────────────────────────

try {
  preloadModules();

  if (typeof global.gc === 'function') {
    global.gc();
  }

  log(`V8 snapshot build complete. Output will be written to: ${outputPath}`);
  log('(Snapshot is written by Node.js after this script exits)');
} catch (err) {
  log(`FATAL: ${err.stack || err.message}`);
  process.exit(1);
}
