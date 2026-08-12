package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
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

func TestValidateSettingsRejectsForceStreamWithPassThrough(t *testing.T) {
	rawSetting := `{"force_stream":true,"pass_through_body_enabled":true}`
	channel := Channel{Setting: &rawSetting}

	require.ErrorContains(t, channel.ValidateSettings(), "force_stream and pass_through_body_enabled are mutually exclusive")
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
		{
			name:       "cache reduction enabled with default percentage",
			rawSetting: `{"cache_reduction_enabled":true}`,
		},
		{
			name:       "cache reduction enabled with explicit zero percentage",
			rawSetting: `{"cache_reduction_enabled":true,"cache_reduction_percentage":0}`,
		},
		{
			name:       "cache reduction enabled with one hundred percent",
			rawSetting: `{"cache_reduction_enabled":true,"cache_reduction_percentage":100}`,
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
			errorMessage: "cache_enabled, no_cache_enabled, and cache_reduction_enabled are mutually exclusive",
		},
		{
			name:         "cache injection and reduction are mutually exclusive",
			rawSetting:   `{"cache_enabled":true,"cache_reduction_enabled":true}`,
			errorMessage: "cache_enabled, no_cache_enabled, and cache_reduction_enabled are mutually exclusive",
		},
		{
			name:         "no cache and reduction are mutually exclusive",
			rawSetting:   `{"no_cache_enabled":true,"cache_reduction_enabled":true}`,
			errorMessage: "cache_enabled, no_cache_enabled, and cache_reduction_enabled are mutually exclusive",
		},
		{
			name:         "all cache modes are mutually exclusive",
			rawSetting:   `{"cache_enabled":true,"no_cache_enabled":true,"cache_reduction_enabled":true}`,
			errorMessage: "cache_enabled, no_cache_enabled, and cache_reduction_enabled are mutually exclusive",
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
		{
			name:         "cache reduction below zero",
			rawSetting:   `{"cache_reduction_enabled":true,"cache_reduction_percentage":-1}`,
			errorMessage: "cache_reduction_percentage must be between 0 and 100",
		},
		{
			name:         "cache reduction above one hundred",
			rawSetting:   `{"cache_reduction_enabled":true,"cache_reduction_percentage":101}`,
			errorMessage: "cache_reduction_percentage must be between 0 and 100",
		},
		{
			name:         "disabled reduction still rejects invalid persisted percentage",
			rawSetting:   `{"cache_reduction_percentage":101}`,
			errorMessage: "cache_reduction_percentage must be between 0 and 100",
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

func TestGetSettingCacheReductionPercentageDefaultsAndPreservesZero(t *testing.T) {
	defaultRawSetting := `{"cache_reduction_enabled":true}`
	defaultSetting := (&Channel{Setting: &defaultRawSetting}).GetSetting()
	require.True(t, defaultSetting.CacheReductionEnabled)
	require.Nil(t, defaultSetting.CacheReductionPercentage)
	require.Equal(t, dto.DefaultCacheReductionPercentage, defaultSetting.GetCacheReductionPercentage())

	zeroRawSetting := `{"cache_reduction_enabled":true,"cache_reduction_percentage":0}`
	zeroSetting := (&Channel{Setting: &zeroRawSetting}).GetSetting()
	require.NotNil(t, zeroSetting.CacheReductionPercentage)
	require.Zero(t, zeroSetting.GetCacheReductionPercentage())
}

func TestChannelSettingsSupportsResponsesDefaultsToEnabled(t *testing.T) {
	legacySetting := (&Channel{}).GetSetting()
	require.True(t, legacySetting.SupportsResponses())

	enabledRawSetting := `{"responses_enabled":true}`
	enabledSetting := (&Channel{Setting: &enabledRawSetting}).GetSetting()
	require.NotNil(t, enabledSetting.ResponsesEnabled)
	require.True(t, enabledSetting.SupportsResponses())

	disabledRawSetting := `{"responses_enabled":false}`
	disabledSetting := (&Channel{Setting: &disabledRawSetting}).GetSetting()
	require.NotNil(t, disabledSetting.ResponsesEnabled)
	require.False(t, disabledSetting.SupportsResponses())
}

func TestValidateSettingsAcceptsOrderedFallbackChannels(t *testing.T) {
	rawSetting := `{"fallback_channel_ids":[2,3,5]}`
	channel := Channel{Id: 1, Setting: &rawSetting}

	require.NoError(t, channel.ValidateSettings())
	require.Equal(t, []int{2, 3, 5}, channel.GetSetting().FallbackChannelIDs)
}

func TestValidateSettingsRejectsInvalidFallbackChannels(t *testing.T) {
	tests := []struct {
		name         string
		channelID    int
		fallbackIDs  string
		errorMessage string
	}{
		{name: "non-positive", channelID: 1, fallbackIDs: `[0]`, errorMessage: "positive channel IDs"},
		{name: "self reference", channelID: 2, fallbackIDs: `[2]`, errorMessage: "cannot contain the channel itself"},
		{name: "duplicate", channelID: 1, fallbackIDs: `[2,2]`, errorMessage: "duplicate channel ID"},
		{name: "too many", channelID: 1, fallbackIDs: `[2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22]`, errorMessage: "cannot contain more than"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawSetting := `{"fallback_channel_ids":` + tt.fallbackIDs + `}`
			channel := Channel{Id: tt.channelID, Setting: &rawSetting}
			require.ErrorContains(t, channel.ValidateSettings(), tt.errorMessage)
		})
	}
}

func TestChannelHasEnabledKey(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		want    bool
	}{
		{name: "single key", channel: Channel{Id: 1, Key: "sk-test"}, want: true},
		{name: "blank single key", channel: Channel{Id: 2, Key: "  "}, want: false},
		{
			name: "multi key with one enabled",
			channel: Channel{
				Id:  3,
				Key: "sk-disabled\nsk-enabled",
				ChannelInfo: ChannelInfo{
					IsMultiKey:         true,
					MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
				},
			},
			want: true,
		},
		{
			name: "multi key all disabled",
			channel: Channel{
				Id:  4,
				Key: "sk-one\nsk-two",
				ChannelInfo: ChannelInfo{
					IsMultiKey: true,
					MultiKeyStatusList: map[int]int{
						0: common.ChannelStatusManuallyDisabled,
						1: common.ChannelStatusAutoDisabled,
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.channel.HasEnabledKey())
		})
	}
}
