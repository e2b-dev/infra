export const PINNED_E2B_SDK_VERSION = '2.21.0';

export function requiredEnv(environment, name) {
  const value = environment[name]?.trim();
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function normalizeApiUrl(value) {
  const url = new URL(value);
  if (url.protocol !== 'https:') {
    throw new Error('E2B_API_URL must use HTTPS');
  }
  if (url.username || url.password || url.search || url.hash) {
    throw new Error('E2B_API_URL must not contain credentials, query, or fragment');
  }
  url.pathname = url.pathname.replace(/\/+$/, '');
  return url.toString().replace(/\/$/, '');
}

export function parseForkResponse(status, payload) {
  if (status !== 201) {
    throw new Error(`fork request returned HTTP ${status}`);
  }
  if (!Array.isArray(payload) || payload.length !== 1) {
    throw new Error('fork response must contain exactly one result');
  }

  const [result] = payload;
  if (!result || typeof result !== 'object' || Array.isArray(result)) {
    throw new Error('fork result must be an object');
  }
  const hasSandbox = result.sandbox !== undefined && result.sandbox !== null;
  const hasError = result.error !== undefined && result.error !== null;
  if (hasSandbox === hasError) {
    throw new Error('fork result must contain exactly one of sandbox or error');
  }
  if (hasError) {
    throw new Error('fork result reported a sandbox creation error');
  }

  const sandboxId = result.sandbox?.sandboxID;
  if (typeof sandboxId !== 'string' || sandboxId.trim() === '') {
    throw new Error('fork result has no sandboxID');
  }
  return sandboxId;
}

export function assertBoundedCapacity(sandboxes, maximum = 2) {
  if (!Array.isArray(sandboxes)) {
    throw new Error('sandbox inventory must be an array');
  }
  if (sandboxes.length > maximum) {
    throw new Error(
      `canary capacity exceeded: found ${sandboxes.length}, maximum is ${maximum}`,
    );
  }
}

export async function collectPaginator(paginator, options, maximumItems = 200) {
  const items = [];
  while (paginator.hasNext) {
    const page = await paginator.nextItems(options);
    items.push(...page);
    if (items.length > maximumItems) {
      throw new Error(`paginator exceeded the ${maximumItems}-item safety bound`);
    }
  }
  return items;
}

export function safeErrorMessage(error, secrets = []) {
  let message = error instanceof Error ? error.message : String(error);
  for (const secret of secrets) {
    if (secret) {
      message = message.split(secret).join('[redacted]');
    }
  }
  return message;
}
