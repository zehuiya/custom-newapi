package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}

func uintPtr(v uint) *uint {
	return &v
}

func TestChannelTokenLimitSatisfies(t *testing.T) {
	require.True(t, (*ChannelTokenLimit)(nil).Satisfies(&Channel{}))

	limit := &ChannelTokenLimit{InputTokens: 100, MaxTokens: 50}
	require.True(t, limit.Satisfies(&Channel{}))
	require.True(t, limit.Satisfies(&Channel{MaxContextTokens: intPtr(0), MaxOutputTokens: intPtr(0)}))
	require.True(t, limit.Satisfies(&Channel{MaxContextTokens: intPtr(151), MaxOutputTokens: intPtr(50)}))
	require.False(t, limit.Satisfies(&Channel{MaxContextTokens: intPtr(150)}))
	require.False(t, limit.Satisfies(&Channel{MaxOutputTokens: intPtr(49)}))
	require.True(t, limit.Satisfies(&Channel{MinInputTokens: intPtr(0)}))
	require.True(t, limit.Satisfies(&Channel{MinInputTokens: intPtr(100)}))
	require.False(t, limit.Satisfies(&Channel{MinInputTokens: intPtr(101)}))
	require.True(t, limit.Satisfies(&Channel{MaxInputTokens: intPtr(0)}))
	require.True(t, limit.Satisfies(&Channel{MaxInputTokens: intPtr(100)}))
	require.False(t, limit.Satisfies(&Channel{MaxInputTokens: intPtr(99)}))
}

func TestChannelSelectionConstraintRequiresResponsesWithoutApplyingUnknownTokenLimits(t *testing.T) {
	disabledSetting := `{"responses_enabled":false}`
	legacyChannel := &Channel{MinInputTokens: intPtr(100)}
	disabledChannel := &Channel{Setting: &disabledSetting}

	constraint := &ChannelSelectionConstraint{RequireResponses: true}
	require.True(t, constraint.Satisfies(legacyChannel))
	require.False(t, constraint.Satisfies(disabledChannel))

	constraint.TokenLimit = &ChannelTokenLimit{InputTokens: 10, MaxTokens: 10}
	require.False(t, constraint.Satisfies(legacyChannel))
}

func TestGetRandomSatisfiedChannelWithTokenLimitSkipsInvalidPriority(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroup2Model2Channels := group2model2channels
	oldChannelsIDM := channelsIDM
	defer func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		group2model2channels = oldGroup2Model2Channels
		channelsIDM = oldChannelsIDM
	}()

	common.MemoryCacheEnabled = true
	group2model2channels = map[string]map[string][]int{
		"default": {
			"test-model": {1, 2},
		},
	}
	channelsIDM = map[int]*Channel{
		1: {
			Id:              1,
			Priority:        int64Ptr(100),
			Weight:          uintPtr(1000),
			MaxOutputTokens: intPtr(100),
			MinInputTokens:  intPtr(100),
			MaxInputTokens:  intPtr(200),
		},
		2: {
			Id:              2,
			Priority:        int64Ptr(50),
			Weight:          uintPtr(1),
			MaxOutputTokens: intPtr(100),
		},
	}

	hasContextLimit, hasOutputLimit, hasMinInputLimit, hasMaxInputLimit := TokenLimitedChannelFlagsForGroupModel("default", "test-model")
	require.False(t, hasContextLimit)
	require.True(t, hasOutputLimit)
	require.True(t, hasMinInputLimit)
	require.True(t, hasMaxInputLimit)

	channel, err := GetRandomSatisfiedChannel("default", "test-model", 0)
	require.NoError(t, err)
	require.Equal(t, 1, channel.Id)

	channel, err = GetRandomSatisfiedChannelWithTokenLimit("default", "test-model", 0, &ChannelTokenLimit{
		InputTokens: 1,
		MaxTokens:   20,
	})
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)

	channel, err = GetRandomSatisfiedChannelWithTokenLimit("default", "test-model", 0, &ChannelTokenLimit{
		InputTokens: 201,
		MaxTokens:   20,
	})
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}

func TestGetRandomSatisfiedChannelWithSelectionConstraintSkipsResponsesDisabledChannel(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroup2Model2Channels := group2model2channels
	oldChannelsIDM := channelsIDM
	defer func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		group2model2channels = oldGroup2Model2Channels
		channelsIDM = oldChannelsIDM
	}()

	disabledSetting := `{"responses_enabled":false}`
	common.MemoryCacheEnabled = true
	group2model2channels = map[string]map[string][]int{
		"default": {"responses-model": {1, 2}},
	}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Priority: int64Ptr(100), Weight: uintPtr(1000), Setting: &disabledSetting},
		2: {Id: 2, Priority: int64Ptr(50), Weight: uintPtr(1)},
	}

	channel, err := GetRandomSatisfiedChannel("default", "responses-model", 0)
	require.NoError(t, err)
	require.Equal(t, 1, channel.Id, "other endpoints keep the original selection behavior")

	channel, err = GetRandomSatisfiedChannelWithSelectionConstraint("default", "responses-model", 0, &ChannelSelectionConstraint{RequireResponses: true})
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}
