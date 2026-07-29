import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createCanaryTemplateDefinition,
  TEMPLATE_MARKER_PATH,
  TEMPLATE_MARKER_VALUE,
} from './template-definition.mjs';

test('template marker is written as root while the sandbox keeps its default user', () => {
  const definition = createCanaryTemplateDefinition();
  const [instruction] = definition.instructions;

  assert.equal(instruction.type, 'RUN');
  assert.equal(instruction.args.at(-1), 'root');
  assert.match(instruction.args[0], new RegExp(TEMPLATE_MARKER_PATH));
  assert.match(instruction.args[0], new RegExp(TEMPLATE_MARKER_VALUE));
  assert.equal(
    definition.instructions.some((candidate) => candidate.type === 'USER'),
    false,
  );
});
