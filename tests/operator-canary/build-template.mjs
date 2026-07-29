import { readFile } from 'node:fs/promises';
import { Template } from 'e2b';
import {
  normalizeApiUrl,
  PINNED_E2B_SDK_VERSION,
  requiredEnv,
  safeErrorMessage,
} from './lifecycle-core.mjs';
import { createCanaryTemplateDefinition } from './template-definition.mjs';

const environment = process.env;
const apiKey = requiredEnv(environment, 'E2B_API_KEY');
const apiUrl = normalizeApiUrl(requiredEnv(environment, 'E2B_API_URL'));
const domain = requiredEnv(environment, 'E2B_DOMAIN');
const templateName = requiredEnv(environment, 'E2B_TEMPLATE_NAME');

if (!/^[a-z0-9][a-z0-9-]*:[a-z0-9][a-z0-9.-]*$/.test(templateName)) {
  throw new Error(
    'E2B_TEMPLATE_NAME must be a lowercase immutable name:tag reference',
  );
}

const packageMetadata = JSON.parse(
  await readFile(new URL('./node_modules/e2b/package.json', import.meta.url), 'utf8'),
);
if (packageMetadata.version !== PINNED_E2B_SDK_VERSION) {
  throw new Error(
    `e2b SDK ${packageMetadata.version} is installed; expected ${PINNED_E2B_SDK_VERSION}`,
  );
}

const connection = {
  apiKey,
  apiUrl,
  domain,
  requestTimeoutMs: 10 * 60 * 1000,
};

try {
  if (await Template.exists(templateName, connection)) {
    throw new Error(
      `template reference ${templateName} already exists; choose a new immutable tag`,
    );
  }

  const definition = createCanaryTemplateDefinition();

  const result = await Template.build(definition, templateName, {
    ...connection,
    cpuCount: 2,
    memoryMB: 2048,
    onBuildLogs(entry) {
      process.stderr.write(
        `[template-build:${entry.level}] ${entry.message.replaceAll(apiKey, '[redacted]')}\n`,
      );
    },
  });

  process.stdout.write(
    `${JSON.stringify(
      {
        sdk_version: packageMetadata.version,
        template_ref: templateName,
        template_name: result.name,
        template_id: result.templateId,
        build_id: result.buildId,
        tags: result.tags,
      },
      null,
      2,
    )}\n`,
  );
} catch (error) {
  process.stderr.write(
    `template build failed: ${safeErrorMessage(error, [apiKey])}\n`,
  );
  process.exitCode = 1;
}
