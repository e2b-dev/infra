import { readFile } from 'node:fs/promises';
import { Template } from 'e2b';
import { prepareRuntimeAssets } from './prepare-assets.mjs';
import {
  normalizeApiUrl,
  requiredEnv,
  safeErrorMessage,
  validateTemplateRef,
} from './runtime-core.mjs';
import { createMonadRuntimeTemplate } from './template.mjs';

const environment = process.env;
const apiKey = requiredEnv(environment, 'E2B_API_KEY');
const apiUrl = normalizeApiUrl(requiredEnv(environment, 'E2B_API_URL'));
const domain = requiredEnv(environment, 'E2B_DOMAIN');
const templateRef = validateTemplateRef(
  requiredEnv(environment, 'E2B_TEMPLATE_REF'),
);
const packageMetadata = JSON.parse(
  await readFile(new URL('./node_modules/e2b/package.json', import.meta.url)),
);
if (packageMetadata.version !== '2.21.0') {
  throw new Error(
    `e2b SDK ${packageMetadata.version} is installed; expected 2.21.0`,
  );
}

const connection = {
  apiKey,
  apiUrl,
  domain,
  requestTimeoutMs: 10 * 60 * 1000,
};

try {
  if (await Template.exists(templateRef, connection)) {
    throw new Error(
      `template reference ${templateRef} already exists; choose a new immutable tag`,
    );
  }
  await prepareRuntimeAssets(environment);
  const { template, runtimeVersion, source, assetManifest } =
    await createMonadRuntimeTemplate();
  const result = await Template.build(template, templateRef, {
    ...connection,
    cpuCount: source.template_resources.cpu_count,
    memoryMB: source.template_resources.memory_mb,
    onBuildLogs(entry) {
      process.stderr.write(
        `[monad-runtime:${entry.level}] ${entry.message.replaceAll(apiKey, '[redacted]')}\n`,
      );
    },
  });

  process.stdout.write(
    `${JSON.stringify(
      {
        sdk_version: packageMetadata.version,
        template_ref: templateRef,
        template_name: result.name,
        template_id: result.templateId,
        image_id: result.buildId,
        build_id: result.buildId,
        tags: result.tags,
        runtime_version: runtimeVersion,
        tams_revision: source.tams_revision,
        tams_apps_sandbox_tree_oid: source.tams_apps_sandbox_tree_oid,
        runtime_input_tree_oids: assetManifest.runtime_input_tree_oids,
      },
      null,
      2,
    )}\n`,
  );
} catch (error) {
  process.stderr.write(
    `Monad runtime template build failed: ${safeErrorMessage(error, [apiKey])}\n`,
  );
  process.exitCode = 1;
}
