/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useMemo } from 'react';
import { Empty, Modal, Spin, Tag, Typography } from '@douyinfe/semi-ui';
import { CHANNEL_OPTIONS } from '../../../../constants';
import {
  formatAuditTime,
  getAuditActionMeta,
  getAuditFieldLabel,
  getAuditSourceLabel,
} from '../channelMonitorUtils';

const { Text } = Typography;
const hasOwn = (value, key) =>
  Object.prototype.hasOwnProperty.call(value || {}, key);
const BOOLEAN_FIELD_NAMES = new Set([
  'auto_ban',
  'force_format',
  'pass_through_body_enabled',
  'system_prompt_override',
  'is_multi_key',
  'openrouter_enterprise',
  'claude_beta_query',
  'allow_service_tier',
  'allow_inference_geo',
  'allow_speed',
  'allow_safety_identifier',
  'disable_store',
  'allow_include_obfuscation',
  'upstream_model_update_check_enabled',
  'upstream_model_update_auto_sync_enabled',
]);

const parseMaybeJson = (value) => {
  if (typeof value !== 'string') return value;
  const trimmed = value.trim();
  if (!trimmed) return value;
  try {
    return JSON.parse(trimmed);
  } catch {
    return value;
  }
};

const isPlainObject = (value) =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

const isSensitiveField = (field) => {
  const normalized = String(field || '').toLowerCase();
  const segments = normalized.split('.');
  const leaf = segments[segments.length - 1] || '';
  return (
    segments.some((segment) =>
      ['key', 'keys', 'proxy', 'header_override', 'authorization'].includes(
        segment,
      ),
    ) ||
    /(^|_)(api_key|access_key|secret|token|password|passwd|credential|signature|authorization)(_|$)/.test(
      leaf,
    )
  );
};

