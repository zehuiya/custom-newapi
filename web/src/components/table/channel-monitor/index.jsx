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
import CardPro from '../../common/ui/CardPro';
import ChannelMonitorActions from './ChannelMonitorActions';
import ChannelMonitorFilters from './ChannelMonitorFilters';
import ChannelMonitorTable from './ChannelMonitorTable';
import ChannelChangeDetailModal from './modals/ChannelChangeDetailModal';
import { useChannelMonitorData } from '../../../hooks/channel-monitor/useChannelMonitorData';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
import { createCardProPagination } from '../../../helpers/utils';

const ChannelMonitorPage = () => {
  const monitorData = useChannelMonitorData();
  const isMobile = useIsMobile();

  return (
    <>
      <ChannelChangeDetailModal
        visible={monitorData.detailVisible}
        loading={monitorData.detailLoading}
        record={monitorData.detailRecord}
        detail={monitorData.detailData}
        onClose={monitorData.closeDetail}
        t={monitorData.t}
      />
      <CardPro
        type='type2'
        statsArea={<ChannelMonitorActions {...monitorData} />}
        searchArea={<ChannelMonitorFilters {...monitorData} />}
        paginationArea={createCardProPagination({
          currentPage: monitorData.activePage,
          pageSize: monitorData.pageSize,
          total: monitorData.recordCount,
          onPageChange: monitorData.handlePageChange,
          onPageSizeChange: monitorData.handlePageSizeChange,
          isMobile,
          t: monitorData.t,
        })}
        t={monitorData.t}
      >
        <ChannelMonitorTable {...monitorData} />
      </CardPro>
    </>
  );
};

export default ChannelMonitorPage;
