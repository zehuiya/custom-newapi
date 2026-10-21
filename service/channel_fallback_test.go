package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRetryParamBuildsIndependentEndpointAndTokenConstraints(t *testing.T) {
	require.Nil(t, (*RetryParam)(nil).GetSelectionConstraint())
	for _, path := range []string{"", "/v1/responses/compact", "/v1/completions", "/v1/embeddings", "/v1/realtime"} {
		require.Nil(t, (&RetryParam{RequestPath: path}).GetSelectionConstraint(), path)
	}
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions", "/pg/chat/completions"} {
		retryParam := &RetryParam{RequestPath: path}
		constraint := retryParam.GetSelectionConstraint()
		require.NotNil(t, constraint)
		require.Equal(t, path, constraint.RequestPath)
		require.Nil(t, constraint.TokenLimit)

		retryParam.TokenLimit = &model.ChannelTokenLimit{InputTokens: 100, MaxTokens: 20}
		constraint = retryParam.GetSelectionConstraint()
		require.Equal(t, retryParam.TokenLimit, constraint.TokenLimit)
		require.Equal(t, path, constraint.RequestPath)
	}
}

func TestEndpointConstraintsApplyToFallbackAndRetrySelection(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	disabled := `{"responses_enabled":false,"messages_enabled":false,"chat_completions_enabled":false}`
	channels := []model.Channel{
		{Id: 81001, Name: "endpoint-disabled", Key: "test-key", Group: "endpoint-fallback-test", Models: "endpoint-model", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(100)), Setting: &disabled},
		{Id: 81002, Name: "legacy-enabled", Key: "test-key", Group: "endpoint-fallback-test", Models: "endpoint-model", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(50)), MaxOutputTokens: common.GetPointer(100)},
	}
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id in ?", []int{81001, 81002}).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id in ?", []int{81001, 81002}).Delete(&model.Channel{}).Error)
		model.InitChannelCache()
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	})
	require.NoError(t, model.DB.Create(&channels).Error)
	for i := range channels {
		require.NoError(t, channels[i].AddAbilities(model.DB))
	}
	model.InitChannelCache()

	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions", "/pg/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			param := &RetryParam{Ctx: &gin.Context{}, TokenGroup: "endpoint-fallback-test", ModelName: "endpoint-model", RequestPath: path}
			param.ConfigureFallback(1, []int{81001, 81002}, "endpoint-fallback-test")
			channel, err := CacheGetFallbackSatisfiedChannel(param, 81001)
			require.ErrorContains(t, err, "does not satisfy request constraints")
			require.Nil(t, channel)

			channel, err = CacheGetFallbackSatisfiedChannel(param, 81002)
			require.NoError(t, err)
			require.Equal(t, 81002, channel.Id)

			param.SetRetry(1)
			channel, group, err := CacheGetRandomSatisfiedChannel(param)
			require.NoError(t, err)
			require.Equal(t, "endpoint-fallback-test", group)
			require.NotNil(t, channel)
			require.Equal(t, 81002, channel.Id, "retries preserve the original endpoint constraint")

			param.TokenLimit = &model.ChannelTokenLimit{MaxTokens: 101}
			channel, err = CacheGetFallbackSatisfiedChannel(param, 81002)
			require.ErrorContains(t, err, "does not satisfy request constraints")
			require.Nil(t, channel)
		})
	}
}

func TestRetryParamFallbackOrderThenOriginalRetry(t *testing.T) {
	retryParam := &RetryParam{Retry: common.GetPointer(0)}
	retryParam.ConfigureFallback(1, []int{2, 3}, "default")

	require.Equal(t, 0, retryParam.GetAttemptIndex())
	require.True(t, retryParam.HasNextRetry(RetryChannelSourceInitial, 2))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceInitial)

	channelID, ok := retryParam.NextFallbackChannelID()
	require.True(t, ok)
	require.Equal(t, 2, channelID)
	require.Equal(t, 0, retryParam.GetRetry())
	require.True(t, retryParam.HasNextRetry(RetryChannelSourceFallback, 2))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceFallback)

	channelID, ok = retryParam.NextFallbackChannelID()
	require.True(t, ok)
	require.Equal(t, 3, channelID)
	require.Equal(t, 0, retryParam.GetRetry())
	require.True(t, retryParam.HasNextRetry(RetryChannelSourceFallback, 2))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceFallback)

	require.True(t, retryParam.PrepareOriginalRetry(2))
	require.Equal(t, 1, retryParam.GetRetry())
	require.Equal(t, 3, retryParam.GetAttemptIndex())
	require.True(t, retryParam.HasNextRetry(RetryChannelSourceOriginal, 2))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceOriginal)

	require.Equal(t, 2, retryParam.GetRetry())
	require.False(t, retryParam.HasNextRetry(RetryChannelSourceOriginal, 2))
}

func TestRetryParamFallbackWorksWhenOriginalRetryDisabled(t *testing.T) {
	retryParam := &RetryParam{Retry: common.GetPointer(0)}
	retryParam.ConfigureFallback(1, []int{2}, "default")

	require.True(t, retryParam.HasNextRetry(RetryChannelSourceInitial, 0))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceInitial)
	channelID, ok := retryParam.NextFallbackChannelID()
	require.True(t, ok)
	require.Equal(t, 2, channelID)
	require.False(t, retryParam.HasNextRetry(RetryChannelSourceFallback, 0))
	require.False(t, retryParam.PrepareOriginalRetry(0))
}

func TestRetryParamEmptyFallbackKeepsOriginalRetryFlow(t *testing.T) {
	retryParam := &RetryParam{Retry: common.GetPointer(0)}
	retryParam.ConfigureFallback(1, nil, "default")

	require.True(t, retryParam.HasNextRetry(RetryChannelSourceInitial, 1))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceInitial)
	require.True(t, retryParam.PrepareOriginalRetry(1))
	require.Equal(t, 1, retryParam.GetRetry())
	require.False(t, retryParam.HasNextRetry(RetryChannelSourceOriginal, 1))
}

func TestRetryParamNormalizesFallbackListWithoutFollowingNestedSettings(t *testing.T) {
	retryParam := &RetryParam{Retry: common.GetPointer(0)}
	retryParam.ConfigureFallback(1, []int{1, 2, 2, -1, 3}, "default")

	channelID, ok := retryParam.NextFallbackChannelID()
	require.True(t, ok)
	require.Equal(t, 2, channelID)
	channelID, ok = retryParam.NextFallbackChannelID()
	require.True(t, ok)
	require.Equal(t, 3, channelID)
	_, ok = retryParam.NextFallbackChannelID()
	require.False(t, ok)

	// Selecting channel 2 does not reconfigure this request from channel 2's settings.
	require.Equal(t, "default", retryParam.GetFallbackGroup())
}

func TestRetryParamPreservesAutoGroupRetryReset(t *testing.T) {
	retryParam := &RetryParam{Retry: common.GetPointer(0)}
	retryParam.ConfigureFallback(1, nil, "group-a")
	retryParam.AdvanceAfterAttempt(RetryChannelSourceInitial)
	require.True(t, retryParam.PrepareOriginalRetry(1))

	retryParam.SetRetry(0)
	retryParam.ResetRetryNextTry()
	require.True(t, retryParam.HasNextRetry(RetryChannelSourceOriginal, 1))
	retryParam.AdvanceAfterAttempt(RetryChannelSourceOriginal)
	require.Equal(t, 0, retryParam.GetRetry())
}