const redactSensitiveUrl = (value) =>
  String(value || '')
    .replace(/^([a-z][a-z\d+.-]*:\/\/)([^/@\s]+)@/i, '$1***@')
    .replace(/([?&])([^=&#]+)=([^&#]*)/g, (match, prefix, rawKey) => {
      let key = rawKey;
      try {
        key = decodeURIComponent(rawKey);
      } catch {
        // 无法解码时继续使用原始参数名匹配。
      }
      return /(key|token|secret|password|passwd|credential|signature|authorization|auth)/i.test(
        key,
      )
        ? `${prefix}${rawKey}=***`
        : match;
    });

const redactNestedValue = (value, parentPath = '') => {
  if (Array.isArray(value)) {
    return value.map((item, index) =>
      redactNestedValue(item, `${parentPath}.${index}`),
    );
  }
  if (!isPlainObject(value)) return value;
  return Object.entries(value).reduce((result, [key, child]) => {
    const path = parentPath ? `${parentPath}.${key}` : key;
    if (isSensitiveField(path)) {
      result[key] = '***';
    } else if (key === 'base_url' && typeof child === 'string') {
      result[key] = redactSensitiveUrl(child);
    } else {
      result[key] = redactNestedValue(child, path);
    }
    return result;
  }, {});
};

const valuesEqual = (before, after) => {
  try {
    return JSON.stringify(before) === JSON.stringify(after);
  } catch {
    return before === after;
  }
};

const deriveSnapshotChanges = (beforeValue, afterValue, prefix = '') => {
  const before = parseMaybeJson(beforeValue);
  const after = parseMaybeJson(afterValue);
  if (isPlainObject(before) || isPlainObject(after)) {
    const beforeObject = isPlainObject(before) ? before : {};
    const afterObject = isPlainObject(after) ? after : {};
    const keys = new Set([
      ...Object.keys(beforeObject),
      ...Object.keys(afterObject),
    ]);
    return Array.from(keys).flatMap((key) => {
      const path = prefix ? `${prefix}.${key}` : key;
      const previous = beforeObject[key];
      const next = afterObject[key];
      if (isPlainObject(previous) || isPlainObject(next)) {
        return deriveSnapshotChanges(previous, next, path);
      }
      if (valuesEqual(previous, next)) return [];
      return [
        {
          field: path,
          before: previous,
          after: next,
          sensitive: isSensitiveField(path),
        },
      ];
    });
  }
  if (valuesEqual(before, after)) return [];
  return [
    {
      field: prefix || '配置',
      before,
      after,
      sensitive: isSensitiveField(prefix),
    },
  ];
};

const getSnapshotValue = (snapshotValue, field) => {
  const snapshot = parseMaybeJson(snapshotValue);
  if (!isPlainObject(snapshot)) return undefined;
  if (hasOwn(snapshot, field)) return snapshot[field];
  return String(field || '')
    .split('.')
    .reduce((value, segment) => value?.[segment], snapshot);
};

const normalizeChange = (change, beforeSnapshot, afterSnapshot) => {
  if (typeof change === 'string') {
    return {
      field: change,
      before: getSnapshotValue(beforeSnapshot, change),
      after: getSnapshotValue(afterSnapshot, change),
      sensitive: isSensitiveField(change),
    };
  }
  const field = change?.field || change?.key || change?.path || '配置';
  const before = hasOwn(change, 'before')
    ? change.before
    : hasOwn(change, 'old_value')
      ? change.old_value
      : getSnapshotValue(beforeSnapshot, field);
  const after = hasOwn(change, 'after')
    ? change.after
    : hasOwn(change, 'new_value')
      ? change.new_value
      : getSnapshotValue(afterSnapshot, field);
  return {
    field,
    before,
    after,
    sensitive: change?.sensitive === true || isSensitiveField(field),
  };
};

const normalizeChanges = (detail) => {
  const beforeSnapshot =
    detail?.before_snapshot === undefined
      ? detail?.before
      : detail.before_snapshot;
  const afterSnapshot =
    detail?.after_snapshot === undefined
      ? detail?.after
      : detail.after_snapshot;
  const rawChanges = parseMaybeJson(detail?.changes);
  if (Array.isArray(rawChanges) && rawChanges.length > 0) {
    return rawChanges.map((change) =>
      normalizeChange(change, beforeSnapshot, afterSnapshot),
    );
  }
  if (isPlainObject(rawChanges) && Object.keys(rawChanges).length > 0) {
    return Object.entries(rawChanges).map(([field, values]) =>
      normalizeChange(
        isPlainObject(values) ? { field, ...values } : { field, after: values },
        beforeSnapshot,
        afterSnapshot,
      ),
    );
  }
  return deriveSnapshotChanges(beforeSnapshot, afterSnapshot);
};

const isConfigured = (value) => {
  if (isPlainObject(value) && typeof value.configured === 'boolean') {
    return value.configured;
  }
  if (value === undefined || value === null || value === '') return false;
  if (Array.isArray(value)) return value.length > 0;
  return true;
};

const formatChangeValue = (
  value,
  field,
  sensitive,
  t,
  { position = 'before', beforeValue } = {},
) => {
  if (sensitive) {
    if (!isConfigured(value)) return t('未设置');
    if (position === 'after' && isConfigured(beforeValue)) {
      return t('已变更（隐藏）');
    }
    return t('已配置（隐藏）');
  }
  if (value === undefined || value === null || value === '') return t('未设置');
  if (typeof value === 'boolean') return value ? t('开') : t('关');
  const leafField = String(field || '')
    .split('.')
    .pop();
  if (BOOLEAN_FIELD_NAMES.has(leafField)) {
    return value === true || value === 1 || value === '1' ? t('开') : t('关');
  }
  if (field === 'status' || field.endsWith('.status')) {
    const statusLabels = {
      0: t('未知'),
      1: t('启用'),
      2: t('禁用'),
      3: t('自动禁用'),
    };
    if (statusLabels[value] !== undefined) return statusLabels[value];
  }
  if (field === 'type' || field.endsWith('.type')) {
    const option = CHANNEL_OPTIONS.find(
      (item) => String(item.value) === String(value),
    );
    if (option) return t(option.label);
  }
  if (field === 'base_url' || field.endsWith('.base_url')) {
    return redactSensitiveUrl(value);
  }

  const parsed = parseMaybeJson(value);
  if (typeof parsed === 'object') {
    try {
      return JSON.stringify(redactNestedValue(parsed, field), null, 2);
    } catch {
      return String(value);
    }
  }
  return String(parsed);
};

const MetadataItem = ({ label, children }) => (
  <div className='min-w-0 rounded-lg bg-gray-50 px-3 py-2 dark:bg-gray-800'>
    <Text type='tertiary' size='small' className='block'>
      {label}
    </Text>
    <div className='mt-1 break-all text-sm'>{children || '-'}</div>
  </div>
);

const ChannelChangeDetailModal = ({
  visible,
  loading,
  record,
  detail,
  onClose,
  t,
}) => {
  const data = { ...(record || {}), ...(detail || {}) };
  const changes = useMemo(() => normalizeChanges(detail || {}), [detail]);
  const actionMeta = getAuditActionMeta(data.action, t);

  return (
    <Modal
      visible={visible}
      title={t('渠道变更详情')}
      okText={t('关闭')}
      cancelButtonProps={{ style: { display: 'none' } }}
      closable={!loading}
      maskClosable={!loading}
      closeOnEsc={!loading}
      width={900}
      style={{ maxWidth: '96vw' }}
      onOk={onClose}
      onCancel={onClose}
    >
      <Spin spinning={loading}>
        <div className='flex min-h-48 flex-col gap-4'>
          <div className='grid grid-cols-1 gap-2 md:grid-cols-2 lg:grid-cols-4'>
            <MetadataItem label={t('操作类型')}>
              <Tag color={actionMeta.color} shape='circle'>
                {actionMeta.label}
              </Tag>
            </MetadataItem>
            <MetadataItem label={t('渠道')}>
              {data.channel_name || '-'}
              {data.channel_id !== undefined && data.channel_id !== null
                ? ` (#${data.channel_id})`
                : ''}
            </MetadataItem>
            <MetadataItem label={t('操作人')}>
              {data.operator_name || t('系统')}
            </MetadataItem>
            <MetadataItem label={t('操作时间')}>
              {formatAuditTime(data.created_at)}
            </MetadataItem>
            <MetadataItem label={t('变更来源')}>
              {getAuditSourceLabel(data.source, t)}
            </MetadataItem>
            <MetadataItem label={t('来源 IP')}>{data.ip || '-'}</MetadataItem>
            <MetadataItem label={t('请求方法')}>
              {data.method || '-'}
            </MetadataItem>
            <MetadataItem label={t('请求路径')}>
              {data.path || '-'}
            </MetadataItem>
          </div>

          <Text type='secondary'>{t('仅展示实际发生变化的配置项')}</Text>

          {!loading && changes.length === 0 ? (
            <Empty description={t('未检测到配置变更')} />
          ) : (
            <div className='max-h-[58vh] overflow-auto rounded-lg border border-gray-200 dark:border-gray-700'>
              <div className='min-w-[720px]'>
                <div
                  className='sticky top-0 z-10 grid gap-3 border-b border-gray-200 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-500 dark:border-gray-700 dark:bg-gray-800'
                  style={{
                    gridTemplateColumns:
                      'minmax(130px, 0.7fr) minmax(0, 1fr) minmax(0, 1fr)',
                  }}
                >
                  <span>{t('修改项')}</span>
                  <span>{t('修改前')}</span>
                  <span>{t('修改后')}</span>
                </div>
                {changes.map((change, index) => (
                  <div
                    key={`${change.field}-${index}`}
                    className='grid gap-3 border-b border-gray-100 px-3 py-3 last:border-b-0 dark:border-gray-800'
                    style={{
                      gridTemplateColumns:
                        'minmax(130px, 0.7fr) minmax(0, 1fr) minmax(0, 1fr)',
                    }}
                  >
                    <div className='min-w-0'>
                      <Text strong className='break-words'>
                        {getAuditFieldLabel(change.field, t)}
                      </Text>
                      <Text
                        type='tertiary'
                        size='small'
                        className='mt-1 block break-all'
                      >
                        {change.field}
                      </Text>
                    </div>
                    <pre className='m-0 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-gray-50 p-2 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300'>
                      {formatChangeValue(
                        change.before,
                        change.field,
                        change.sensitive,
                        t,
                        { position: 'before' },
                      )}
                    </pre>
                    <pre className='m-0 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-blue-50 p-2 text-xs text-blue-700 dark:bg-blue-950 dark:text-blue-300'>
                      {formatChangeValue(
                        change.after,
                        change.field,
                        change.sensitive,
                        t,
                        {
                          position: 'after',
                          beforeValue: change.before,
                        },
                      )}
                    </pre>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </Spin>
    </Modal>
  );
};

export default ChannelChangeDetailModal;
