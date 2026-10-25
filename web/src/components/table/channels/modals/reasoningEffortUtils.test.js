import test from 'node:test';
import assert from 'node:assert/strict';

import {
  buildReasoningEffortFilter,
  normalizeReasoningEfforts,
  readReasoningEffortFilter,
} from './reasoningEffortUtils.js';

test('custom reasoning levels preserve case and remove whitespace and duplicates', () => {
  assert.deepEqual(
    normalizeReasoningEfforts([
      ' high ',
      'max',
      'future-level',
      'high',
      'HIGH',
      '',
      null,
    ]),
    ['high', 'max', 'future-level', 'HIGH'],
  );
});

test('legacy channels default to unrestricted and disabling clears an existing rule', () => {
  assert.equal(
    readReasoningEffortFilter(undefined).reasoning_effort_filter_enabled,
    false,
  );
  assert.equal(
    readReasoningEffortFilter(null).reasoning_effort_filter_enabled,
    false,
  );
  const values = readReasoningEffortFilter({
    allowed_values: ['high'],
    allow_missing: true,
  });
  values.reasoning_effort_filter_enabled = false;
  assert.equal(buildReasoningEffortFilter(values), undefined);
  assert.equal(
    JSON.stringify({
      reasoning_effort_filter: buildReasoningEffortFilter(values),
    }),
    '{}',
  );
});

test('multiple values and explicit none round trip independently of missing', () => {
  const filter = {
    allowed_values: ['none', 'high', 'custom-max'],
    allow_missing: false,
  };
  assert.deepEqual(
    buildReasoningEffortFilter(readReasoningEffortFilter(filter)),
    filter,
  );
  assert.deepEqual(
    buildReasoningEffortFilter(
      readReasoningEffortFilter({ allowed_values: [], allow_missing: true }),
    ),
    { allowed_values: [], allow_missing: true },
  );
});
