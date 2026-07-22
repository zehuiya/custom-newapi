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
import { Typography } from '@douyinfe/semi-ui';
import { History } from 'lucide-react';

const { Text } = Typography;

const ChannelMonitorActions = ({ recordCount, t }) => (
  <div className='flex w-full flex-col items-start justify-between gap-1 md:flex-row md:items-center'>
    <div className='flex items-center text-orange-500'>
      <History className='mr-2' size={18} />
      <Text>{t('渠道配置变更记录')}</Text>
    </div>
    <Text type='tertiary'>
      {t('共 {{count}} 条记录', { count: recordCount })}
    </Text>
  </div>
);

export default ChannelMonitorActions;
