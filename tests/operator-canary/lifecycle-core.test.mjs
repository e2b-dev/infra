import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertBoundedCapacity,
  collectPaginator,
  normalizeApiUrl,
  parseForkResponse,
  requiredEnv,
  safeErrorMessage,
} from './lifecycle-core.mjs';

test('requires non-empty environment inputs', () => {
  assert.equal(requiredEnv({ VALUE: ' configured ' }, 'VALUE'), 'configured');
  assert.throws(() => requiredEnv({}, 'VALUE'), /VALUE is required/);
  assert.throws(() => requiredEnv({ VALUE: '  ' }, 'VALUE'), /VALUE is required/);
});

test('normalizes only credential-free HTTPS API URLs', () => {
  assert.equal(
    normalizeApiUrl('https://api.e2b.example.test/'),
    'https://api.e2b.example.test',
  );
  assert.throws(() => normalizeApiUrl('http://api.example.test'), /HTTPS/);
  assert.throws(
    () => normalizeApiUrl('https://user:pass@api.example.test'),
    /must not contain credentials/,
  );
});

test('accepts exactly one successful fork result', () => {
  assert.equal(
    parseForkResponse(201, [{ sandbox: { sandboxID: 'fork-1' } }]),
    'fork-1',
  );
  assert.throws(() => parseForkResponse(200, []), /HTTP 200/);
  assert.throws(() => parseForkResponse(201, []), /exactly one/);
  assert.throws(
    () => parseForkResponse(201, [{ error: { message: 'failed' } }]),
    /creation error/,
  );
  assert.throws(
    () =>
      parseForkResponse(201, [
        { sandbox: { sandboxID: 'fork-1' }, error: { message: 'failed' } },
      ]),
    /exactly one of sandbox or error/,
  );
});

test('enforces the two-sandbox canary ceiling', () => {
  assert.doesNotThrow(() => assertBoundedCapacity([{}, {}]));
  assert.throws(() => assertBoundedCapacity([{}, {}, {}]), /capacity exceeded/);
});

test('paginator collection has a hard safety bound', async () => {
  const pages = [[{ id: 1 }], [{ id: 2 }]];
  const paginator = {
    get hasNext() {
      return pages.length > 0;
    },
    async nextItems() {
      return pages.shift();
    },
  };
  assert.deepEqual(await collectPaginator(paginator, {}, 2), [
    { id: 1 },
    { id: 2 },
  ]);

  const overflow = {
    hasNext: true,
    async nextItems() {
      this.hasNext = false;
      return [{}, {}];
    },
  };
  await assert.rejects(() => collectPaginator(overflow, {}, 1), /safety bound/);
});

test('redacts known credentials from errors', () => {
  assert.equal(
    safeErrorMessage(new Error('request with e2b_secret failed'), ['e2b_secret']),
    'request with [redacted] failed',
  );
});
