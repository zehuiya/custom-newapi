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

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API, showError } from '../../helpers';
import { ITEMS_PER_PAGE } from '../../constants';

const PAGE_SIZE_STORAGE_KEY = 'channel-monitor-page-size';

const toTimestamp = (value) => {
  if (!value) return null;
  if (value instanceof Date) return Math.floor(value.getTime() / 1000);
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : Math.floor(parsed / 1000);
};

export const useChannelMonitorData = () => {
  const { t } = useTranslation();
  const [records, setRecords] = useState([]);
  const [loading, setLoading] = useState(false);
  const [activePage, setActivePage] = useState(1);
  const [pageSize, setPageSize] = useState(
    () =>
      parseInt(localStorage.getItem(PAGE_SIZE_STORAGE_KEY), 10) ||
      ITEMS_PER_PAGE,
  );
  const [recordCount, setRecordCount] = useState(0);
  const [formApi, setFormApi] = useState(null);

  const [detailVisible, setDetailVisible] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailRecord, setDetailRecord] = useState(null);
  const [detailData, setDetailData] = useState(null);

  const formInitValues = {
    channel_id: '',
    channel_name: '',
    action: '',
    source: '',
    operator_name: '',
    dateRange: [],
  };

  const getFilterValues = () => {
    const values = formApi?.getValues?.() || formInitValues;
    const dateRange = Array.isArray(values.dateRange) ? values.dateRange : [];
    return {
      channel_id: values.channel_id?.trim?.() || '',
      channel_name: values.channel_name?.trim?.() || '',
      action: values.action || '',
      source: values.source?.trim?.() || '',
      operator_name: values.operator_name?.trim?.() || '',
      start_timestamp: toTimestamp(dateRange[0]),
      end_timestamp: toTimestamp(dateRange[1]),
    };
  };

  const loadRecords = async (page = 1, size = pageSize) => {
    setLoading(true);
    try {
      const params = new URLSearchParams({
        p: String(page),
        page_size: String(size),
      });
      const filters = getFilterValues();
      Object.entries(filters).forEach(([key, value]) => {
        if (value !== '' && value !== null && value !== undefined) {
          params.set(key, String(value));
        }
      });

      const res = await API.get(`/api/channel_audit/?${params.toString()}`);
      const { success, message, data } = res?.data || {};
      if (!success) {
        showError(message || t('加载渠道变更记录失败'));
        return;
      }

      const items = Array.isArray(data?.items) ? data.items : [];
      setRecords(
        items.map((item) => ({
          ...item,
          key: String(item.id),
        })),
      );
      setRecordCount(Number(data?.total) || 0);
      setActivePage(Number(data?.page) || page);
      setPageSize(Number(data?.page_size) || size);
    } catch (error) {
      showError(
        error?.response?.data?.message ||
          error?.message ||
          t('加载渠道变更记录失败'),
      );
    } finally {
      setLoading(false);
    }
  };

  const refresh = async () => {
    await loadRecords(1, pageSize);
  };

  const handlePageChange = (page) => {
    loadRecords(page, pageSize).then();
  };

  const handlePageSizeChange = (size) => {
    localStorage.setItem(PAGE_SIZE_STORAGE_KEY, String(size));
    loadRecords(1, size).then();
  };

  const openDetail = async (record) => {
    setDetailRecord(record);
    setDetailData(null);
    setDetailVisible(true);
    setDetailLoading(true);
    try {
      const res = await API.get(`/api/channel_audit/${record.id}`);
      const { success, message, data } = res?.data || {};
      if (success) {
        setDetailData(data || {});
      } else {
        showError(message || t('加载渠道变更详情失败'));
      }
    } catch (error) {
      showError(
        error?.response?.data?.message ||
          error?.message ||
          t('加载渠道变更详情失败'),
      );
    } finally {
      setDetailLoading(false);
    }
  };

  const closeDetail = () => {
    if (detailLoading) return;
    setDetailVisible(false);
    setDetailRecord(null);
    setDetailData(null);
  };

  useEffect(() => {
    loadRecords(1, pageSize).then();
  }, []);

  return {
    t,
    records,
    loading,
    activePage,
    pageSize,
    recordCount,
    formApi,
    setFormApi,
    formInitValues,
    refresh,
    handlePageChange,
    handlePageSizeChange,
    detailVisible,
    detailLoading,
    detailRecord,
    detailData,
    openDetail,
    closeDetail,
  };
};
