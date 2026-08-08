package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func noCacheRelayInfo(channelName string, noCacheEnabled bool, finalFormat types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		FinalRequestRelayFormat: finalFormat,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName:    channelName,
			ChannelSetting: dto.ChannelSettings{NoCacheEnabled: noCacheEnabled},
		},
	}
}

func cacheReductionRelayInfo(percentage *int, finalFormat types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		FinalRequestRelayFormat: finalFormat,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{
				CacheReductionEnabled:    true,
				CacheReductionPercentage: percentage,
			},
		},
	}
}

func TestNormalizeNoCacheUsageForRelayPlainNameWithSettingAddsCacheReadToInput(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:         130,
		InputTokens:          130,
		CompletionTokens:     20,
		TotalTokens:          150,
		PromptCacheHitTokens: 30,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	changed := NormalizeNoCacheUsageForRelay(nil, noCacheRelayInfo("openai", true, types.RelayFormatOpenAI), usage)

	require.True(t, changed)
	require.Equal(t, 160, usage.PromptTokens)
	require.Equal(t, 160, usage.InputTokens)
	require.Equal(t, 20, usage.CompletionTokens)
	require.Equal(t, 180, usage.TotalTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.InputTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.PromptCacheHitTokens)
}

func TestNormalizeNoCacheUsageForRelayClaudeAddsCacheReadToInput(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		InputTokens:      100,
		CompletionTokens: 20,
		TotalTokens:      120,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 7,
		},
		ClaudeCacheCreation5mTokens: 3,
		ClaudeCacheCreation1hTokens: 4,
	}

	changed := NormalizeNoCacheUsageForRelay(nil, noCacheRelayInfo("claude", true, types.RelayFormatClaude), usage)

	require.True(t, changed)
	require.Equal(t, 130, usage.PromptTokens)
	require.Equal(t, 130, usage.InputTokens)
	require.Equal(t, 20, usage.CompletionTokens)
	require.Equal(t, 150, usage.TotalTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 7, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 3, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 4, usage.ClaudeCacheCreation1hTokens)
}

func TestNormalizeNoCacheUsageForRelayUsesContextChannelSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{NoCacheEnabled: true})
	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 2,
		TotalTokens:      12,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 5,
		},
	}

	changed := NormalizeNoCacheUsageForRelay(ctx, &relaycommon.RelayInfo{}, usage)

	require.True(t, changed)
	require.Equal(t, 15, usage.PromptTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
}

func TestNormalizeNoCacheUsageForRelayIsIdempotent(t *testing.T) {
	info := noCacheRelayInfo("claude", true, types.RelayFormatClaude)
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	require.True(t, NormalizeNoCacheUsageForRelay(nil, info, usage))
	require.False(t, NormalizeNoCacheUsageForRelay(nil, info, usage))
	require.Equal(t, 130, usage.PromptTokens)
	require.Equal(t, 150, usage.TotalTokens)
}

func TestNormalizeNoCacheUsageForRelayIgnoresLegacyNameMarkerWithoutSetting(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	changed := NormalizeNoCacheUsageForRelay(nil, noCacheRelayInfo("openai-[no_cache]", false, types.RelayFormatOpenAI), usage)

	require.False(t, changed)
	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, 30, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 120, usage.TotalTokens)
}

func TestNormalizeNoCacheUsageForRelayUsesInputTokensDetailsFallback(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     130,
		CompletionTokens: 20,
		TotalTokens:      150,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	changed := NormalizeNoCacheUsageForRelay(nil, noCacheRelayInfo("openai", true, types.RelayFormatOpenAI), usage)

	require.True(t, changed)
	require.Equal(t, 160, usage.PromptTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.InputTokensDetails.CachedTokens)
}

func TestNormalizeNoCacheUsageForRelayTieredParamsTreatCacheReadAsPrompt(t *testing.T) {
	expr := `tier("base", p * 2 + c * 10 + cr * 0.1)`
	usedVars := billingexpr.UsedVars(expr)
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	require.True(t, NormalizeNoCacheUsageForRelay(nil, noCacheRelayInfo("claude", true, types.RelayFormatClaude), usage))
	params := BuildTieredTokenParams(usage, true, usedVars)

	require.Equal(t, 130.0, params.P)
	require.Equal(t, 20.0, params.C)
	require.Equal(t, 0.0, params.CR)
}

