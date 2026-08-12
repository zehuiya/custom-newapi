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

import { timestamp2string } from '../../../helpers';

export const CHANNEL_AUDIT_FIELD_LABELS = {
  id: '渠道 ID',
  type: '类型',
  name: '名称',
  key: '密钥',
  openai_organization: '组织',
  test_model: '默认测试模型',
  status: '状态',
  weight: '渠道权重',
  priority: '渠道优先级',
  max_context_tokens: '最大上下文',
  max_output_tokens: '最大输出',
  min_input_tokens: '最小输入',
  max_input_tokens: '最大输入',
  base_url: 'API地址',
  other: '其他配置',
  models: '模型',
  group: '分组',
  groups: '分组',
  model_mapping: '模型重定向',
  status_code_mapping: '状态码复写',
  auto_ban: '是否自动禁用',
  tag: '渠道标签',
  setting: '渠道设置',
  param_override: '参数覆盖',
  header_override: '请求头覆盖',
  remark: '备注',
  channel_info: '多密钥配置',
  settings: '其他设置',
  multi_key_mode: '密钥聚合模式',
  force_format: '强制格式化',
  force_stream: '强制流式传输',
  proxy: '代理地址',
  pass_through_body_enabled: '透传请求体',
  system_prompt: '系统提示词',
  system_prompt_override: '系统提示词拼接',
  azure_responses_version: 'Azure Responses API 版本',
  vertex_key_type: 'Vertex 密钥格式',
  aws_key_type: 'AWS 密钥格式',
  allow_service_tier: '允许 service_tier 透传',
  disable_store: '禁用 store 透传',
  allow_safety_identifier: '允许 safety_identifier 透传',
  allow_include_obfuscation: '允许 stream_options.include_obfuscation 透传',
  allow_inference_geo: '允许 inference_geo 透传',
  allow_speed: '允许 speed 透传',
  claude_beta_query: 'Claude 强制 beta=true',
  upstream_model_update_check_enabled: '是否检测上游模型更新',
  upstream_model_update_auto_sync_enabled: '是否自动同步上游模型更新',
  upstream_model_update_ignored_models: '已忽略模型',
};

export const getAuditActionMeta = (action, t) => {
  switch (String(action || '').toLowerCase()) {
    case 'create':
    case 'created':
    case 'add':
      return { label: t('新增'), color: 'green' };
    case 'update':
    case 'updated':
    case 'edit':
    case 'modify':
      return { label: t('更新'), color: 'blue' };
    case 'delete':
    case 'deleted':
    case 'remove':
      return { label: t('删除'), color: 'red' };
    case 'status_change':
      return { label: t('状态变更'), color: 'orange' };
    case 'tag_update':
      return { label: t('标签变更'), color: 'cyan' };
    case 'key_manage':
      return { label: t('密钥管理'), color: 'purple' };
    case 'model_sync':
      return { label: t('模型同步'), color: 'indigo' };
    case 'credential_change':
      return { label: t('凭据变更'), color: 'light-blue' };
    case 'system_repair':
      return { label: t('系统修复'), color: 'yellow' };
    default:
      return { label: action || t('未知'), color: 'grey' };
  }
};

const AUDIT_SOURCE_LABELS = {
  create: '创建渠道',
  update: '编辑渠道',
  delete: '删除渠道',
  copy: '复制渠道',
  batch_delete: '批量删除渠道',
  delete_disabled: '删除已禁用渠道',
  batch_set_tag: '批量设置渠道标签',
  automatic_status: '自动状态变更',
};

export const getAuditSourceLabel = (source, t) => {
  const normalized = String(source || '').toLowerCase();
  if (!normalized) return '-';
  const sourceKey = normalized.startsWith('channel_')
    ? normalized.slice('channel_'.length)
    : normalized;
  if (AUDIT_SOURCE_LABELS[sourceKey]) {
    return t(AUDIT_SOURCE_LABELS[sourceKey]);
  }
  if (sourceKey.startsWith('tag_')) return t('渠道标签操作');
  if (sourceKey.startsWith('multi_key_')) return t('多密钥管理');
  if (sourceKey.startsWith('upstream_')) return t('上游模型同步');
  if (sourceKey.startsWith('codex_')) return t('Codex 凭据操作');
  return source;
};

export const formatAuditTime = (value) => {
  if (value === undefined || value === null || value === '') return '-';
  const numericValue = Number(value);
  if (Number.isFinite(numericValue)) {
    const seconds =
      numericValue > 1000000000000 ? numericValue / 1000 : numericValue;
    return timestamp2string(seconds);
  }
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) return String(value);
  return timestamp2string(parsed / 1000);
};

export const parseChangedFields = (value) => {
  if (Array.isArray(value)) return value.filter(Boolean).map(String);
  if (!value) return [];
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) return [];
    try {
      const parsed = JSON.parse(trimmed);
      if (Array.isArray(parsed)) return parsed.filter(Boolean).map(String);
    } catch {
      // 兼容旧记录中的逗号分隔字段。
    }
    return trimmed
      .split(',')
      .map((field) => field.trim())
      .filter(Boolean);
  }
  return Object.keys(value);
};

export const getAuditFieldLabel = (field, t) => {
  const fieldName = String(field || '');
  const segments = fieldName.split('.');
  const leaf = segments[segments.length - 1];
  const sourceLabel =
    CHANNEL_AUDIT_FIELD_LABELS[fieldName] ||
    CHANNEL_AUDIT_FIELD_LABELS[leaf] ||
    fieldName ||
    '未知';
  return t(sourceLabel);
};
