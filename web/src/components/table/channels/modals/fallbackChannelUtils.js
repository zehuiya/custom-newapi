export const normalizeFallbackChannelIds = (value) => {
  const rawValues = Array.isArray(value)
    ? value
    : String(value || '')
        .split(',')
        .map((item) => item.trim());
  const seen = new Set();
  const result = [];
  rawValues.forEach((rawValue) => {
    const channelId = Number(rawValue);
    if (!Number.isInteger(channelId) || channelId <= 0 || seen.has(channelId)) {
      return;
    }
    seen.add(channelId);
    result.push(channelId);
  });
  return result;
};

export const moveFallbackChannel = (channelIds, fromIndex, toIndex) => {
  const next = [...normalizeFallbackChannelIds(channelIds)];
  if (
    fromIndex < 0 ||
    fromIndex >= next.length ||
    toIndex < 0 ||
    toIndex >= next.length ||
    fromIndex === toIndex
  ) {
    return next;
  }
  [next[fromIndex], next[toIndex]] = [next[toIndex], next[fromIndex]];
  return next;
};

export const formatFallbackChannels = (
  channelIds,
  channelOptions,
  emptyLabel = '',
) => {
  const optionMap = new Map(
    (channelOptions || []).map((option) => [Number(option.id), option]),
  );
  const labels = normalizeFallbackChannelIds(channelIds).map((channelId) => {
    const option = optionMap.get(channelId);
    return option ? `#${channelId} ${option.name}` : `#${channelId}（已删除）`;
  });
  return labels.length > 0 ? labels.join('\n') : emptyLabel;
};
