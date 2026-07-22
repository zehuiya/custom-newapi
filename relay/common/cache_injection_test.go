package common

import (
	"net/http/httptest"
	"testing"

	basecommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func percentagePointer(value int) *int {
	return &value
}

func TestCalculateSyntheticCacheTokensDefaultRange(t *testing.T) {
	setting := dto.ChannelSettings{}

	minPercentage, maxPercentage := setting.GetCachePercentageRange()
	require.Equal(t, dto.DefaultCachePercentageMin, minPercentage)
	require.Equal(t, dto.DefaultCachePercentageMax, maxPercentage)

	for i := 0; i < 100; i++ {
		cachedTokens := CalculateSyntheticCacheTokens(10000, setting)
		require.GreaterOrEqual(t, cachedTokens, 5000)
		require.LessOrEqual(t, cachedTokens, 9000)
	}
}

func TestCalculateSyntheticCacheTokensFixedPercentage(t *testing.T) {
	tests := []struct {
		name       string
		total      int
		percentage int
		want       int
	}{
		{name: "zero percent", total: 10000, percentage: 0, want: 0},
		{name: "sixty percent", total: 10000, percentage: 60, want: 6000},
		{name: "integer truncation", total: 3333, percentage: 33, want: 1099},
		{name: "one hundred percent", total: 10000, percentage: 100, want: 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := dto.ChannelSettings{
				CachePercentageMin: percentagePointer(tt.percentage),
				CachePercentageMax: percentagePointer(tt.percentage),
			}
			require.Equal(t, tt.want, CalculateSyntheticCacheTokens(tt.total, setting))
		})
	}
}

func TestCalculateSyntheticCacheTokensConfiguredRange(t *testing.T) {
	setting := dto.ChannelSettings{
		CachePercentageMin: percentagePointer(20),
		CachePercentageMax: percentagePointer(30),
	}

	for i := 0; i < 100; i++ {
		cachedTokens := CalculateSyntheticCacheTokens(10000, setting)
		require.GreaterOrEqual(t, cachedTokens, 2000)
		require.LessOrEqual(t, cachedTokens, 3000)
	}
}

func TestCalculateSyntheticCacheTokensInvalidRangeFallsBackToDefaults(t *testing.T) {
	setting := dto.ChannelSettings{
		CachePercentageMin: percentagePointer(90),
		CachePercentageMax: percentagePointer(10),
	}

	for i := 0; i < 100; i++ {
		cachedTokens := CalculateSyntheticCacheTokens(10000, setting)
		require.GreaterOrEqual(t, cachedTokens, 5000)
		require.LessOrEqual(t, cachedTokens, 9000)
	}
}

func TestCalculateSyntheticCacheTokensNonPositiveTotal(t *testing.T) {
	setting := dto.ChannelSettings{
		CachePercentageMin: percentagePointer(100),
		CachePercentageMax: percentagePointer(100),
	}

	require.Zero(t, CalculateSyntheticCacheTokens(0, setting))
	require.Zero(t, CalculateSyntheticCacheTokens(-1, setting))
}

func TestRelayInfoReusesSyntheticCachePercentageAcrossResponseStages(t *testing.T) {
	info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelSetting: dto.ChannelSettings{
		CacheEnabled:       true,
		CachePercentageMin: percentagePointer(0),
		CachePercentageMax: percentagePointer(100),
	}}}

	first := info.CalculateSyntheticCacheTokens(10000)
	require.NotNil(t, info.syntheticCachePercent)
	sampledPercentage := info.syntheticCachePercent
	second := info.CalculateSyntheticCacheTokens(20000)

	require.Same(t, sampledPercentage, info.syntheticCachePercent)
	require.Equal(t, first*2, second)
}

func TestRelayInfoRemembersZeroPercentSample(t *testing.T) {
	info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelSetting: dto.ChannelSettings{
		CacheEnabled:       true,
		CachePercentageMin: percentagePointer(0),
		CachePercentageMax: percentagePointer(0),
	}}}

	require.Zero(t, info.CalculateSyntheticCacheTokens(10000))
	require.NotNil(t, info.syntheticCachePercent)
	sampledPercentage := info.syntheticCachePercent
	require.Zero(t, info.CalculateSyntheticCacheTokens(20000))
	require.Same(t, sampledPercentage, info.syntheticCachePercent)
}

func TestInitChannelMetaResetsSyntheticCachePercentageForRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelSetting: dto.ChannelSettings{
		CachePercentageMin: percentagePointer(0),
		CachePercentageMax: percentagePointer(0),
	}}}
	require.Zero(t, info.CalculateSyntheticCacheTokens(10000))
	require.NotNil(t, info.syntheticCachePercent)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	basecommon.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		CachePercentageMin: percentagePointer(100),
		CachePercentageMax: percentagePointer(100),
	})
	info.InitChannelMeta(ctx)

	require.Nil(t, info.syntheticCachePercent)
	require.Equal(t, 10000, info.CalculateSyntheticCacheTokens(10000))
}
