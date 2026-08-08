import React, { useMemo, useState } from 'react';
import { Button, Select, Tag, Tooltip, Typography } from '@douyinfe/semi-ui';
import {
  IconChevronDown,
  IconChevronUp,
  IconDelete,
} from '@douyinfe/semi-icons';

import {
  moveFallbackChannel,
  normalizeFallbackChannelIds,
} from './fallbackChannelUtils';

const CHANNEL_STATUS_ENABLED = 1;
const MAX_FALLBACK_CHANNELS = 20;
const { Text } = Typography;

const FallbackChannelSelector = ({
  value,
  options,
  currentChannelId,
  loading,
  onChange,
  t,
}) => {
  const [pickerValue, setPickerValue] = useState(undefined);
  const selectedChannelIds = normalizeFallbackChannelIds(value);
  const optionMap = useMemo(
    () => new Map((options || []).map((option) => [Number(option.id), option])),
    [options],
  );
  const selectedSet = useMemo(
    () => new Set(selectedChannelIds),
    [selectedChannelIds],
  );
  const selectableOptions = useMemo(
    () =>
      (options || [])
        .filter(
          (option) =>
            Number(option.id) !== Number(currentChannelId) &&
            !selectedSet.has(Number(option.id)),
        )
        .map((option) => ({
          value: Number(option.id),
          label: `#${option.id} ${option.name}`,
          disabled: Number(option.status) !== CHANNEL_STATUS_ENABLED,
        })),
    [currentChannelId, options, selectedSet],
  );

  const addChannel = (channelId) => {
    const normalizedChannelId = Number(channelId);
    setPickerValue(undefined);
    if (
      !Number.isInteger(normalizedChannelId) ||
      selectedSet.has(normalizedChannelId)
    ) {
      return;
    }
    onChange?.([...selectedChannelIds, normalizedChannelId]);
  };

  const removeChannel = (channelId) => {
    onChange?.(selectedChannelIds.filter((id) => id !== channelId));
  };

  const moveChannel = (fromIndex, toIndex) => {
    onChange?.(moveFallbackChannel(selectedChannelIds, fromIndex, toIndex));
  };

  return (
    <div data-testid='fallback-channel-selector'>
      <Select
        data-testid='fallback-channel-dropdown'
        value={pickerValue}
        optionList={selectableOptions}
        placeholder={t('选择兜底渠道')}
        loading={loading}
        disabled={selectedChannelIds.length >= MAX_FALLBACK_CHANNELS}
        filter
        showClear
        searchPosition='dropdown'
        onChange={addChannel}
        onClear={() => setPickerValue(undefined)}
        style={{ width: '100%' }}
      />

      {selectedChannelIds.length === 0 ? (
        <Text type='tertiary' className='mt-2 block text-xs'>
          {t('未配置时保持原有重试流程')}
        </Text>
      ) : (
        <div className='mt-2 flex flex-col gap-2'>
          {selectedChannelIds.map((channelId, index) => {
            const option = optionMap.get(channelId);
            const enabled = Number(option?.status) === CHANNEL_STATUS_ENABLED;
            return (
              <div
                key={channelId}
                data-fallback-channel-id={channelId}
                className='flex min-h-9 items-center gap-2 border-b border-gray-100 pb-2 last:border-b-0 dark:border-gray-700'
              >
                <Tag size='small' color='blue' className='shrink-0'>
                  {index + 1}
                </Tag>
                <Text
                  ellipsis={{ showTooltip: true }}
                  className='min-w-0 flex-1'
                >
                  #{channelId} {option?.name || t('已删除')}
                </Text>
                {!option && <Tag color='red'>{t('已删除')}</Tag>}
                {option && !enabled && <Tag color='orange'>{t('已禁用')}</Tag>}
                <Tooltip content={t('上移')}>
                  <Button
                    data-testid={`fallback-channel-up-${channelId}`}
                    aria-label={t('上移')}
                    icon={<IconChevronUp />}
                    theme='borderless'
                    size='small'
                    disabled={index === 0}
                    onClick={() => moveChannel(index, index - 1)}
                  />
                </Tooltip>
                <Tooltip content={t('下移')}>
                  <Button
                    data-testid={`fallback-channel-down-${channelId}`}
                    aria-label={t('下移')}
                    icon={<IconChevronDown />}
                    theme='borderless'
                    size='small'
                    disabled={index === selectedChannelIds.length - 1}
                    onClick={() => moveChannel(index, index + 1)}
                  />
                </Tooltip>
                <Tooltip content={t('删除')}>
                  <Button
                    data-testid={`fallback-channel-remove-${channelId}`}
                    aria-label={t('删除')}
                    icon={<IconDelete />}
                    type='danger'
                    theme='borderless'
                    size='small'
                    onClick={() => removeChannel(channelId)}
                  />
                </Tooltip>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default FallbackChannelSelector;
