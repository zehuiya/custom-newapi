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

func TestValidateSettingsAcceptsCacheConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		rawSetting string
	}{
		{
			name:       "cache enabled with default range",
			rawSetting: `{"cache_enabled":true}`,
		},
		{
			name:       "cache enabled with explicit zero range",
			rawSetting: `{"cache_enabled":true,"cache_percentage_min":0,"cache_percentage_max":0}`,
		},
		{
			name:       "cache enabled with configured range",
			rawSetting: `{"cache_enabled":true,"cache_percentage_min":25,"cache_percentage_max":75}`,
		},
		{
			name:       "cache enabled with one hundred percent range",
			rawSetting: `{"cache_enabled":true,"cache_percentage_min":100,"cache_percentage_max":100}`,
		},
		{
			name:       "no cache enabled",
			rawSetting: `{"no_cache_enabled":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := Channel{Setting: &tt.rawSetting}
			require.NoError(t, channel.ValidateSettings())
		})
	}
}

func TestValidateSettingsRejectsInvalidCacheConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		rawSetting   string
		errorMessage string
	}{
		{
			name:         "cache and no cache are mutually exclusive",
			rawSetting:   `{"cache_enabled":true,"no_cache_enabled":true}`,
			errorMessage: "cache_enabled and no_cache_enabled cannot both be enabled",
		},
		{
			name:         "minimum below zero",
			rawSetting:   `{"cache_enabled":true,"cache_percentage_min":-1,"cache_percentage_max":90}`,
			errorMessage: "cache_percentage_min must be between 0 and 100",
		},
		{
			name:         "minimum above one hundred",
			rawSetting:   `{"cache_enabled":true,"cache_percentage_min":101,"cache_percentage_max":101}`,
			errorMessage: "cache_percentage_min must be between 0 and 100",
		},
		{
			name:         "maximum below zero",
			rawSetting:   `{"cache_enabled":true,"cache_percentage_min":0,"cache_percentage_max":-1}`,
			errorMessage: "cache_percentage_max must be between 0 and 100",
		},
		{
			name:         "maximum above one hundred",
			rawSetting:   `{"cache_enabled":true,"cache_percentage_min":0,"cache_percentage_max":101}`,
			errorMessage: "cache_percentage_max must be between 0 and 100",
		},
		{
			name:         "minimum exceeds maximum",
			rawSetting:   `{"cache_enabled":true,"cache_percentage_min":80,"cache_percentage_max":20}`,
			errorMessage: "cache_percentage_min must not exceed cache_percentage_max",
		},
		{
			name:         "disabled cache still rejects invalid persisted range",
			rawSetting:   `{"cache_percentage_min":101,"cache_percentage_max":101}`,
			errorMessage: "cache_percentage_min must be between 0 and 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := Channel{Setting: &tt.rawSetting}
			require.ErrorContains(t, channel.ValidateSettings(), tt.errorMessage)
		})
	}
}

func TestGetSettingPreservesExplicitZeroCacheRange(t *testing.T) {
	rawSetting := `{"cache_enabled":true,"cache_percentage_min":0,"cache_percentage_max":0}`
	channel := Channel{Setting: &rawSetting}

	setting := channel.GetSetting()
	require.True(t, setting.CacheEnabled)
	require.NotNil(t, setting.CachePercentageMin)
	require.NotNil(t, setting.CachePercentageMax)
	require.Zero(t, *setting.CachePercentageMin)
	require.Zero(t, *setting.CachePercentageMax)
}
