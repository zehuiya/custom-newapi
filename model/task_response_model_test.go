package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestInitTaskSnapshotsRestoreResponseModel(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		info := &relaycommon.RelayInfo{
			OriginModelName: "client-A",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:         7,
				UpstreamModelName: "upstream-B",
				ChannelSetting:    dto.ChannelSettings{RestoreResponseModel: enabled},
			},
		}
		task := InitTask(constant.TaskPlatform("test"), info)
		require.Equal(t, enabled, task.PrivateData.RestoreResponseModel)
		require.Equal(t, "client-A", task.Properties.OriginModelName)
		require.Equal(t, "upstream-B", task.Properties.UpstreamModelName)

		// A later channel edit must not change how this task's settlement is shown.
		info.ChannelSetting.RestoreResponseModel = !enabled
		require.Equal(t, enabled, task.PrivateData.RestoreResponseModel)
		encoded, err := common.Marshal(task.PrivateData)
		require.NoError(t, err)
		require.Equal(t, enabled, gjson.GetBytes(encoded, "restore_response_model").Exists())
		var restored TaskPrivateData
		require.NoError(t, common.Unmarshal(encoded, &restored))
		require.Equal(t, enabled, restored.RestoreResponseModel)
		publicJSON, err := common.Marshal(task)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(publicJSON, "private_data").Exists())
		require.NotContains(t, string(publicJSON), "restore_response_model")
	}
}

func TestTaskPrivateDataRestoreResponseModelLegacyJSON(t *testing.T) {
	var privateData TaskPrivateData
	require.NoError(t, common.UnmarshalJsonStr(`{"token_id":42,"billing_source":"wallet","upstream_task_id":"legacy-task"}`, &privateData))
	require.False(t, privateData.RestoreResponseModel)
	require.Equal(t, 42, privateData.TokenId)
	require.Equal(t, "legacy-task", privateData.UpstreamTaskID)
}