func TestAdjustCacheUsageForRelayReducesCacheByConfiguredPercentage(t *testing.T) {
	percentage := 10
	usage := &dto.Usage{
		PromptTokens:         1000,
		InputTokens:          1000,
		CompletionTokens:     100,
		TotalTokens:          1100,
		PromptCacheHitTokens: 900,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 900,
		},
		InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 900},
	}

	changed := AdjustCacheUsageForRelay(nil, cacheReductionRelayInfo(&percentage, types.RelayFormatOpenAI), usage)

	require.True(t, changed)
	require.Equal(t, 1090, usage.PromptTokens)
	require.Equal(t, 1090, usage.InputTokens)
	require.Equal(t, 810, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 810, usage.InputTokensDetails.CachedTokens)
	require.Equal(t, 810, usage.PromptCacheHitTokens)
	require.Equal(t, 1190, usage.TotalTokens)
}

func TestAdjustCacheUsageForRelayUsesDefaultTenPercent(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 900,
		},
	}

	require.True(t, AdjustCacheUsageForRelay(nil, cacheReductionRelayInfo(nil, types.RelayFormatClaude), usage))
	require.Equal(t, 1090, usage.PromptTokens)
	require.Equal(t, 810, usage.PromptTokensDetails.CachedTokens)
}

func TestAdjustCacheUsageForRelayReductionIsIdempotent(t *testing.T) {
	percentage := 10
	info := cacheReductionRelayInfo(&percentage, types.RelayFormatClaude)
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 900,
		},
	}

	require.True(t, AdjustCacheUsageForRelay(nil, info, usage))
	require.False(t, AdjustCacheUsageForRelay(nil, info, usage))
	require.Equal(t, 1090, usage.PromptTokens)
	require.Equal(t, 810, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1190, usage.TotalTokens)
}

func TestAdjustCacheUsageForRelayHandlesPartialStreamingUsageRefresh(t *testing.T) {
	percentage := 10
	info := cacheReductionRelayInfo(&percentage, types.RelayFormatClaude)
	usage := &dto.Usage{
		PromptTokens:     1000,
		InputTokens:      1000,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 900,
		},
	}
	require.True(t, AdjustCacheUsageForRelay(nil, info, usage))

	// A later Claude message_delta refreshes input/output but omits cache usage.
	usage.PromptTokens = 1000
	usage.CompletionTokens = 100
	usage.TotalTokens = 1100
	require.True(t, AdjustCacheUsageForRelay(nil, info, usage))
	require.Equal(t, 1090, usage.PromptTokens)
	require.Equal(t, 1090, usage.InputTokens)
	require.Equal(t, 810, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1190, usage.TotalTokens)

	// A provider may instead refresh only the original cache value.
	usage.PromptTokensDetails.CachedTokens = 900
	require.True(t, AdjustCacheUsageForRelay(nil, info, usage))
	require.Equal(t, 1090, usage.PromptTokens)
	require.Equal(t, 810, usage.PromptTokensDetails.CachedTokens)
}

func TestAdjustCacheUsageForRelayReductionUsesFallbackCacheField(t *testing.T) {
	percentage := 10
	usage := &dto.Usage{
		PromptTokens:       1000,
		CompletionTokens:   100,
		InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 900},
	}

	require.True(t, AdjustCacheUsageForRelay(nil, cacheReductionRelayInfo(&percentage, types.RelayFormatOpenAI), usage))
	require.Equal(t, 1090, usage.PromptTokens)
	require.Equal(t, 810, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 810, usage.InputTokensDetails.CachedTokens)
}

func TestAdjustCacheUsageForRelayZeroReductionIsNoOp(t *testing.T) {
	percentage := 0
	usage := &dto.Usage{
		PromptTokens: 1000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 900,
		},
	}

	require.False(t, AdjustCacheUsageForRelay(nil, cacheReductionRelayInfo(&percentage, types.RelayFormatOpenAI), usage))
	require.Equal(t, 1000, usage.PromptTokens)
	require.Equal(t, 900, usage.PromptTokensDetails.CachedTokens)
}

func TestCacheReductionAdjustedUsageFeedsBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	percentage := 10
	info := cacheReductionRelayInfo(&percentage, types.RelayFormatClaude)
	info.OriginModelName = "cache-reduction-test"
	info.StartTime = time.Now()
	info.PriceData = types.PriceData{
		ModelRatio:      1,
		CompletionRatio: 2,
		CacheRatio:      0.25,
		GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
	}
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 900,
		},
	}

	require.True(t, AdjustCacheUsageForRelay(ctx, info, usage))
	summary := calculateTextQuotaSummary(ctx, info, usage)

	require.Equal(t, 1090, summary.PromptTokens)
	require.Equal(t, 810, summary.CacheTokens)
	// 1090 normal input + 810*0.25 cache input + 100*2 output = 1492.5.
	require.Equal(t, 1493, summary.Quota)
}
