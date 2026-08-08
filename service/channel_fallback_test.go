package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

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
