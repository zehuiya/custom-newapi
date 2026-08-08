import test from 'node:test';
import assert from 'node:assert/strict';

import {
  formatFallbackChannels,
  moveFallbackChannel,
  normalizeFallbackChannelIds,
} from './fallbackChannelUtils.js';

test('normalizeFallbackChannelIds preserves order and removes invalid duplicates', () => {
  assert.deepEqual(
    normalizeFallbackChannelIds([3, '2', 3, 0, -1, 'x', 5]),
    [3, 2, 5],
  );
});

test('moveFallbackChannel changes only the requested positions', () => {
  assert.deepEqual(moveFallbackChannel([2, 3, 4], 2, 1), [2, 4, 3]);
  assert.deepEqual(moveFallbackChannel([2, 3], 0, -1), [2, 3]);
});

test('formatFallbackChannels includes channel names and stale IDs', () => {
  assert.equal(
    formatFallbackChannels(
      [2, 9],
      [{ id: 2, name: 'fallback-two', status: 1 }],
      '未设置',
    ),
    '#2 fallback-two\n#9（已删除）',
  );
  assert.equal(formatFallbackChannels([], [], '未设置'), '未设置');
});
