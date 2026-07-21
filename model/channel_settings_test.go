package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestValidateSettingsRemovesRetiredThinkingToContent(t *testing.T) {
	rawSetting := `{"force_format":true,"thinking_to_content":true,"proxy":"","future_setting":{"large_id":9007199254740993,"nested":[1,2,3]}}`
	channel := Channel{Setting: &rawSetting}

	require.NoError(t, channel.ValidateSettings())
	require.NotNil(t, channel.Setting)

	var stored map[string]json.RawMessage
	require.NoError(t, common.UnmarshalJsonStr(*channel.Setting, &stored))
	require.NotContains(t, stored, "thinking_to_content")
	require.JSONEq(t, `true`, string(stored["force_format"]))
	require.JSONEq(t, `{"large_id":9007199254740993,"nested":[1,2,3]}`, string(stored["future_setting"]))
}

func TestValidateSettingsDoesNotRewriteSettingsWithoutRetiredField(t *testing.T) {
	rawSetting := "{\n  \"proxy\": \"\",\n  \"future_large_id\": 9007199254740993\n}"
	channel := Channel{Setting: &rawSetting}

	require.NoError(t, channel.ValidateSettings())
	require.Equal(t, rawSetting, *channel.Setting)
}

func TestGetSettingIgnoresRetiredThinkingToContent(t *testing.T) {
	rawSetting := `{"thinking_to_content":true,"proxy":"socks5://127.0.0.1:1080"}`
	channel := Channel{Setting: &rawSetting}

	setting := channel.GetSetting()
	require.Equal(t, "socks5://127.0.0.1:1080", setting.Proxy)

	serialized, err := common.Marshal(setting)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "thinking_to_content")
}
