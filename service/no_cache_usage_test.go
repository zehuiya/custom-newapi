package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
