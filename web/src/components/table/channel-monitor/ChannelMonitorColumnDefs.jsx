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

import React from 'react';
import { Button, Space, Tag, Tooltip, Typography } from '@douyinfe/semi-ui';
import { IconEyeOpened } from '@douyinfe/semi-icons';
import { CHANNEL_OPTIONS } from '../../../constants';
import {
  formatAuditTime,
  getAuditActionMeta,
  getAuditFieldLabel,
  getAuditSourceLabel,
  parseChangedFields,
} from './channelMonitorUtils';

const { Text } = Typography;

const renderChangedFields = (value, record, t) => {
  const fields = parseChangedFields(value);
  const count = Number(record.change_count) || fields.length;
  if (fields.length === 0) {
    return count > 0 ? t('{{count}} 项', { count }) : '-';
  }

  const visibleFields = fields.slice(0, 2);
  const content = (
    <div className='flex max-w-[360px] flex-wrap gap-1'>
      {fields.map((field) => (
        <Tag key={field} size='small' color='grey'>
          {getAuditFieldLabel(field, t)}
        </Tag>
      ))}
    </div>
  );

  return (
    <Tooltip content={content} position='top'>
      <Space spacing={4} wrap>
        {visibleFields.map((field) => (
          <Tag key={field} size='small' color='grey'>
            {getAuditFieldLabel(field, t)}
          </Tag>
        ))}
        {fields.length > visibleFields.length && (
          <Text type='tertiary'>+{fields.length - visibleFields.length}</Text>
        )}
      </Space>
    </Tooltip>
  );
};

export const getChannelMonitorColumns = ({ t, openDetail }) => [
  {
    title: t('操作时间'),
    dataIndex: 'created_at',
    key: 'created_at',
    width: 170,
    render: (value) => formatAuditTime(value),
  },
  {
    title: t('操作类型'),
    dataIndex: 'action',
    key: 'action',
    width: 90,
    render: (value) => {
      const meta = getAuditActionMeta(value, t);
      return (
        <Tag color={meta.color} shape='circle'>
          {meta.label}
        </Tag>
      );
    },
  },
  {
    title: t('渠道 ID'),
    dataIndex: 'channel_id',
    key: 'channel_id',
    width: 90,
    render: (value) => value ?? '-',
  },
  {
    title: t('渠道名称'),
    dataIndex: 'channel_name',
    key: 'channel_name',
    width: 180,
    render: (value) => value || '-',
  },
  {
    title: t('类型'),
    dataIndex: 'channel_type',
    key: 'channel_type',
    width: 150,
    render: (value) => {
      const option = CHANNEL_OPTIONS.find(
        (item) => String(item.value) === String(value),
      );
      return option ? (
        <Tag color={option.color} shape='circle'>
          {t(option.label)}
        </Tag>
      ) : (
        (value ?? '-')
      );
    },
  },
  {
    title: t('操作人'),
    dataIndex: 'operator_name',
    key: 'operator_name',
    width: 120,
    render: (value) => value || t('系统'),
  },
  {
    title: t('变更来源'),
    dataIndex: 'source',
    key: 'source',
    width: 140,
    render: (value) => getAuditSourceLabel(value, t),
  },
  {
    title: t('来源 IP'),
    dataIndex: 'ip',
    key: 'ip',
    width: 140,
    render: (value) => value || '-',
  },
  {
    title: t('变更内容'),
    dataIndex: 'changed_fields',
    key: 'changed_fields',
    width: 240,
    render: (value, record) => renderChangedFields(value, record, t),
  },
  {
    title: t('操作'),
    key: 'operation',
    fixed: 'right',
    width: 100,
    render: (_, record) => (
      <Button
        theme='borderless'
        type='primary'
        size='small'
        icon={<IconEyeOpened />}
        onClick={() => openDetail(record)}
      >
        {t('查看详情')}
      </Button>
    ),
  },
];
