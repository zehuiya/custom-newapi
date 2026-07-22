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
import { Button, Form } from '@douyinfe/semi-ui';
import { IconSearch } from '@douyinfe/semi-icons';
import { DATE_RANGE_PRESETS } from '../../../constants/console.constants';

const ChannelMonitorFilters = ({
  formInitValues,
  formApi,
  setFormApi,
  refresh,
  loading,
  t,
}) => (
  <Form
    initValues={formInitValues}
    getFormApi={setFormApi}
    onSubmit={refresh}
    allowEmpty
    autoComplete='off'
    layout='vertical'
    trigger='change'
  >
    <div className='flex flex-col gap-2'>
      <div className='grid grid-cols-1 gap-2 md:grid-cols-2 lg:grid-cols-4'>
        <div className='lg:col-span-2'>
          <Form.DatePicker
            field='dateRange'
            className='w-full'
            type='dateTimeRange'
            placeholder={[t('开始时间'), t('结束时间')]}
            showClear
            pure
            size='small'
            presets={DATE_RANGE_PRESETS.map((preset) => ({
              text: t(preset.text),
              start: preset.start(),
              end: preset.end(),
            }))}
          />
        </div>
        <Form.Input
          field='channel_id'
          prefix={<IconSearch />}
          placeholder={t('渠道 ID')}
          showClear
          pure
          size='small'
        />
        <Form.Input
          field='channel_name'
          prefix={<IconSearch />}
          placeholder={t('渠道名称')}
          showClear
          pure
          size='small'
        />
        <Form.Select
          field='action'
          placeholder={t('全部操作')}
          optionList={[
            { label: t('新增'), value: 'create' },
            { label: t('更新'), value: 'update' },
            { label: t('删除'), value: 'delete' },
            { label: t('状态变更'), value: 'status_change' },
            { label: t('标签变更'), value: 'tag_update' },
            { label: t('密钥管理'), value: 'key_manage' },
            { label: t('模型同步'), value: 'model_sync' },
            { label: t('凭据变更'), value: 'credential_change' },
            { label: t('系统修复'), value: 'system_repair' },
          ]}
          showClear
          pure
          size='small'
        />
        <Form.Input
          field='source'
          prefix={<IconSearch />}
          placeholder={t('变更来源')}
          showClear
          pure
          size='small'
        />
        <Form.Input
          field='operator_name'
          prefix={<IconSearch />}
          placeholder={t('操作人')}
          showClear
          pure
          size='small'
        />
      </div>

      <div className='flex justify-end gap-2'>
        <Button
          type='tertiary'
          htmlType='submit'
          loading={loading}
          size='small'
        >
          {t('查询')}
        </Button>
        <Button
          type='tertiary'
          size='small'
          onClick={() => {
            formApi?.reset?.();
            setTimeout(() => refresh(), 100);
          }}
        >
          {t('重置')}
        </Button>
      </div>
    </div>
  </Form>
);

export default ChannelMonitorFilters;
