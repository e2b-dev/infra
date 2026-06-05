#!/usr/bin/env node
/**
 * dns-prewarm.js — resolves common DNS hostnames and caches results as JSON.
 *
 * This script runs during Layer 1 snapshot creation. The output file is
 * placed in the snapshot rootfs so that on resume, envd or the orchestrator
 * can load pre-resolved IPs and skip DNS lookups (~5-20ms each) during the
 * cold start critical path.
 *
 * Usage: node dns-prewarm.js [output.json]
 */

const dns = require('dns');
const fs = require('fs');
const path = require('path');

const DEFAULT_OUTPUT = '/etc/e2b/dns-cache.json';

const HOSTNAMES = [
  'api.openai.com',
  'api.anthropic.com',
  'api.deepseek.com',
  'api.mistral.ai',
  'api.perplexity.ai',
  'api.x.ai',
  'generativelanguage.googleapis.com',
  'api.openclaw.dev',
  'registry.npmjs.org',
  'github.com',
  'api.github.com',
];

async function resolveHostname(hostname) {
  return new Promise((resolve) => {
    dns.resolve4(hostname, (err, addresses) => {
      if (err) {
        dns.resolve6(hostname, (err6, addresses6) => {
          if (err6) {
            console.error(`DNS: ${hostname} → FAILED (${err.code}/${err6.code})`);
            resolve(null);
            return;
          }
          console.error(`DNS: ${hostname} → ${addresses6.join(', ')} (IPv6)`);
          resolve({ hostname, ips: addresses6, ttl: 300 });
        });
        return;
      }
      console.error(`DNS: ${hostname} → ${addresses.join(', ')}`);
      resolve({ hostname, ips: addresses, ttl: 300 });
    });
  });
}

async function main() {
  const outputPath = process.argv[2] || DEFAULT_OUTPUT;

  console.error('DNS pre-warming for Layer 1 snapshot...');

  const results = [];
  for (const hostname of HOSTNAMES) {
    const entry = await resolveHostname(hostname);
    if (entry) {
      results.push(entry);
    }
  }

  const dir = path.dirname(outputPath);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }

  fs.writeFileSync(outputPath, JSON.stringify(results, null, 2));
  console.error(`DNS cache written to ${outputPath} (${results.length} entries)`);
}

main().catch((err) => {
  console.error('DNS pre-warming failed:', err);
  process.exit(1);
});
